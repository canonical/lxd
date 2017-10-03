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
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/endpoints"
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
	clientCerts         []x509.Certificate
	os                  *sys.OS
	db                  *sql.DB
	readyChan           chan bool
	pruneChan           chan bool
	shutdownChan        chan bool
	resetAutoUpdateChan chan bool

	Storage storage

	tasks     task.Group
	config    *DaemonConfig
	endpoints *endpoints.Endpoints

	proxy func(req *http.Request) (*url.URL, error)
}

// DaemonConfig holds configuration values for Daemon.
type DaemonConfig struct {
	Group     string // Group name the local unix socket should be chown'ed to
	SetupMode bool   // Legacy option for running the daemon in "setup mode"
}

// NewDaemon returns a new Daemon object with the given configuration.
func NewDaemon(config *DaemonConfig) *Daemon {
	return &Daemon{
		config: config,
		os:     sys.NewOS(),
	}
}

// DefaultDaemon returns a new, un-initialized Daemon object with default values.
func DefaultDaemon() *Daemon {
	config := &DaemonConfig{}
	return NewDaemon(config)
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
	return state.NewState(d.db, d.os)
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

		if util.IsTrustedClient(r, d.clientCerts) {
			logger.Debug(
				"handling",
				log.Ctx{"method": r.Method, "url": r.URL.RequestURI(), "ip": r.RemoteAddr})
		} else if r.Method == "GET" && c.untrustedGet {
			logger.Debug(
				"allowing untrusted GET",
				log.Ctx{"url": r.URL.RequestURI(), "ip": r.RemoteAddr})
		} else if r.Method == "POST" && c.untrustedPost {
			logger.Debug(
				"allowing untrusted POST",
				log.Ctx{"url": r.URL.RequestURI(), "ip": r.RemoteAddr})
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
	/* Initialize some variables */
	d.readyChan = make(chan bool)
	d.shutdownChan = make(chan bool)

	/* Set the LVM environment */
	err := os.Setenv("LVM_SUPPRESS_FD_WARNINGS", "1")
	if err != nil {
		return err
	}

	/* Print welcome message */
	if d.os.MockMode {
		logger.Info(fmt.Sprintf("LXD %s is starting in mock mode", version.Version),
			log.Ctx{"path": shared.VarPath("")})
	} else if d.config.SetupMode {
		logger.Info(fmt.Sprintf("LXD %s is starting in setup mode", version.Version),
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
	err = initializeDbObject(d, shared.VarPath("lxd.db"))
	if err != nil {
		return err
	}

	/* Load all config values from the database */
	err = daemonConfigInit(d.db)
	if err != nil {
		return err
	}

	/* Setup the storage driver */
	err = SetupStorageDriver(d)
	if err != nil {
		return fmt.Errorf("Failed to setup storage: %s", err)
	}

	/* Apply all patches */
	err = patchesApplyAll(d)
	if err != nil {
		return err
	}

	/* Restore simplestreams cache */
	err = imageLoadStreamCache(d) // No-op if no simplestreams.yaml metadata file exists.
	if err != nil {
		return err
	}

	/* Log expiry */
	d.tasks.Add(expireLogsTask(d.db))

	/* set the initial proxy function based on config values in the DB */
	d.proxy = shared.ProxyFromConfig(
		daemonConfig["core.proxy_https"].Get(),
		daemonConfig["core.proxy_http"].Get(),
		daemonConfig["core.proxy_ignore_hosts"].Get(),
	)

	/* Setup some mounts (nice to have) */
	if !d.os.MockMode {
		// Attempt to mount the shmounts tmpfs
		setupSharedMounts()

		// Attempt to Mount the devlxd tmpfs
		if !shared.IsMountPoint(shared.VarPath("devlxd")) {
			syscall.Mount("tmpfs", shared.VarPath("devlxd"), "tmpfs", 0, "size=100k,mode=0755")
		}
	}

	if !d.os.MockMode {
		/* Start the scheduler */
		go deviceEventListener(d.State(), d.Storage)

		readSavedClientCAList(d)
	}

	/* Setup the web server */
	restAPI := mux.NewRouter()
	restAPI.StrictSlash(false)

	restAPI.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		SyncResponse(true, []string{"/1.0"}).Render(w)
	})

	for _, c := range api10 {
		d.createCmd(restAPI, "1.0", c)
	}

	for _, c := range apiInternal {
		d.createCmd(restAPI, "internal", c)
	}

	restAPI.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info("Sending top level 404", log.Ctx{"url": r.URL})
		w.Header().Set("Content-Type", "application/json")
		NotFound.Render(w)
	})

	certInfo, err := shared.KeyPairAndCA(shared.VarPath(), "server", shared.CertServer)
	if err != nil {
		return err
	}

	config := &endpoints.Config{
		Dir:                  shared.VarPath(),
		Cert:                 certInfo,
		RestServer:           &http.Server{Handler: &lxdHttpServer{r: restAPI, d: d}},
		DevLxdServer:         &http.Server{Handler: devLxdAPI(d), ConnState: pidMapper.ConnStateHandler},
		LocalUnixSocketGroup: d.config.Group,
		NetworkAddress:       daemonConfig["core.https_address"].Get(),
	}
	d.endpoints, err = endpoints.Up(config)
	if err != nil {
		return fmt.Errorf("cannot start API endpoints: %v", err)
	}

	// Run the post initialization actions
	if !d.config.SetupMode {
		err := d.Ready()
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *Daemon) Ready() error {
	/* Prune images */
	d.pruneChan = make(chan bool)
	go func() {
		for {
			timer := time.NewTimer(24 * time.Hour)
			select {
			case <-timer.C:
				/* run once per day */
				pruneExpiredImages(d)
			case <-d.pruneChan:
				/* run when image.remote_cache_expiry is changed */
				pruneExpiredImages(d)
				timer.Stop()
			}
		}
	}()

	// Do an initial pruning run before we start updating images
	pruneExpiredImages(d)

	/* Auto-update images */
	d.resetAutoUpdateChan = make(chan bool)
	go func() {
		// Initial image sync
		interval := daemonConfig["images.auto_update_interval"].GetInt64()
		if interval > 0 {
			autoUpdateImages(d)
		}

		// Background image sync
		for {
			interval := daemonConfig["images.auto_update_interval"].GetInt64()
			if interval > 0 {
				timer := time.NewTimer(time.Duration(interval) * time.Hour)

				select {
				case <-timer.C:
					autoUpdateImages(d)
				case <-d.resetAutoUpdateChan:
					timer.Stop()
				}
			} else {
				select {
				case <-d.resetAutoUpdateChan:
					continue
				}
			}
		}
	}()

	/* Auto-update instance types */
	go func() {
		// Background update
		for {
			instanceRefreshTypes(d)
			time.Sleep(24 * time.Hour)
		}
	}()

	// FIXME: There's no hard reason for which we should not run tasks in
	//        mock mode. However it requires that we tweak the tasks so
	//        they exit gracefully without blocking (something we should
	//        do anyways) and they don't hit the internet or similar.
	if !d.os.MockMode {
		d.tasks.Start()
	}

	s := d.State()

	/* Restore containers */
	containersRestart(s, d.Storage)

	/* Re-balance in case things changed while LXD was down */
	deviceTaskBalance(s, d.Storage)

	close(d.readyChan)

	return nil
}

