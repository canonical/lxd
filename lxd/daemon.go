package main

import (
	"bytes"
	"crypto/x509"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/CanonicalLtd/candidclient"
	"github.com/CanonicalLtd/go-dqlite"
	"github.com/gorilla/mux"
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
	"github.com/lxc/lxd/shared/idmap"
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

	// Tasks registry for long-running background tasks
	// Keep clustering tasks separate as they cause a lot of CPU wakeups
	tasks        task.Group
	clusterTasks task.Group

	// Indexes of tasks that need to be reset when their execution interval changes
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
	expiry   int64
	bakery   *identchecker.Bakery
}

// DaemonConfig holds configuration values for Daemon.
type DaemonConfig struct {
	Group              string        // Group name the local unix socket should be chown'ed to
	Trace              []string      // List of sub-systems to trace
	RaftLatency        float64       // Coarse grain measure of the cluster latency
	DqliteSetupTimeout time.Duration // How long to wait for the cluster database to be up
}

// IdentityClientWrapper is a wrapper around an IdentityClient.
type IdentityClientWrapper struct {
	client       identchecker.IdentityClient
	ValidDomains []string
}

func (m *IdentityClientWrapper) IdentityFromContext(ctx context.Context) (identchecker.Identity, []checkers.Caveat, error) {
	return m.client.IdentityFromContext(ctx)
}

func (m *IdentityClientWrapper) DeclaredIdentity(ctx context.Context, declared map[string]string) (identchecker.Identity, error) {
	// Extract the domain from the username
	fields := strings.SplitN(declared["username"], "@", 2)

	// Only validate domain if we have a list of valid domains
	if len(m.ValidDomains) > 0 {
		// If no domain was provided by candid, reject the request
		if len(fields) < 2 {
			logger.Warnf("Failed candid client authentication: no domain provided")
			return nil, fmt.Errorf("Missing domain in candid reply")
		}

		// Check that it was a valid domain
		if !shared.StringInSlice(fields[1], m.ValidDomains) {
			logger.Warnf("Failed candid client authentication: untrusted domain \"%s\"", fields[1])
			return nil, fmt.Errorf("Untrusted candid domain")
		}
	}

	return m.client.DeclaredIdentity(ctx, declared)
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
		RaftLatency:        3.0,
		DqliteSetupTimeout: 36 * time.Hour, // Account for snap refresh lag
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
	// Check the cluster certificate first, so we return an error if the
	// notification header is set but the client is not presenting the
	// cluster certificate (iow this request does not appear to come from a
	// cluster node).
	cert, _ := x509.ParseCertificate(d.endpoints.NetworkCert().KeyPair().Certificate[0])
	clusterCerts := []x509.Certificate{*cert}
	if r.TLS != nil {
		for i := range r.TLS.PeerCertificates {
			if util.CheckTrustState(*r.TLS.PeerCertificates[i], clusterCerts) {
				return nil
			}
		}
	}
	if isClusterNotification(r) {
		return fmt.Errorf("cluster notification not using cluster certificate")
	}

	if r.RemoteAddr == "@" {
		// Unix socket
		return nil
	}

	if r.RemoteAddr == "@devlxd" {
		// Devlxd unix socket
		return fmt.Errorf("devlxd query")
	}

	if r.TLS == nil {
		return fmt.Errorf("no TLS")
	}

	if d.externalAuth != nil && r.Header.Get(httpbakery.BakeryProtocolHeader) != "" {
		ctx := httpbakery.ContextWithRequest(context.TODO(), r)
		authChecker := d.externalAuth.bakery.Checker.Auth(httpbakery.RequestMacaroons(r)...)

		ops := []bakery.Op{{
			Entity: r.URL.Path,
			Action: r.Method,
		}}

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

func writeMacaroonsRequiredResponse(b *identchecker.Bakery, r *http.Request, w http.ResponseWriter, derr *bakery.DischargeRequiredError, expiry int64) {
	ctx := httpbakery.ContextWithRequest(context.TODO(), r)
	caveats := append(derr.Caveats,
		checkers.TimeBeforeCaveat(time.Now().Add(time.Duration(expiry)*time.Second)))

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
	return state.NewState(d.db, d.cluster, d.maas, d.os, d.endpoints)
}

// UnixSocket returns the full path to the unix.socket file that this daemon is
// listening on. Used by tests.
func (d *Daemon) UnixSocket() string {
	path := os.Getenv("LXD_SOCKET")
	if path != "" {
		return path
	}

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
		select {
		case <-d.setupChan:
		default:
			response := Unavailable(fmt.Errorf("LXD daemon setup in progress"))
			response.Render(w)
			return
		}

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
			writeMacaroonsRequiredResponse(d.externalAuth.bakery, r, w, derr, d.externalAuth.expiry)
			return
		} else {
			logger.Warn(
				"rejecting request from untrusted client",
				log.Ctx{"ip": r.RemoteAddr})
			Forbidden(nil).Render(w)
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
		resp = NotImplemented(nil)

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
			resp = NotFound(fmt.Errorf("Method '%s' not found", r.Method))
		}

		if err := resp.Render(w); err != nil {
			err := InternalError(err).Render(w)
			if err != nil {
				logger.Errorf("Failed writing error for error, giving up")
			}
		}
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
		logger.Errorf("Failed to start the daemon: %v", err)
		d.Stop()
	}

	return err
}

