package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
	"github.com/syndtr/gocapability/capability"
	"gopkg.in/tomb.v2"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/sys"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
	"github.com/lxc/lxd/shared/version"

	log "gopkg.in/inconshreveable/log15.v2"
)

// AppArmor
var aaAvailable = false
var aaAdmin = false
var aaConfined = false
var aaStacking = false
var aaStacked = false

// CGroup
var cgBlkioController = false
var cgCpuController = false
var cgCpuacctController = false
var cgCpusetController = false
var cgDevicesController = false
var cgMemoryController = false
var cgNetPrioController = false
var cgPidsController = false
var cgSwapAccounting = false

// UserNS
var runningInUserns = false

type Socket struct {
	Socket      net.Listener
	CloseOnExit bool
}

// A Daemon can respond to requests from a shared client.
type Daemon struct {
	clientCerts         []x509.Certificate
	os                  *sys.OS
	db                  *sql.DB
	group               string
	mux                 *mux.Router
	tomb                tomb.Tomb
	readyChan           chan bool
	pruneChan           chan bool
	shutdownChan        chan bool
	resetAutoUpdateChan chan bool

	TCPSocket  *Socket
	UnixSocket *Socket

	devlxd *net.UnixListener

	SetupMode bool

	tlsConfig *tls.Config

	proxy func(req *http.Request) (*url.URL, error)
}

