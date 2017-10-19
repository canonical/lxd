package main

import (
	"bytes"
	"crypto/x509"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/juju/idmclient"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2/bakery/identchecker"
	"gopkg.in/macaroon-bakery.v2/httpbakery"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/endpoints"
	"github.com/lxc/lxd/lxd/maas"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/sys"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"

	log "github.com/lxc/lxd/shared/log15"
)

// A Daemon can respond to requests from a shared client.
type Daemon struct {
	clientCerts  []x509.Certificate
	os           *sys.OS
	db           *db.Node
	maas         *maas.Controller
	cluster      *db.Cluster
	setupChan    chan struct{} // Closed when basic Daemon setup is completed
	readyChan    chan struct{} // Closed when LXD is fully ready
	shutdownChan chan struct{}

	// Tasks registry for long-running background tasks.
	tasks task.Group

	// Indexes of tasks that need to be reset when their execution interval
	// changes.
	taskPruneImages *task.Task
	taskAutoUpdate  *task.Task

	config    *DaemonConfig
	endpoints *endpoints.Endpoints
	gateway   *cluster.Gateway

	proxy func(req *http.Request) (*url.URL, error)

	externalAuth *externalAuth
}

type externalAuth struct {
	endpoint string
	bakery   *identchecker.Bakery
}

// DaemonConfig holds configuration values for Daemon.
type DaemonConfig struct {
	Group       string  // Group name the local unix socket should be chown'ed to
	RaftLatency float64 // Coarse grain measure of the cluster latency
}

// NewDaemon returns a new Daemon object with the given configuration.
func NewDaemon(config *DaemonConfig, os *sys.OS) *Daemon {
	return &Daemon{
		config:       config,
		os:           os,
		setupChan:    make(chan struct{}),
		readyChan:    make(chan struct{}),
		shutdownChan: make(chan struct{}),
	}
}

// DefaultDaemonConfig returns a DaemonConfig object with default values/
func DefaultDaemonConfig() *DaemonConfig {
	return &DaemonConfig{
		RaftLatency: 1.0,
	}
}

// DefaultDaemon returns a new, un-initialized Daemon object with default values.
func DefaultDaemon() *Daemon {
	config := DefaultDaemonConfig()
	os := sys.DefaultOS()
	return NewDaemon(config, os)
}

// Command is the basic structure for every API call.
type Command struct {
	name          string
	untrustedGet  bool
	untrustedPost bool
	get           func(d *Daemon, r *http.Request) Response
	put           func(d *Daemon, r *http.Request) Response
	post          func(d *Daemon, r *http.Request) Response
	delete        func(d *Daemon, r *http.Request) Response
	patch         func(d *Daemon, r *http.Request) Response
}

// Check whether the request comes from a trusted client.
func (d *Daemon) checkTrustedClient(r *http.Request) error {
	if r.RemoteAddr == "@" {
		// Unix socket
		return nil
	}

	if r.TLS == nil {
		return fmt.Errorf("no TLS")
	}

	if d.externalAuth != nil && r.Header.Get(httpbakery.BakeryProtocolHeader) != "" {
		ctx := httpbakery.ContextWithRequest(context.TODO(), r)
		authChecker := d.externalAuth.bakery.Checker.Auth(
			httpbakery.RequestMacaroons(r)...)
		ops := getBakeryOps(r)
		_, err := authChecker.Allow(ctx, ops...)
		return err
	}

	for i := range r.TLS.PeerCertificates {
		if util.CheckTrustState(*r.TLS.PeerCertificates[i], d.clientCerts) {
			return nil
		}
	}
	return fmt.Errorf("unauthorized")
}

// Return the bakery operations implied by the given HTTP request
func getBakeryOps(r *http.Request) []bakery.Op {
	return []bakery.Op{{
		Entity: r.URL.Path,
		Action: r.Method,
	}}
}