func (d *Daemon) init() error {
	// Lets check if there's an existing LXD running
	err := endpoints.CheckAlreadyRunning(d.UnixSocket())
	if err != nil {
		return err
	}

	/* Set the LVM environment */
	err = os.Setenv("LVM_SUPPRESS_FD_WARNINGS", "1")
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

	/* List of sub-systems to trace */
	trace := d.config.Trace

	/* Initialize the operating system facade */
	err = d.os.Init()
	if err != nil {
		return err
	}

	// Look for kernel features
	logger.Infof("Kernel features:")
	d.os.NetnsGetifaddrs = CanUseNetnsGetifaddrs()
	if d.os.NetnsGetifaddrs {
		logger.Infof(" - netnsid-based network retrieval: yes")
	} else {
		logger.Infof(" - netnsid-based network retrieval: no")
	}

	d.os.UeventInjection = CanUseUeventInjection()
	if d.os.UeventInjection {
		logger.Infof(" - uevent injection: yes")
	} else {
		logger.Infof(" - uevent injection: no")
	}

	/*
	 * During daemon startup we're the only thread that touches VFS3Fscaps
	 * so we don't need to bother with atomic.StoreInt32() when touching
	 * VFS3Fscaps.
	 */
	d.os.VFS3Fscaps = idmap.SupportsVFS3Fscaps("")
	if d.os.VFS3Fscaps {
		idmap.VFS3Fscaps = idmap.VFS3FscapsSupported
		logger.Infof(" - unprivileged file capabilities: yes")
	} else {
		idmap.VFS3Fscaps = idmap.VFS3FscapsUnsupported
		logger.Infof(" - unprivileged file capabilities: no")
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

	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return err
	}

	// If not already applied, run the daemon patch that shrinks the boltdb
	// file. We can't run this daemon patch later on along with the other
	// ones because it needs to run before we open the cluster database.
	appliedPatches, err := d.db.Patches()
	if err != nil {
		return errors.Wrap(err, "Fetch applied daemon patches")
	}
	if !shared.StringInSlice("shrink_logs_db_file", appliedPatches) {
		if !clustered {
			// We actually run the patch only if this lxd daemon is
			// not clustered.
			err := patchShrinkLogsDBFile("", d)
			if err != nil {
				return errors.Wrap(err, "Shrink logs.db file")
			}
		}

		err = d.db.PatchesMarkApplied("shrink_logs_db_file")
		if err != nil {
			return err
		}
	}

	/* Setup dqlite */
	clusterLogLevel := "ERROR"
	if shared.StringInSlice("dqlite", trace) {
		clusterLogLevel = "TRACE"
	}
	d.gateway, err = cluster.NewGateway(
		d.db,
		certInfo,
		cluster.Latency(d.config.RaftLatency),
		cluster.LogLevel(clusterLogLevel))
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
		return errors.Wrap(err, "Failed to fetch node address")
	}

	clusterAddress, err := node.ClusterAddress(d.db)
	if err != nil {
		return errors.Wrap(err, "Failed to fetch cluster address")
	}

	debugAddress, err := node.DebugAddress(d.db)
	if err != nil {
		return errors.Wrap(err, "Failed to fetch debug address")
	}

	/* Setup the web server */
	config := &endpoints.Config{
		Dir:                  d.os.VarDir,
		UnixSocket:           d.UnixSocket(),
		Cert:                 certInfo,
		RestServer:           RestServer(d),
		DevLxdServer:         DevLxdServer(d),
		LocalUnixSocketGroup: d.config.Group,
		NetworkAddress:       address,
		ClusterAddress:       clusterAddress,
		DebugAddress:         debugAddress,
	}
	d.endpoints, err = endpoints.Up(config)
	if err != nil {
		return err
	}

	/* Open the cluster database */
	for {
		logger.Info("Initializing global database")
		dir := filepath.Join(d.os.VarDir, "database")
		store := d.gateway.ServerStore()

		contextTimeout := 5 * time.Second
		if !clustered {
			// FIXME: this is a workaround for #5234. We set a very
			// high timeout when we're not clustered, since there's
			// actually no networking involved.
			contextTimeout = time.Minute
		}

		d.cluster, err = db.OpenCluster(
			"db.bin", store, address, dir,
			d.config.DqliteSetupTimeout,
			dqlite.WithDialFunc(d.gateway.DialFunc()),
			dqlite.WithContext(d.gateway.Context()),
			dqlite.WithConnectionTimeout(10*time.Second),
			dqlite.WithContextTimeout(contextTimeout),
			dqlite.WithLogFunc(cluster.DqliteLog),
		)
		if err == nil {
			break
		}
		// If some other nodes have schema or API versions less recent
		// than this node, we block until we receive a notification
		// from the last node being upgraded that everything should be
		// now fine, and then retry
		if err == db.ErrSomeNodesAreBehind {
			logger.Info("Wait for other cluster nodes to upgrade their versions")

			// The only thing we want to still do on this node is
			// to run the heartbeat task, in case we are the raft
			// leader.
			stop, _ := task.Start(cluster.Heartbeat(d.gateway, d.cluster))
			d.gateway.WaitUpgradeNotification()
			stop(time.Second)

			d.cluster.Close()

			continue
		}
		return errors.Wrap(err, "failed to open cluster database")
	}
	err = cluster.NotifyUpgradeCompleted(d.State(), certInfo)
	if err != nil {
		// Ignore the error, since it's not fatal for this particular
		// node. In most cases it just means that some nodes are
		// offline.
		logger.Debugf("Could not notify all nodes of database upgrade: %v", err)
	}

	/* Migrate the node local data to the cluster database, if needed */
	if dump != nil {
		logger.Infof("Migrating data from local to global database")
		err = d.cluster.ImportPreClusteringData(dump)
		if err != nil {
			// Restore the local sqlite3 backup and wipe the raft
			// directory, so users can fix problems and retry.
			path := d.os.LocalDatabasePath()
			copyErr := shared.FileCopy(path+".bak", path)
			if copyErr != nil {
				// Ignore errors here, there's not much we can do
				logger.Errorf("Failed to restore local database: %v", copyErr)
			}
			rmErr := os.RemoveAll(d.os.GlobalDatabaseDir())
			if rmErr != nil {
				// Ignore errors here, there's not much we can do
				logger.Errorf("Failed to cleanup global database: %v", rmErr)
			}

			return fmt.Errorf("Failed to migrate data to global database: %v", err)
		}
	}

	// Setup the user-agent
	if clustered {
		version.UserAgentFeatures([]string{"cluster"})
	}

	/* Read the storage pools */
	logger.Infof("Initializing storage pools")
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
	logger.Infof("Initializing networks")
	err = networkStartup(d.State())
	if err != nil {
		return err
	}

	/* Restore simplestreams cache */
	err = imageLoadStreamCache(d)
	if err != nil {
		return err
	}

	// Cleanup leftover images
	pruneLeftoverImages(d)

	/* Setup the proxy handler, external authentication and MAAS */
	var candidExpiry int64
	candidDomains := ""
	candidEndpoint := ""
	candidEndpointKey := ""
	maasAPIURL := ""
	maasAPIKey := ""
	maasMachine := ""

	err = d.db.Transaction(func(tx *db.NodeTx) error {
		config, err := node.ConfigLoad(tx)
		if err != nil {
			return err
		}

		maasMachine = config.MAASMachine()
		return nil
	})
	if err != nil {
		return err
	}

	logger.Infof("Loading daemon configuration")
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		config, err := cluster.ConfigLoad(tx)
		if err != nil {
			return err
		}

		d.proxy = shared.ProxyFromConfig(
			config.ProxyHTTPS(), config.ProxyHTTP(), config.ProxyIgnoreHosts(),
		)
		candidEndpoint = config.CandidEndpoint()
		candidEndpointKey = config.CandidEndpointKey()
		candidExpiry = config.CandidExpiry()
		candidDomains = config.CandidDomains()
		maasAPIURL, maasAPIKey = config.MAASController()
		return nil
	})
	if err != nil {
		return err
	}

	err = d.setupExternalAuthentication(candidEndpoint, candidEndpointKey, candidExpiry, candidDomains)
	if err != nil {
		return err
	}

	if !d.os.MockMode {
		// Start the scheduler
		go deviceEventListener(d.State())

		// Setup inotify watches
		_, err := deviceInotifyInit(d.State())
		if err != nil {
			return err
		}

		deviceInotifyDirRescan(d.State())
		go deviceInotifyHandler(d.State())

		// Read the trusted certificates
		readSavedClientCAList(d)

		// Connect to MAAS
		if maasAPIURL != "" {
			go func() {
				for {
					err = d.setupMAASController(maasAPIURL, maasAPIKey, maasMachine)
					if err == nil {
						logger.Info("Connected to MAAS controller", log.Ctx{"url": maasAPIURL})
						break
					}

					logger.Warn("Unable to connect to MAAS, trying again in a minute", log.Ctx{"url": maasAPIURL, "err": err})
					time.Sleep(time.Minute)
				}
			}()
		}
	}

	close(d.setupChan)

	// Run the post initialization actions
	err = d.Ready()
	if err != nil {
		return err
	}

	return nil
}

