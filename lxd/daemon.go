package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/pem"
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

	"golang.org/x/crypto/scrypt"

	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
	"github.com/syndtr/gocapability/capability"
	"gopkg.in/tomb.v2"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logging"

	log "gopkg.in/inconshreveable/log15.v2"
)

// AppArmor
var aaAdmin = true
var aaAvailable = true
var aaConfined = false

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
	architectures       []int
	BackingFs           string
	clientCerts         []x509.Certificate
	db                  *sql.DB
	group               string
	IdmapSet            *shared.IdmapSet
	lxcpath             string
	mux                 *mux.Router
	tomb                tomb.Tomb
	readyChan           chan bool
	pruneChan           chan bool
	shutdownChan        chan bool
	resetAutoUpdateChan chan bool

	Storage storage

	TCPSocket  *Socket
	UnixSocket *Socket

	devlxd *net.UnixListener

	MockMode  bool
	SetupMode bool

	imagesDownloading     map[string]chan bool
	imagesDownloadingLock sync.RWMutex

	tlsConfig *tls.Config

	proxy func(req *http.Request) (*url.URL, error)
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

func (d *Daemon) httpGetSync(url string, certificate string) (*lxd.Response, error) {
	var err error

	var cert *x509.Certificate
	if certificate != "" {
		certBlock, _ := pem.Decode([]byte(certificate))
		if certBlock == nil {
			return nil, fmt.Errorf("Invalid certificate")
		}

		cert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return nil, err
		}
	}

	tlsConfig, err := shared.GetTLSConfig("", "", "", cert)
	if err != nil {
		return nil, err
	}

	tr := &http.Transport{
		TLSClientConfig: tlsConfig,
		Dial:            shared.RFC3493Dialer,
		Proxy:           d.proxy,
	}

	myhttp := http.Client{
		Transport: tr,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", shared.UserAgent)

	r, err := myhttp.Do(req)
	if err != nil {
		return nil, err
	}

	resp, err := lxd.ParseResponse(r)
	if err != nil {
		return nil, err
	}

	if resp.Type != lxd.Sync {
		return nil, fmt.Errorf("unexpected non-sync response")
	}

	return resp, nil
}

func (d *Daemon) httpGetFile(url string, certificate string) (*http.Response, error) {
	var err error

	var cert *x509.Certificate
	if certificate != "" {
		certBlock, _ := pem.Decode([]byte(certificate))
		if certBlock == nil {
			return nil, fmt.Errorf("Invalid certificate")
		}

		cert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return nil, err
		}
	}

	tlsConfig, err := shared.GetTLSConfig("", "", "", cert)
	if err != nil {
		return nil, err
	}

	tr := &http.Transport{
		TLSClientConfig: tlsConfig,
		Dial:            shared.RFC3493Dialer,
		Proxy:           d.proxy,
	}
	myhttp := http.Client{
		Transport: tr,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", shared.UserAgent)

	raw, err := myhttp.Do(req)
	if err != nil {
		return nil, err
	}

	if raw.StatusCode != 200 {
		_, err := lxd.HoistResponse(raw, lxd.Error)
		if err != nil {
			return nil, err
		}

		return nil, fmt.Errorf("non-200 status with no error response?")
	}

	return raw, nil
}

func readMyCert() (string, string, error) {
	certf := shared.VarPath("server.crt")
	keyf := shared.VarPath("server.key")
	shared.LogDebug("Looking for existing certificates", log.Ctx{"cert": certf, "key": keyf})
	err := shared.FindOrGenCert(certf, keyf)

	return certf, keyf, err
}