func writeMacaroonsRequiredResponse(b *identchecker.Bakery, r *http.Request, w http.ResponseWriter, derr *bakery.DischargeRequiredError) {
	ctx := httpbakery.ContextWithRequest(context.TODO(), r)
	caveats := append(derr.Caveats, checkers.TimeBeforeCaveat(time.Now().Add(5*time.Minute)))

	// Mint an appropriate macaroon and send it back to the client.
	m, err := b.Oven.NewMacaroon(
		ctx, httpbakery.RequestVersion(r), caveats, derr.Ops...)
	if err != nil {
		resp := errorResponse{http.StatusInternalServerError, err.Error()}
		resp.Render(w)
		return
	}

	herr := httpbakery.NewDischargeRequiredError(
		httpbakery.DischargeRequiredErrorParams{
			Macaroon:      m,
			OriginalError: derr,
			Request:       r,
		})
	herr.(*httpbakery.Error).Info.CookieNameSuffix = "auth"
	httpbakery.WriteError(ctx, w, herr)
	return
}

func isJSONRequest(r *http.Request) bool {
	for k, vs := range r.Header {
		if strings.ToLower(k) == "content-type" &&
			len(vs) == 1 && strings.ToLower(vs[0]) == "application/json" {
			return true
		}
	}

	return false
}

// State creates a new State instance liked to our internal db and os.
func (d *Daemon) State() *state.State {
	return state.NewState(d.db, d.cluster, d.maas, d.os)
}

// UnixSocket returns the full path to the unix.socket file that this daemon is
// listening on. Used by tests.
func (d *Daemon) UnixSocket() string {
	return filepath.Join(d.os.VarDir, "unix.socket")
}

func (d *Daemon) createCmd(restAPI *mux.Router, version string, c Command) {
	var uri string
	if c.name == "" {
		uri = fmt.Sprintf("/%s", version)
	} else {
		uri = fmt.Sprintf("/%s/%s", version, c.name)
	}

	restAPI.HandleFunc(uri, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Block public API requests until we're done with basic
		// initialization tasks, such setting up the cluster database.
		<-d.setupChan

		untrustedOk := (r.Method == "GET" && c.untrustedGet) || (r.Method == "POST" && c.untrustedPost)
		err := d.checkTrustedClient(r)
		if err == nil {
			logger.Debug(
				"handling",
				log.Ctx{"method": r.Method, "url": r.URL.RequestURI(), "ip": r.RemoteAddr})
		} else if untrustedOk && r.Header.Get("X-LXD-authenticated") == "" {
			logger.Debug(
				fmt.Sprintf("allowing untrusted %s", r.Method),
				log.Ctx{"url": r.URL.RequestURI(), "ip": r.RemoteAddr})
		} else if derr, ok := err.(*bakery.DischargeRequiredError); ok {
			writeMacaroonsRequiredResponse(d.externalAuth.bakery, r, w, derr)
			return
		} else {
			logger.Warn(
				"rejecting request from untrusted client",
				log.Ctx{"ip": r.RemoteAddr})
			Forbidden.Render(w)
			return
		}

		if debug && r.Method != "GET" && isJSONRequest(r) {
			newBody := &bytes.Buffer{}
			captured := &bytes.Buffer{}
			multiW := io.MultiWriter(newBody, captured)
			if _, err := io.Copy(multiW, r.Body); err != nil {
				InternalError(err).Render(w)
				return
			}

			r.Body = shared.BytesReadCloser{Buf: newBody}
			shared.DebugJson(captured)
		}

		var resp Response
		resp = NotImplemented

		switch r.Method {
		case "GET":
			if c.get != nil {
				resp = c.get(d, r)
			}
		case "PUT":
			if c.put != nil {
				resp = c.put(d, r)
			}
		case "POST":
			if c.post != nil {
				resp = c.post(d, r)
			}
		case "DELETE":
			if c.delete != nil {
				resp = c.delete(d, r)
			}
		case "PATCH":
			if c.patch != nil {
				resp = c.patch(d, r)
			}
		default:
			resp = NotFound
		}

		if err := resp.Render(w); err != nil {
			err := InternalError(err).Render(w)
			if err != nil {
				logger.Errorf("Failed writing error for error, giving up")
			}
		}

		/*
		 * When we create a new lxc.Container, it adds a finalizer (via
		 * SetFinalizer) that frees the struct. However, it sometimes
		 * takes the go GC a while to actually free the struct,
		 * presumably since it is a small amount of memory.
		 * Unfortunately, the struct also keeps the log fd open, so if
		 * we leave too many of these around, we end up running out of
		 * fds. So, let's explicitly do a GC to collect these at the
		 * end of each request.
		 */
		runtime.GC()
	})
}