// NewDaemon returns a new, un-initialized Daemon object.
func NewDaemon() *Daemon {
	return &Daemon{
		os: sys.NewOS(),
	}
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

func readMyCert() (string, string, error) {
	certf := shared.VarPath("server.crt")
	keyf := shared.VarPath("server.key")
	logger.Debug("Looking for existing certificates", log.Ctx{"cert": certf, "key": keyf})
	err := shared.FindOrGenCert(certf, keyf, false)

	return certf, keyf, err
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

func (d *Daemon) createCmd(version string, c Command) {
	var uri string
	if c.name == "" {
		uri = fmt.Sprintf("/%s", version)
	} else {
		uri = fmt.Sprintf("/%s/%s", version, c.name)
	}

	d.mux.HandleFunc(uri, func(w http.ResponseWriter, r *http.Request) {
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

func (d *Daemon) UpdateHTTPsPort(newAddress string) error {
	oldAddress := daemonConfig["core.https_address"].Get()

	if oldAddress == newAddress {
		return nil
	}

	if d.TCPSocket != nil {
		d.TCPSocket.Socket.Close()
	}

	if newAddress != "" {
		_, _, err := net.SplitHostPort(newAddress)
		if err != nil {
			ip := net.ParseIP(newAddress)
			if ip != nil && ip.To4() == nil {
				newAddress = fmt.Sprintf("[%s]:%s", newAddress, shared.DefaultPort)
			} else {
				newAddress = fmt.Sprintf("%s:%s", newAddress, shared.DefaultPort)
			}
		}

		var tcpl net.Listener
		for i := 0; i < 10; i++ {
			tcpl, err = tls.Listen("tcp", newAddress, d.tlsConfig)
			if err == nil {
				break
			}

			time.Sleep(100 * time.Millisecond)
		}

		if err != nil {
			return fmt.Errorf("cannot listen on https socket: %v", err)
		}

		d.tomb.Go(func() error { return http.Serve(tcpl, &lxdHttpServer{d.mux, d}) })
		d.TCPSocket = &Socket{Socket: tcpl, CloseOnExit: true}
	}

	return nil
}

func haveMacAdmin() bool {
	c, err := capability.NewPid(0)
	if err != nil {
		return false
	}
	if c.Get(capability.EFFECTIVE, capability.CAP_MAC_ADMIN) {
		return true
	}
	return false
}

func (d *Daemon) Init() error {
	/* Initialize some variables */
	d.readyChan = make(chan bool)
	d.shutdownChan = make(chan bool)

	/* Set the executable path */
	/* Set the LVM environment */
	err := os.Setenv("LVM_SUPPRESS_FD_WARNINGS", "1")
	if err != nil {
		return err
	}

	/* Setup logging if that wasn't done before */
	if logger.Log == nil {
		logger.Log, err = logging.GetLogger("", "", true, true, nil)
		if err != nil {
			return err
		}
	}

	/* Print welcome message */
	if d.os.MockMode {
		logger.Info(fmt.Sprintf("LXD %s is starting in mock mode", version.Version),
			log.Ctx{"path": shared.VarPath("")})
	} else if d.SetupMode {
		logger.Info(fmt.Sprintf("LXD %s is starting in setup mode", version.Version),
			log.Ctx{"path": shared.VarPath("")})
	} else {
		logger.Info(fmt.Sprintf("LXD %s is starting in normal mode", version.Version),
			log.Ctx{"path": shared.VarPath("")})
	}

	/* Detect user namespaces */
	runningInUserns = shared.RunningInUserNS()

	/* Detect AppArmor availability */
	_, err = exec.LookPath("apparmor_parser")
	if os.Getenv("LXD_SECURITY_APPARMOR") == "false" {
		logger.Warnf("AppArmor support has been manually disabled")
	} else if !shared.IsDir("/sys/kernel/security/apparmor") {
		logger.Warnf("AppArmor support has been disabled because of lack of kernel support")
	} else if err != nil {
		logger.Warnf("AppArmor support has been disabled because 'apparmor_parser' couldn't be found")
	} else {
		aaAvailable = true
	}

	/* Detect AppArmor stacking support */
	aaCanStack := func() bool {
		contentBytes, err := ioutil.ReadFile("/sys/kernel/security/apparmor/features/domain/stack")
		if err != nil {
			return false
		}

		if string(contentBytes) != "yes\n" {
			return false
		}

		contentBytes, err = ioutil.ReadFile("/sys/kernel/security/apparmor/features/domain/version")
		if err != nil {
			return false
		}

		content := string(contentBytes)

		parts := strings.Split(strings.TrimSpace(content), ".")

		if len(parts) == 0 {
			logger.Warn("unknown apparmor domain version", log.Ctx{"version": content})
			return false
		}

		major, err := strconv.Atoi(parts[0])
		if err != nil {
			logger.Warn("unknown apparmor domain version", log.Ctx{"version": content})
			return false
		}

		minor := 0
		if len(parts) == 2 {
			minor, err = strconv.Atoi(parts[1])
			if err != nil {
				logger.Warn("unknown apparmor domain version", log.Ctx{"version": content})
				return false
			}
		}

		return major >= 1 && minor >= 2
	}

	aaStacking = aaCanStack()

	/* Detect existing AppArmor stack */
	if shared.PathExists("/sys/kernel/security/apparmor/.ns_stacked") {
		contentBytes, err := ioutil.ReadFile("/sys/kernel/security/apparmor/.ns_stacked")
		if err == nil && string(contentBytes) == "yes\n" {
			aaStacked = true
		}
	}

	/* Detect AppArmor admin support */
	if !haveMacAdmin() {
		if aaAvailable {
			logger.Warnf("Per-container AppArmor profiles are disabled because the mac_admin capability is missing.")
		}
	} else if runningInUserns && !aaStacked {
		if aaAvailable {
			logger.Warnf("Per-container AppArmor profiles are disabled because LXD is running in an unprivileged container without stacking.")
		}
	} else {
		aaAdmin = true
	}

	/* Detect AppArmor confinment */
	profile := aaProfile()
	if profile != "unconfined" && profile != "" {
		if aaAvailable {
			logger.Warnf("Per-container AppArmor profiles are disabled because LXD is already protected by AppArmor.")
		}
		aaConfined = true
	}

	/* Detect CGroup support */
	cgBlkioController = shared.PathExists("/sys/fs/cgroup/blkio/")
	if !cgBlkioController {
		logger.Warnf("Couldn't find the CGroup blkio controller, I/O limits will be ignored.")
	}

	cgCpuController = shared.PathExists("/sys/fs/cgroup/cpu/")
	if !cgCpuController {
		logger.Warnf("Couldn't find the CGroup CPU controller, CPU time limits will be ignored.")
	}

	cgCpuacctController = shared.PathExists("/sys/fs/cgroup/cpuacct/")
	if !cgCpuacctController {
		logger.Warnf("Couldn't find the CGroup CPUacct controller, CPU accounting will not be available.")
	}

	cgCpusetController = shared.PathExists("/sys/fs/cgroup/cpuset/")
	if !cgCpusetController {
		logger.Warnf("Couldn't find the CGroup CPUset controller, CPU pinning will be ignored.")
	}

	cgDevicesController = shared.PathExists("/sys/fs/cgroup/devices/")
	if !cgDevicesController {
		logger.Warnf("Couldn't find the CGroup devices controller, device access control won't work.")
	}

	cgMemoryController = shared.PathExists("/sys/fs/cgroup/memory/")
	if !cgMemoryController {
		logger.Warnf("Couldn't find the CGroup memory controller, memory limits will be ignored.")
	}

	cgNetPrioController = shared.PathExists("/sys/fs/cgroup/net_prio/")
	if !cgNetPrioController {
		logger.Warnf("Couldn't find the CGroup network class controller, network limits will be ignored.")
	}

	cgPidsController = shared.PathExists("/sys/fs/cgroup/pids/")
	if !cgPidsController {
		logger.Warnf("Couldn't find the CGroup pids controller, process limits will be ignored.")
	}

	cgSwapAccounting = shared.PathExists("/sys/fs/cgroup/memory/memory.memsw.limit_in_bytes")
	if !cgSwapAccounting {
		logger.Warnf("CGroup memory swap accounting is disabled, swap limits will be ignored.")
	}

	/* Make sure all our directories are available */
	if err := os.MkdirAll(shared.VarPath(), 0711); err != nil {
		return err
	}
	if err := os.MkdirAll(shared.CachePath(), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(shared.VarPath("containers"), 0711); err != nil {
		return err
	}
	if err := os.MkdirAll(shared.VarPath("devices"), 0711); err != nil {
		return err
	}
	if err := os.MkdirAll(shared.VarPath("devlxd"), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(shared.VarPath("images"), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(shared.LogPath(), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(shared.VarPath("security"), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(shared.VarPath("shmounts"), 0711); err != nil {
		return err
	}
	if err := os.MkdirAll(shared.VarPath("snapshots"), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(shared.VarPath("networks"), 0711); err != nil {
		return err
	}
	if err := os.MkdirAll(shared.VarPath("disks"), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(shared.VarPath("storage-pools"), 0711); err != nil {
		return err
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

	if !d.os.MockMode {
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
	}

	/* Log expiry */
	go func() {
		t := time.NewTicker(24 * time.Hour)
		for {
			logger.Infof("Expiring log files")

			err := ExpireLogs(d.db)
			if err != nil {
				logger.Error("Failed to expire logs", log.Ctx{"err": err})
			}

			logger.Infof("Done expiring log files")
			<-t.C
		}
	}()

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

	/* Setup /dev/lxd */
	logger.Infof("Starting /dev/lxd handler")
	d.devlxd, err = createAndBindDevLxd()
	if err != nil {
		return err
	}

	d.tomb.Go(func() error {
		server := devLxdServer(d)
		return server.Serve(d.devlxd)
	})

	if !d.os.MockMode {
		/* Start the scheduler */
		go deviceEventListener(d.State())

		/* Setup the TLS authentication */
		certf, keyf, err := readMyCert()
		if err != nil {
			return err
		}

		cert, err := tls.LoadX509KeyPair(certf, keyf)
		if err != nil {
			return err
		}

		tlsConfig := &tls.Config{
			ClientAuth:   tls.RequestClientCert,
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
			MaxVersion:   tls.VersionTLS12,
			CipherSuites: []uint16{
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA},
			PreferServerCipherSuites: true,
		}

		if shared.PathExists(shared.VarPath("server.ca")) {
			ca, err := shared.ReadCert(shared.VarPath("server.ca"))
			if err != nil {
				return err
			}

			caPool := x509.NewCertPool()
			caPool.AddCert(ca)
			tlsConfig.RootCAs = caPool
			tlsConfig.ClientCAs = caPool

			logger.Infof("LXD is in CA mode, only CA-signed certificates will be allowed")
		}

		tlsConfig.BuildNameToCertificate()

		d.tlsConfig = tlsConfig

		readSavedClientCAList(d)
	}

	/* Setup the web server */
	d.mux = mux.NewRouter()
	d.mux.StrictSlash(false)

	d.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		SyncResponse(true, []string{"/1.0"}).Render(w)
	})

	for _, c := range api10 {
		d.createCmd("1.0", c)
	}

	for _, c := range apiInternal {
		d.createCmd("internal", c)
	}

	d.mux.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info("Sending top level 404", log.Ctx{"url": r.URL})
		w.Header().Set("Content-Type", "application/json")
		NotFound.Render(w)
	})

	// Prepare the list of listeners
	listeners := util.GetListeners()
	if len(listeners) > 0 {
		logger.Infof("LXD is socket activated")

		for _, listener := range listeners {
			if shared.PathExists(listener.Addr().String()) {
				d.UnixSocket = &Socket{Socket: listener, CloseOnExit: false}
			} else {
				tlsListener := tls.NewListener(listener, d.tlsConfig)
				d.TCPSocket = &Socket{Socket: tlsListener, CloseOnExit: false}
			}
		}
	} else {
		logger.Infof("LXD isn't socket activated")

		localSocketPath := shared.VarPath("unix.socket")

		// If the socket exists, let's try to connect to it and see if there's
		// a lxd running.
		if shared.PathExists(localSocketPath) {
			_, err := lxd.ConnectLXDUnix("", nil)
			if err != nil {
				logger.Debugf("Detected stale unix socket, deleting")
				// Connecting failed, so let's delete the socket and
				// listen on it ourselves.
				err = os.Remove(localSocketPath)
				if err != nil {
					return err
				}
			} else {
				return fmt.Errorf("LXD is already running.")
			}
		}

		unixAddr, err := net.ResolveUnixAddr("unix", localSocketPath)
		if err != nil {
			return fmt.Errorf("cannot resolve unix socket address: %v", err)
		}

		unixl, err := net.ListenUnix("unix", unixAddr)
		if err != nil {
			return fmt.Errorf("cannot listen on unix socket: %v", err)
		}

		if err := os.Chmod(localSocketPath, 0660); err != nil {
			return err
		}

		var gid int
		if d.group != "" {
			gid, err = shared.GroupId(d.group)
			if err != nil {
				return err
			}
		} else {
			gid = os.Getgid()
		}

		if err := os.Chown(localSocketPath, os.Getuid(), gid); err != nil {
			return err
		}

		d.UnixSocket = &Socket{Socket: unixl, CloseOnExit: true}
	}

	listenAddr := daemonConfig["core.https_address"].Get()
	if listenAddr != "" {
		_, _, err := net.SplitHostPort(listenAddr)
		if err != nil {
			listenAddr = fmt.Sprintf("%s:%s", listenAddr, shared.DefaultPort)
		}

		tcpl, err := tls.Listen("tcp", listenAddr, d.tlsConfig)
		if err != nil {
			logger.Error("cannot listen on https socket, skipping...", log.Ctx{"err": err})
		} else {
			if d.TCPSocket != nil {
				logger.Infof("Replacing inherited TCP socket with configured one")
				d.TCPSocket.Socket.Close()
			}
			d.TCPSocket = &Socket{Socket: tcpl, CloseOnExit: true}
		}
	}

	// Bind the REST API
	logger.Infof("REST API daemon:")
	if d.UnixSocket != nil {
		logger.Info(" - binding Unix socket", log.Ctx{"socket": d.UnixSocket.Socket.Addr()})
		d.tomb.Go(func() error { return http.Serve(d.UnixSocket.Socket, &lxdHttpServer{d.mux, d}) })
	}

	if d.TCPSocket != nil {
		logger.Info(" - binding TCP socket", log.Ctx{"socket": d.TCPSocket.Socket.Addr()})
		d.tomb.Go(func() error { return http.Serve(d.TCPSocket.Socket, &lxdHttpServer{d.mux, d}) })
	}

	// Run the post initialization actions
	if !d.os.MockMode && !d.SetupMode {
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
		pruneExpiredImages(d)
		for {
			timer := time.NewTimer(24 * time.Hour)
			timeChan := timer.C
			select {
			case <-timeChan:
				/* run once per day */
				pruneExpiredImages(d)
			case <-d.pruneChan:
				/* run when image.remote_cache_expiry is changed */
				pruneExpiredImages(d)
				timer.Stop()
			}
		}
	}()

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
				timeChan := timer.C

				select {
				case <-timeChan:
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

	s := d.State()

	/* Restore containers */
	containersRestart(s)

	/* Re-balance in case things changed while LXD was down */
	deviceTaskBalance(s)

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

var errStop = fmt.Errorf("requested stop")

// Stop stops the shared daemon.
func (d *Daemon) Stop() error {
	forceStop := false

	d.tomb.Kill(errStop)
	logger.Infof("Stopping REST API handler:")
	for _, socket := range []*Socket{d.TCPSocket, d.UnixSocket} {
		if socket == nil {
			continue
		}

		if socket.CloseOnExit {
			logger.Info(" - closing socket", log.Ctx{"socket": socket.Socket.Addr()})
			socket.Socket.Close()
		} else {
			logger.Info(" - skipping socket-activated socket", log.Ctx{"socket": socket.Socket.Addr()})
			forceStop = true
		}
	}

	logger.Infof("Stopping /dev/lxd handler")
	d.devlxd.Close()
	logger.Infof("Stopped /dev/lxd handler")

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

	if d.os.MockMode || forceStop {
		return nil
	}

	err := d.tomb.Wait()
	if err == errStop {
		return nil
	}

	return err
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

	allowedCredentials := daemonConfig["core.https_allowed_credentials"].GetBool()
	if allowedCredentials {
		rw.Header().Set("Access-Control-Allow-Credentials", "true")
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