func (d *Daemon) startClusterTasks() {
	// Heartbeats
	d.clusterTasks.Add(cluster.Heartbeat(d.gateway, d.cluster))

	// Events
	d.clusterTasks.Add(cluster.Events(d.endpoints, d.cluster, eventForward))

	// Cluster update trigger
	d.clusterTasks.Add(cluster.KeepUpdated(d.State()))

	// Start all background tasks
	d.clusterTasks.Start()
}

func (d *Daemon) stopClusterTasks() {
	d.clusterTasks.Stop(3 * time.Second)
	d.clusterTasks = task.Group{}
}

func (d *Daemon) Ready() error {
	// Check if clustered
	clustered, err := cluster.Enabled(d.db)
	if err != nil {
		return err
	}

	if clustered {
		d.startClusterTasks()
	}

	// FIXME: There's no hard reason for which we should not run these
	//        tasks in mock mode. However it requires that we tweak them so
	//        they exit gracefully without blocking (something we should do
	//        anyways) and they don't hit the internet or similar. Support
	//        for proper cancellation is something that has been started
	//        but has not been fully completed.
	if !d.os.MockMode {
		// Log expiry (daily)
		d.tasks.Add(expireLogsTask(d.State()))

		// Remove expired images (daily)
		d.taskPruneImages = d.tasks.Add(pruneExpiredImagesTask(d))

		// Auto-update images (every 6 hours, configurable)
		d.taskAutoUpdate = d.tasks.Add(autoUpdateImagesTask(d))

		// Auto-update instance types (daily)
		d.tasks.Add(instanceRefreshTypesTask(d))

		// Remove expired container backups (hourly)
		d.tasks.Add(pruneExpiredContainerBackupsTask(d))

		// Take snapshot of containers (hourly check of configurable cron expression)
		d.tasks.Add(autoCreateContainerSnapshotsTask(d))
	}

	// Start all background tasks
	d.tasks.Start()

	// Get daemon state struct
	s := d.State()

	// Restore containers
	containersRestart(s)

	// Re-balance in case things changed while LXD was down
	deviceTaskBalance(s)

	// Unblock incoming requests
	close(d.readyChan)

	return nil
}