// have we setup shared mounts?
var sharedMounted bool
var sharedMountsLock sync.Mutex

func setupSharedMounts() error {
	// Check if we already went through this
	if sharedMounted {
		return nil
	}

	// Get a lock to prevent races
	sharedMountsLock.Lock()
	defer sharedMountsLock.Unlock()

	// Check if already setup
	path := shared.VarPath("shmounts")
	if shared.IsMountPoint(path) {
		sharedMounted = true
		return nil
	}

	// Mount a new tmpfs
	if err := syscall.Mount("tmpfs", path, "tmpfs", 0, "size=100k,mode=0711"); err != nil {
		return err
	}

	// Mark as MS_SHARED and MS_REC
	var flags uintptr = syscall.MS_SHARED | syscall.MS_REC
	if err := syscall.Mount(path, path, "none", flags, ""); err != nil {
		return err
	}

	sharedMounted = true
	return nil
}

func (d *Daemon) Init() error {
	err := d.init()

	// If an error occurred synchronously while starting up, let's try to
	// cleanup any state we produced so far. Errors happening here will be
	// ignored.
	if err != nil {
		d.Stop()
	}

	return err
}

func (d *Daemon) init() error {
	/* Set the LVM environment */
	err := os.Setenv("LVM_SUPPRESS_FD_WARNINGS", "1")
	if err != nil {
		return err
	}

	/* Print welcome message */
	if d.os.MockMode {
		logger.Info(fmt.Sprintf("LXD %s is starting in mock mode", version.Version),
			log.Ctx{"path": shared.VarPath("")})
	} else {
		logger.Info(fmt.Sprintf("LXD %s is starting in normal mode", version.Version),
			log.Ctx{"path": shared.VarPath("")})
	}

	/* Initialize the operating system facade */
	err = d.os.Init()
	if err != nil {
		return err
	}

	/* Initialize the database */
	dump, err := initializeDbObject(d)
	if err != nil {
		return err
	}

	/* Setup server certificate */
	certInfo, err := util.LoadCert(d.os.VarDir)
	if err != nil {
		return err
	}

	/* Setup dqlite */
	d.gateway, err = cluster.NewGateway(d.db, certInfo, d.config.RaftLatency)
	if err != nil {
		return err
	}

	/* Setup some mounts (nice to have) */
	if !d.os.MockMode {
		// Attempt to mount the shmounts tmpfs
		setupSharedMounts()

		// Attempt to Mount the devlxd tmpfs
		devlxd := filepath.Join(d.os.VarDir, "devlxd")
		if !shared.IsMountPoint(devlxd) {
			syscall.Mount("tmpfs", devlxd, "tmpfs", 0, "size=100k,mode=0755")
		}
	}

	address, err := node.HTTPSAddress(d.db)
	if err != nil {
		return errors.Wrap(err, "failed to fetch node address")
	}

	/* Open the cluster database */
	d.cluster, err = db.OpenCluster("db.bin", d.gateway.Dialer(), address)
	if err != nil {
		return errors.Wrap(err, "failed to open cluster database")
	}

	/* Setup the web server */
	config := &endpoints.Config{
		Dir:                  d.os.VarDir,
		Cert:                 certInfo,
		RestServer:           RestServer(d),
		DevLxdServer:         DevLxdServer(d),
		LocalUnixSocketGroup: d.config.Group,
		NetworkAddress:       address,
	}
	d.endpoints, err = endpoints.Up(config)
	if err != nil {
		return err
	}

	/* Migrate the node local data to the cluster database, if needed */
	if dump != nil {
		logger.Infof("Migrating data from lxd.db to db.bin")
		err = d.cluster.ImportPreClusteringData(dump)
		if err != nil {
			return fmt.Errorf("Failed to migrate data to db.bin: %v", err)
		}
	}

	/* Read the storage pools */
	err = SetupStorageDriver(d.State(), false)
	if err != nil {
		return err
	}

	/* Apply all patches */
	err = patchesApplyAll(d)
	if err != nil {
		return err
	}

	/* Setup the networks */
	err = networkStartup(d.State())
	if err != nil {
		return err
	}

	/* Restore simplestreams cache */
	err = imageLoadStreamCache(d)
	if err != nil {
		return err
	}

	/* Log expiry */
	d.tasks.Add(expireLogsTask(d.State()))

	/* set the initial proxy function and external auth based on config values in the DB */
	macaroonEndpoint := ""
	maasAPIURL := ""
	maasAPIKey := ""
	maasMachine := ""
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		config, err := cluster.ConfigLoad(tx)
		if err != nil {
			return err
		}
		d.proxy = shared.ProxyFromConfig(
			config.ProxyHTTPS(), config.ProxyHTTP(), config.ProxyIgnoreHosts(),
		)
		macaroonEndpoint = config.MacaroonEndpoint()
		maasAPIURL, maasAPIKey, maasMachine = config.MAASController()
		return nil
	})
	if err != nil {
		return err
	}
	err = d.setupExternalAuthentication(macaroonEndpoint)
	if err != nil {
		return err
	}

	err = d.setupMAASController(maasAPIURL, maasAPIKey, maasMachine)
	if err != nil {
		return err
	}

	if !d.os.MockMode {
		/* Start the scheduler */
		go deviceEventListener(d.State())
		readSavedClientCAList(d)
	}

	close(d.setupChan)

	// Run the post initialization actions
	err = d.Ready()
	if err != nil {
		return err
	}

	return nil
}