func (d *Daemon) numRunningContainers() (int, error) {
	results, err := db.ContainersList(d.db, db.CTypeRegular)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, r := range results {
		container, err := containerLoadByName(d.State(), d.Storage, r)
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
	// FIXME: we should track also other errors happening during shutdown,
	// not only the endpoints one.
	errEndpointsDown := d.endpoints.Down()

	d.tasks.Stop(5 * time.Second) // Give tasks at most five seconds to cleanup.

	if n, err := d.numRunningContainers(); err != nil || n == 0 {
		logger.Infof("Unmounting temporary filesystems")

		syscall.Unmount(shared.VarPath("devlxd"), syscall.MNT_DETACH)
		syscall.Unmount(shared.VarPath("shmounts"), syscall.MNT_DETACH)

		logger.Infof("Done unmounting temporary filesystems")
	} else {
		logger.Debugf("Not unmounting temporary filesystems (containers are still running)")
	}

	logger.Infof("Closing the database")
	d.db.Close()

	logger.Infof("Saving simplestreams cache")
	imageSaveStreamCache()
	logger.Infof("Saved simplestreams cache")

	return errEndpointsDown
}

type lxdHttpServer struct {
	r *mux.Router
	d *Daemon
}

func (s *lxdHttpServer) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	allowedOrigin := daemonConfig["core.https_allowed_origin"].Get()
	origin := req.Header.Get("Origin")
	if allowedOrigin != "" && origin != "" {
		rw.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
	}

	allowedMethods := daemonConfig["core.https_allowed_methods"].Get()
	if allowedMethods != "" && origin != "" {
		rw.Header().Set("Access-Control-Allow-Methods", allowedMethods)
	}

	allowedHeaders := daemonConfig["core.https_allowed_headers"].Get()
	if allowedHeaders != "" && origin != "" {
		rw.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
	}

	// OPTIONS request don't need any further processing
	if req.Method == "OPTIONS" {
		return
	}

	// Call the original server
	s.r.ServeHTTP(rw, req)
}

// Create a database connection and perform any updates needed.
func initializeDbObject(d *Daemon, path string) error {
	var err error

	// Open the database. If the file doesn't exist it is created.
	d.db, err = db.OpenDb(path)
	if err != nil {
		return err
	}

	// Create the DB if it doesn't exist.
	err = db.CreateDb(d.db, patchesGetNames())
	if err != nil {
		return fmt.Errorf("Error creating database: %s", err)
	}

	// Detect LXD downgrades
	if db.GetSchema(d.db) > db.GetLatestSchema() {
		return fmt.Errorf("The database schema is more recent than LXD's schema.")
	}

	// Apply any database update.
	//
	// NOTE: we use the legacyPatches parameter to run a few
	// legacy non-db updates that were in place before the
	// patches mechanism was introduced in lxd/patches.go. The
	// rest of non-db patches will be applied separately via
	// patchesApplyAll. See PR #3322 for more details.
	legacy := map[int]*db.LegacyPatch{}
	for i, patch := range legacyPatches {
		legacy[i] = &db.LegacyPatch{
			Hook: func() error {
				return patch(d)
			},
		}
	}
	for _, i := range legacyPatchesNeedingDB {
		legacy[i].NeedsDB = true
	}
	err = db.UpdatesApplyAll(d.db, true, legacy)
	if err != nil {
		return err
	}

	return nil
}