func (d *Daemon) isTrustedClient(r *http.Request) bool {
	if r.RemoteAddr == "@" {
		// Unix socket
		return true
	}

	if r.TLS == nil {
		return false
	}

	for i := range r.TLS.PeerCertificates {
		if d.CheckTrustState(*r.TLS.PeerCertificates[i]) {
			return true
		}
	}

	return false
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

func (d *Daemon) isRecursionRequest(r *http.Request) bool {
	recursionStr := r.FormValue("recursion")

	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		return false
	}

	return recursion == 1
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

		if d.isTrustedClient(r) {
			shared.LogDebug(
				"handling",
				log.Ctx{"method": r.Method, "url": r.URL.RequestURI(), "ip": r.RemoteAddr})
		} else if r.Method == "GET" && c.untrustedGet {
			shared.LogDebug(
				"allowing untrusted GET",
				log.Ctx{"url": r.URL.RequestURI(), "ip": r.RemoteAddr})
		} else if r.Method == "POST" && c.untrustedPost {
			shared.LogDebug(
				"allowing untrusted POST",
				log.Ctx{"url": r.URL.RequestURI(), "ip": r.RemoteAddr})
		} else {
			shared.LogWarn(
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
				shared.LogErrorf("Failed writing error for error, giving up")
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

func (d *Daemon) SetupStorageDriver() error {
	var err error

	lvmVgName := daemonConfig["storage.lvm_vg_name"].Get()
	zfsPoolName := daemonConfig["storage.zfs_pool_name"].Get()

	if lvmVgName != "" {
		d.Storage, err = newStorage(d, storageTypeLvm)
		if err != nil {
			shared.LogDebugf("Could not initialize storage type LVM: %s - falling back to dir", err)
		} else {
			return nil
		}
	} else if zfsPoolName != "" {
		d.Storage, err = newStorage(d, storageTypeZfs)
		if err != nil {
			shared.LogDebugf("Could not initialize storage type ZFS: %s - falling back to dir", err)
		} else {
			return nil
		}
	} else if d.BackingFs == "btrfs" {
		d.Storage, err = newStorage(d, storageTypeBtrfs)
		if err != nil {
			shared.LogDebugf("Could not initialize storage type btrfs: %s - falling back to dir", err)
		} else {
			return nil
		}
	}

	d.Storage, err = newStorage(d, storageTypeDir)

	return err
}

// have we setup shared mounts?
var sharedMounted bool
var sharedMountsLock sync.Mutex

func setupSharedMounts() error {
	if sharedMounted {
		return nil
	}

	sharedMountsLock.Lock()
	defer sharedMountsLock.Unlock()

	if sharedMounted {
		return nil
	}

	path := shared.VarPath("shmounts")

	isShared, err := shared.IsOnSharedMount(path)
	if err != nil {
		return err
	}

	if isShared {
		// / may already be ms-shared, or shmounts may have
		// been mounted by a previous lxd run
		sharedMounted = true
		return nil
	}

	if err := syscall.Mount(path, path, "none", syscall.MS_BIND, ""); err != nil {
		return err
	}

	var flags uintptr = syscall.MS_SHARED | syscall.MS_REC
	if err := syscall.Mount(path, path, "none", flags, ""); err != nil {
		return err
	}

	sharedMounted = true
	return nil
}

func (d *Daemon) ListenAddresses() ([]string, error) {
	addresses := make([]string, 0)

	value := daemonConfig["core.https_address"].Get()
	if value == "" {
		return addresses, nil
	}

	localHost, localPort, err := net.SplitHostPort(value)
	if err != nil {
		localHost = value
		localPort = shared.DefaultPort
	}

	if localHost == "0.0.0.0" || localHost == "::" || localHost == "[::]" {
		ifaces, err := net.Interfaces()
		if err != nil {
			return addresses, err
		}

		for _, i := range ifaces {
			addrs, err := i.Addrs()
			if err != nil {
				continue
			}

			for _, addr := range addrs {
				var ip net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}

				if !ip.IsGlobalUnicast() {
					continue
				}

				if ip.To4() == nil {
					if localHost == "0.0.0.0" {
						continue
					}
					addresses = append(addresses, fmt.Sprintf("[%s]:%s", ip, localPort))
				} else {
					addresses = append(addresses, fmt.Sprintf("%s:%s", ip, localPort))
				}
			}
		}
	} else {
		if strings.Contains(localHost, ":") {
			addresses = append(addresses, fmt.Sprintf("[%s]:%s", localHost, localPort))
		} else {
			addresses = append(addresses, fmt.Sprintf("%s:%s", localHost, localPort))
		}
	}

	return addresses, nil
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

		d.tomb.Go(func() error { return http.Serve(tcpl, d.mux) })
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
	d.imagesDownloading = map[string]chan bool{}

	d.readyChan = make(chan bool)
	d.shutdownChan = make(chan bool)

	/* Set the executable path */
	/* Set the LVM environment */
	err := os.Setenv("LVM_SUPPRESS_FD_WARNINGS", "1")
	if err != nil {
		return err
	}

	/* Setup logging if that wasn't done before */
	if shared.Log == nil {
		shared.Log, err = logging.GetLogger("", "", true, true, nil)
		if err != nil {
			return err
		}
	}

	/* Print welcome message */
	if d.MockMode {
		shared.LogDebug("LXD is starting in mock mode",
			log.Ctx{"path": shared.VarPath("")})
	} else if d.SetupMode {
		shared.LogDebug("LXD is starting in setup mode",
			log.Ctx{"path": shared.VarPath("")})
	} else {
		shared.LogDebug("LXD is starting in normal mode",
			log.Ctx{"path": shared.VarPath("")})
	}

	/* Detect user namespaces */
	runningInUserns = shared.RunningInUserNS()

	/* Detect AppArmor support */
	if aaAvailable && os.Getenv("LXD_SECURITY_APPARMOR") == "false" {
		aaAvailable = false
		aaAdmin = false
		shared.LogWarnf("AppArmor support has been manually disabled")
	}

	if aaAvailable && !shared.IsDir("/sys/kernel/security/apparmor") {
		aaAvailable = false
		aaAdmin = false
		shared.LogWarnf("AppArmor support has been disabled because of lack of kernel support")
	}

	_, err = exec.LookPath("apparmor_parser")
	if aaAvailable && err != nil {
		aaAvailable = false
		aaAdmin = false
		shared.LogWarnf("AppArmor support has been disabled because 'apparmor_parser' couldn't be found")
	}

	/* Detect AppArmor admin support */
	if aaAdmin && !haveMacAdmin() {
		aaAdmin = false
		shared.LogWarnf("Per-container AppArmor profiles are disabled because the mac_admin capability is missing.")
	}

	if aaAdmin && runningInUserns {
		aaAdmin = false
		shared.LogWarnf("Per-container AppArmor profiles are disabled because LXD is running in an unprivileged container.")
	}

	/* Detect AppArmor confinment */
	if !aaConfined {
		profile := aaProfile()
		if profile != "unconfined" && profile != "" {
			aaConfined = true
			shared.LogWarnf("Per-container AppArmor profiles are disabled because LXD is already protected by AppArmor.")
		}
	}

	/* Detect CGroup support */
	cgBlkioController = shared.PathExists("/sys/fs/cgroup/blkio/")
	if !cgBlkioController {
		shared.LogWarnf("Couldn't find the CGroup blkio controller, I/O limits will be ignored.")
	}

	cgCpuController = shared.PathExists("/sys/fs/cgroup/cpu/")
	if !cgCpuController {
		shared.LogWarnf("Couldn't find the CGroup CPU controller, CPU time limits will be ignored.")
	}

	cgCpuacctController = shared.PathExists("/sys/fs/cgroup/cpuacct/")
	if !cgCpuacctController {
		shared.LogWarnf("Couldn't find the CGroup CPUacct controller, CPU accounting will not be available.")
	}

	cgCpusetController = shared.PathExists("/sys/fs/cgroup/cpuset/")
	if !cgCpusetController {
		shared.LogWarnf("Couldn't find the CGroup CPUset controller, CPU pinning will be ignored.")
	}

	cgDevicesController = shared.PathExists("/sys/fs/cgroup/devices/")
	if !cgDevicesController {
		shared.LogWarnf("Couldn't find the CGroup devices controller, device access control won't work.")
	}

	cgMemoryController = shared.PathExists("/sys/fs/cgroup/memory/")
	if !cgMemoryController {
		shared.LogWarnf("Couldn't find the CGroup memory controller, memory limits will be ignored.")
	}

	cgNetPrioController = shared.PathExists("/sys/fs/cgroup/net_prio/")
	if !cgNetPrioController {
		shared.LogWarnf("Couldn't find the CGroup network class controller, network limits will be ignored.")
	}

	cgPidsController = shared.PathExists("/sys/fs/cgroup/pids/")
	if !cgPidsController {
		shared.LogWarnf("Couldn't find the CGroup pids controller, process limits will be ignored.")
	}

	cgSwapAccounting = shared.PathExists("/sys/fs/cgroup/memory/memory.memsw.limit_in_bytes")
	if !cgSwapAccounting {
		shared.LogWarnf("CGroup memory swap accounting is disabled, swap limits will be ignored.")
	}

	/* Get the list of supported architectures */
	var architectures = []int{}

	architectureName, err := shared.ArchitectureGetLocal()
	if err != nil {
		return err
	}

	architecture, err := shared.ArchitectureId(architectureName)
	if err != nil {
		return err
	}
	architectures = append(architectures, architecture)

	personalities, err := shared.ArchitecturePersonalities(architecture)
	if err != nil {
		return err
	}
	for _, personality := range personalities {
		architectures = append(architectures, personality)
	}
	d.architectures = architectures

	/* Set container path */
	d.lxcpath = shared.VarPath("containers")

	/* Make sure all our directories are available */
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

	/* Detect the filesystem */
	d.BackingFs, err = filesystemDetect(d.lxcpath)
	if err != nil {
		shared.LogError("Error detecting backing fs", log.Ctx{"err": err})
	}

	/* Read the uid/gid allocation */
	d.IdmapSet, err = shared.DefaultIdmapSet()
	if err != nil {
		shared.LogWarn("Error reading idmap", log.Ctx{"err": err.Error()})
		shared.LogWarnf("Only privileged containers will be able to run")
	} else {
		shared.LogDebugf("Default uid/gid map:")
		for _, lxcmap := range d.IdmapSet.ToLxcString() {
			shared.LogDebugf(strings.TrimRight(" - "+lxcmap, "\n"))
		}
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

	if !d.MockMode {
		/* Setup the storage driver */
		err = d.SetupStorageDriver()
		if err != nil {
			return fmt.Errorf("Failed to setup storage: %s", err)
		}

		/* Apply all patches */
		err = patchesApplyAll(d)
		if err != nil {
			return err
		}
	}

	/* Log expiry */
	go func() {
		t := time.NewTicker(24 * time.Hour)
		for {
			shared.LogDebugf("Expiring log files")

			err := d.ExpireLogs()
			if err != nil {
				shared.LogError("Failed to expire logs", log.Ctx{"err": err})
			}

			shared.LogDebugf("Done expiring log files")
			<-t.C
		}
	}()

	/* set the initial proxy function based on config values in the DB */
	d.proxy = shared.ProxyFromConfig(
		daemonConfig["core.proxy_https"].Get(),
		daemonConfig["core.proxy_http"].Get(),
		daemonConfig["core.proxy_ignore_hosts"].Get(),
	)

	/* Setup /dev/lxd */
	d.devlxd, err = createAndBindDevLxd()
	if err != nil {
		return err
	}

	if !d.MockMode {
		/* Start the scheduler */
		go deviceEventListener(d)

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
			InsecureSkipVerify: true,
			ClientAuth:         tls.RequestClientCert,
			Certificates:       []tls.Certificate{cert},
			MinVersion:         tls.VersionTLS12,
			MaxVersion:         tls.VersionTLS12,
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

			shared.LogDebugf("LXD is in CA mode, only CA-signed certificates will be allowed")
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
		shared.LogDebug("Sending top level 404", log.Ctx{"url": r.URL})
		w.Header().Set("Content-Type", "application/json")
		NotFound.Render(w)
	})

	listeners := d.GetListeners()
	if len(listeners) > 0 {
		shared.LogDebugf("LXD is socket activated")

		for _, listener := range listeners {
			if shared.PathExists(listener.Addr().String()) {
				d.UnixSocket = &Socket{Socket: listener, CloseOnExit: false}
			} else {
				tlsListener := tls.NewListener(listener, d.tlsConfig)
				d.TCPSocket = &Socket{Socket: tlsListener, CloseOnExit: false}
			}
		}
	} else {
		shared.LogDebugf("LXD isn't socket activated")

		localSocketPath := shared.VarPath("unix.socket")

		// If the socket exists, let's try to connect to it and see if there's
		// a lxd running.
		if shared.PathExists(localSocketPath) {
			_, err := lxd.NewClient(&lxd.DefaultConfig, "local")
			if err != nil {
				shared.LogDebugf("Detected stale unix socket, deleting")
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
			shared.LogError("cannot listen on https socket, skipping...", log.Ctx{"err": err})
		} else {
			if d.TCPSocket != nil {
				shared.LogDebugf("Replacing inherited TCP socket with configured one")
				d.TCPSocket.Socket.Close()
			}
			d.TCPSocket = &Socket{Socket: tcpl, CloseOnExit: true}
		}
	}

	d.tomb.Go(func() error {
		shared.LogDebugf("REST API daemon:")
		if d.UnixSocket != nil {
			shared.LogDebug(" - binding Unix socket", log.Ctx{"socket": d.UnixSocket.Socket.Addr()})
			d.tomb.Go(func() error { return http.Serve(d.UnixSocket.Socket, &lxdHttpServer{d.mux, d}) })
		}

		if d.TCPSocket != nil {
			shared.LogDebug(" - binding TCP socket", log.Ctx{"socket": d.TCPSocket.Socket.Addr()})
			d.tomb.Go(func() error { return http.Serve(d.TCPSocket.Socket, &lxdHttpServer{d.mux, d}) })
		}

		d.tomb.Go(func() error {
			server := devLxdServer(d)
			return server.Serve(d.devlxd)
		})
		return nil
	})

	if !d.MockMode && !d.SetupMode {
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
		autoUpdateImages(d)

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

	/* Restore containers */
	go containersRestart(d)

	/* Re-balance in case things changed while LXD was down */
	deviceTaskBalance(d)

	close(d.readyChan)

	return nil
}

// CheckTrustState returns True if the client is trusted else false.
func (d *Daemon) CheckTrustState(cert x509.Certificate) bool {
	for k, v := range d.clientCerts {
		if bytes.Compare(cert.Raw, v.Raw) == 0 {
			shared.LogDebug("Found cert", log.Ctx{"k": k})
			return true
		}
	}

	return false
}

func (d *Daemon) numRunningContainers() (int, error) {
	results, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, r := range results {
		container, err := containerLoadByName(d, r)
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
	shared.LogDebugf("Stopping REST API handler:")
	for _, socket := range []*Socket{d.TCPSocket, d.UnixSocket} {
		if socket == nil {
			continue
		}

		if socket.CloseOnExit {
			shared.LogDebug(" - closing socket", log.Ctx{"socket": socket.Socket.Addr()})
			socket.Socket.Close()
		} else {
			shared.LogDebug(" - skipping socket-activated socket", log.Ctx{"socket": socket.Socket.Addr()})
			forceStop = true
		}
	}

	if n, err := d.numRunningContainers(); err != nil || n == 0 {
		shared.LogDebugf("Unmounting shmounts")

		syscall.Unmount(shared.VarPath("shmounts"), syscall.MNT_DETACH)
	} else {
		shared.LogDebugf("Not unmounting shmounts (containers are still running)")
	}

	shared.LogDebugf("Closing the database")
	d.db.Close()

	shared.LogDebugf("Stopping /dev/lxd handler")
	d.devlxd.Close()

	if d.MockMode || forceStop {
		return nil
	}

	err := d.tomb.Wait()
	if err == errStop {
		return nil
	}

	return err
}

func (d *Daemon) PasswordCheck(password string) error {
	value := daemonConfig["core.trust_password"].Get()

	// No password set
	if value == "" {
		return fmt.Errorf("No password is set")
	}

	// Compare the password
	buff, err := hex.DecodeString(value)
	if err != nil {
		return err
	}

	salt := buff[0:32]
	hash, err := scrypt.Key([]byte(password), salt, 1<<14, 8, 1, 64)
	if err != nil {
		return err
	}

	if !bytes.Equal(hash, buff[32:]) {
		return fmt.Errorf("Bad password provided")
	}

	return nil
}

func (d *Daemon) ExpireLogs() error {
	entries, err := ioutil.ReadDir(shared.LogPath())
	if err != nil {
		return err
	}

	result, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return err
	}

	newestFile := func(path string, dir os.FileInfo) time.Time {
		newest := dir.ModTime()

		entries, err := ioutil.ReadDir(path)
		if err != nil {
			return newest
		}

		for _, entry := range entries {
			if entry.ModTime().After(newest) {
				newest = entry.ModTime()
			}
		}

		return newest
	}

	for _, entry := range entries {
		// Check if the container still exists
		if shared.StringInSlice(entry.Name(), result) {
			// Remove any log file which wasn't modified in the past 48 hours
			logs, err := ioutil.ReadDir(shared.LogPath(entry.Name()))
			if err != nil {
				return err
			}

			for _, logfile := range logs {
				path := shared.LogPath(entry.Name(), logfile.Name())

				// Always keep the LXC config
				if logfile.Name() == "lxc.conf" {
					continue
				}

				// Deal with directories (snapshots)
				if logfile.IsDir() {
					newest := newestFile(path, logfile)
					if time.Since(newest).Hours() >= 48 {
						os.RemoveAll(path)
						if err != nil {
							return err
						}
					}

					continue
				}

				// Individual files
				if time.Since(logfile.ModTime()).Hours() >= 48 {
					err := os.Remove(path)
					if err != nil {
						return err
					}
				}
			}
		} else {
			// Empty directory if unchanged in the past 24 hours
			path := shared.LogPath(entry.Name())
			newest := newestFile(path, entry)
			if time.Since(newest).Hours() >= 24 {
				err := os.RemoveAll(path)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (d *Daemon) GetListeners() []net.Listener {
	defer func() {
		os.Unsetenv("LISTEN_PID")
		os.Unsetenv("LISTEN_FDS")
	}()

	pid, err := strconv.Atoi(os.Getenv("LISTEN_PID"))
	if err != nil {
		return nil
	}

	if pid != os.Getpid() {
		return nil
	}

	fds, err := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	if err != nil {
		return nil
	}

	listeners := []net.Listener{}

	for i := 3; i < 3+fds; i++ {
		syscall.CloseOnExec(i)

		file := os.NewFile(uintptr(i), fmt.Sprintf("inherited-fd%d", i))
		listener, err := net.FileListener(file)
		if err != nil {
			continue
		}

		listeners = append(listeners, listener)
	}

	return listeners
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