func (d *Daemon) Ready() error {
	/* Prune images */
	d.taskPruneImages = d.tasks.Add(pruneExpiredImagesTask(d))

	/* Auto-update images */
	d.taskAutoUpdate = d.tasks.Add(autoUpdateImagesTask(d))

	/* Auto-update instance types */
	d.tasks.Add(instanceRefreshTypesTask(d))

	// FIXME: There's no hard reason for which we should not run tasks in
	//        mock mode. However it requires that we tweak the tasks so
	//        they exit gracefully without blocking (something we should
	//        do anyways) and they don't hit the internet or similar. Support
	//        for proper cancellation is something that has been started but
	//        has not been fully completed.
	if !d.os.MockMode {
		d.tasks.Start()
	}

	s := d.State()

	/* Restore containers */
	containersRestart(s)

	/* Re-balance in case things changed while LXD was down */
	deviceTaskBalance(s)

	close(d.readyChan)

	return nil
}

func (d *Daemon) numRunningContainers() (int, error) {
	results, err := d.db.ContainersList(db.CTypeRegular)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, r := range results {
		container, err := containerLoadByName(d.State(), r)
		if err != nil {
			continue
		}

		if container.IsRunning() {
			count = count + 1
		}
	}

	return count, nil
}

// Stop stops the shared daemon.
func (d *Daemon) Stop() error {
	errors := []error{}
	trackError := func(err error) {
		if err != nil {
			errors = append(errors, err)
		}
	}

	if d.endpoints != nil {
		trackError(d.endpoints.Down())
	}

	trackError(d.tasks.Stop(time.Second)) // Give tasks at most a second to cleanup.

	shouldUnmount := false
	if d.db != nil {
		if n, err := d.numRunningContainers(); err != nil || n == 0 {
			shouldUnmount = true
		}

		logger.Infof("Closing the database")
		trackError(d.db.Close())
	}
	if d.cluster != nil {
		trackError(d.cluster.Close())
	}
	if d.gateway != nil {
		trackError(d.gateway.Shutdown())
	}
	if d.endpoints != nil {
		trackError(d.endpoints.Down())
	}

	if d.endpoints != nil {
		trackError(d.endpoints.Down())
	}

	if shouldUnmount {
		logger.Infof("Unmounting temporary filesystems")

		syscall.Unmount(shared.VarPath("devlxd"), syscall.MNT_DETACH)
		syscall.Unmount(shared.VarPath("shmounts"), syscall.MNT_DETACH)

		logger.Infof("Done unmounting temporary filesystems")
	} else {
		logger.Debugf(
			"Not unmounting temporary filesystems (containers are still running)")
	}

	logger.Infof("Saving simplestreams cache")
	trackError(imageSaveStreamCache(d.os))
	logger.Infof("Saved simplestreams cache")

	var err error
	if n := len(errors); n > 0 {
		format := "%v"
		if n > 1 {
			format += fmt.Sprintf(" (and %d more errors)", n)
		}
		err = fmt.Errorf(format, errors[0])
	}
	return err
}