func (d *Daemon) numRunningContainers() (int, error) {
	results, err := containerLoadNodeAll(d.State())
	if err != nil {
		return 0, err
	}

	count := 0
	for _, container := range results {
		if container.IsRunning() {
			count = count + 1
		}
	}

	return count, nil
}

// Kill signals the daemon that we want to shutdown, and that any work
// initiated from this point (e.g. database queries over gRPC) should not be
// retried in case of failure.
func (d *Daemon) Kill() {
	if d.gateway != nil {
		d.gateway.Kill()
	}
}

// Stop stops the shared daemon.
func (d *Daemon) Stop() error {
	logger.Info("Starting shutdown sequence")
	errs := []error{}
	trackError := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}

	if d.endpoints != nil {
		trackError(d.endpoints.Down())
	}

	trackError(d.tasks.Stop(3 * time.Second))        // Give tasks a bit of time to cleanup.
	trackError(d.clusterTasks.Stop(3 * time.Second)) // Give tasks a bit of time to cleanup.

	shouldUnmount := false
	if d.cluster != nil {
		// It might be that database nodes are all down, in that case
		// we don't want to wait too much.
		//
		// FIXME: it should be possible to provide a context or a
		//        timeout for database queries.
		ch := make(chan bool)
		go func() {
			n, err := d.numRunningContainers()
			ch <- err != nil || n == 0
		}()
		select {
		case shouldUnmount = <-ch:
		case <-time.After(2 * time.Second):
			shouldUnmount = true
		}

		logger.Infof("Closing the database")
		err := d.cluster.Close()
		// If we got io.EOF the network connection was interrupted and
		// it's likely that the other node shutdown. Let's just log the
		// event and return cleanly.
		if errors.Cause(err) == driver.ErrBadConn {
			logger.Debugf("Could not close remote database cleanly: %v", err)
		} else {
			trackError(err)
		}
	}
	if d.db != nil {
		trackError(d.db.Close())
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
	if n := len(errs); n > 0 {
		format := "%v"
		if n > 1 {
			format += fmt.Sprintf(" (and %d more errors)", n)
		}
		err = fmt.Errorf(format, errs[0])
	}
	if err != nil {
		logger.Errorf("Failed to cleanly shutdown daemon: %v", err)
	}
	return err
}