// Setup external authentication
func (d *Daemon) setupExternalAuthentication(authEndpoint string) error {
	if authEndpoint == "" {
		d.externalAuth = nil
		return nil
	}

	idmClient, err := idmclient.New(idmclient.NewParams{
		BaseURL: authEndpoint,
	})
	if err != nil {
		return err
	}
	key, err := bakery.GenerateKey()
	if err != nil {
		return err
	}
	pkLocator := httpbakery.NewThirdPartyLocator(nil, nil)
	if strings.HasPrefix(authEndpoint, "http://") {
		pkLocator.AllowInsecure()
	}
	bakery := identchecker.NewBakery(identchecker.BakeryParams{
		Key:            key,
		Location:       authEndpoint,
		Locator:        pkLocator,
		Checker:        httpbakery.NewChecker(),
		IdentityClient: idmClient,
		Authorizer: identchecker.ACLAuthorizer{
			GetACL: func(ctx context.Context, op bakery.Op) ([]string, bool, error) {
				return []string{identchecker.Everyone}, false, nil
			},
		},
	})
	d.externalAuth = &externalAuth{
		endpoint: authEndpoint,
		bakery:   bakery,
	}
	return nil
}

// Setup MAAS
func (d *Daemon) setupMAASController(server string, key string, machine string) error {
	var err error
	d.maas = nil

	// Default the machine name to the hostname
	if machine == "" {
		machine, err = os.Hostname()
		if err != nil {
			return err
		}
	}

	// We need both URL and key, otherwise disable MAAS
	if server == "" || key == "" {
		return nil
	}

	// Get a new controller struct
	controller, err := maas.NewController(server, key, machine)
	if err != nil {
		d.maas = nil
		return err
	}

	d.maas = controller
	return nil
}

// Create a database connection and perform any updates needed.
func initializeDbObject(d *Daemon) (*db.Dump, error) {
	// NOTE: we use the legacyPatches parameter to run a few
	// legacy non-db updates that were in place before the
	// patches mechanism was introduced in lxd/patches.go. The
	// rest of non-db patches will be applied separately via
	// patchesApplyAll. See PR #3322 for more details.
	legacy := map[int]*db.LegacyPatch{}
	for i, patch := range legacyPatches {
		legacy[i] = &db.LegacyPatch{
			Hook: func(node *sql.DB) error {
				// FIXME: Attach the local db to the Daemon, since at
				//        this stage we're not fully initialized, yet
				//        some legacy patches expect to find it here.
				d.db = db.ForLegacyPatches(node)
				return patch(d)
			},
		}
	}
	for _, i := range legacyPatchesNeedingDB {
		legacy[i].NeedsDB = true
	}

	// Hook to run when the local database is created from scratch. It will
	// create the default profile and mark all patches as applied.
	freshHook := func(db *db.Node) error {
		for _, patchName := range patchesGetNames() {
			err := db.PatchesMarkApplied(patchName)
			if err != nil {
				return err
			}
		}
		return nil
	}
	var err error
	var dump *db.Dump
	d.db, dump, err = db.OpenNode(d.os.VarDir, freshHook, legacy)
	if err != nil {
		return nil, fmt.Errorf("Error creating database: %s", err)
	}

	return dump, nil
}