// Setup external authentication
func (d *Daemon) setupExternalAuthentication(authEndpoint string, authPubkey string, expiry int64, domains string) error {
	// Parse the list of domains
	authDomains := []string{}
	for _, domain := range strings.Split(domains, ",") {
		if domain == "" {
			continue
		}

		authDomains = append(authDomains, strings.TrimSpace(domain))
	}

	// Allow disable external authentication
	if authEndpoint == "" {
		d.externalAuth = nil
		return nil
	}

	// Setup the candid client
	idmClient, err := candidclient.New(candidclient.NewParams{
		BaseURL: authEndpoint,
	})
	if err != nil {
		return err
	}

	idmClientWrapper := &IdentityClientWrapper{
		client:       idmClient,
		ValidDomains: authDomains,
	}

	// Generate an internal private key
	key, err := bakery.GenerateKey()
	if err != nil {
		return err
	}

	pkCache := bakery.NewThirdPartyStore()
	pkLocator := httpbakery.NewThirdPartyLocator(nil, pkCache)
	if authPubkey != "" {
		// Parse the public key
		pkKey := bakery.Key{}
		err := pkKey.UnmarshalText([]byte(authPubkey))
		if err != nil {
			return err
		}

		// Add the key information
		pkCache.AddInfo(authEndpoint, bakery.ThirdPartyInfo{
			PublicKey: bakery.PublicKey{Key: pkKey},
			Version:   3,
		})

		// Allow http URLs if we have a public key set
		if strings.HasPrefix(authEndpoint, "http://") {
			pkLocator.AllowInsecure()
		}
	}

	// Setup the bakery
	bakery := identchecker.NewBakery(identchecker.BakeryParams{
		Key:            key,
		Location:       authEndpoint,
		Locator:        pkLocator,
		Checker:        httpbakery.NewChecker(),
		IdentityClient: idmClientWrapper,
		Authorizer: identchecker.ACLAuthorizer{
			GetACL: func(ctx context.Context, op bakery.Op) ([]string, bool, error) {
				return []string{identchecker.Everyone}, false, nil
			},
		},
	})

	// Store our settings
	d.externalAuth = &externalAuth{
		endpoint: authEndpoint,
		expiry:   expiry,
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
	logger.Info("Initializing local database")
	// Rename the old database name if needed.
	if shared.PathExists(d.os.LegacyLocalDatabasePath()) {
		if shared.PathExists(d.os.LocalDatabasePath()) {
			return nil, fmt.Errorf("Both legacy and new local database files exists")
		}
		logger.Info("Renaming local database file from lxd.db to database/local.db")
		err := os.Rename(d.os.LegacyLocalDatabasePath(), d.os.LocalDatabasePath())
		if err != nil {
			return nil, errors.Wrap(err, "Failed to rename legacy local database file")
		}
	}

	// NOTE: we use the legacyPatches parameter to run a few
	// legacy non-db updates that were in place before the
	// patches mechanism was introduced in lxd/patches.go. The
	// rest of non-db patches will be applied separately via
	// patchesApplyAll. See PR #3322 for more details.
	legacy := map[int]*db.LegacyPatch{}
	for i, patch := range legacyPatches {
		legacy[i] = &db.LegacyPatch{
			Hook: func(node *sql.DB) error {
				// FIXME: Use the low-level *node* SQL db as backend for both the
				//        db.Node and db.Cluster objects, since at this point we
				//        haven't migrated the data to the cluster database yet.
				cluster := d.cluster
				defer func() {
					d.cluster = cluster
				}()
				d.db = db.ForLegacyPatches(node)
				d.cluster = db.ForLocalInspection(node)
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
	d.db, dump, err = db.OpenNode(filepath.Join(d.os.VarDir, "database"), freshHook, legacy)
	if err != nil {
		return nil, fmt.Errorf("Error creating database: %s", err)
	}

	return dump, nil
}
