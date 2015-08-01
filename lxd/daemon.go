package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/crypto/scrypt"

	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stgraber/lxd-go-systemd/activation"
	"gopkg.in/lxc/go-lxc.v2"
	"gopkg.in/tomb.v2"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

const (
	pwSaltBytes = 32
	pwHashBytes = 64
)

// A Daemon can respond to requests from a shared client.
type Daemon struct {
	architectures []int
	BackingFs     string
	certf         string
	clientCerts   []x509.Certificate
	db            *sql.DB
	IdmapSet      *shared.IdmapSet
	keyf          string
	lxcpath       string
	mux           *mux.Router
	tomb          tomb.Tomb

	Storage storage

	Sockets []net.Listener

	tlsconfig *tls.Config

	devlxd *net.UnixListener

	configValues map[string]string
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

func (d *Daemon) httpGetSync(url string) (*lxd.Response, error) {
	var err error
	if d.tlsconfig == nil {
		d.tlsconfig, err = shared.GetTLSConfig(d.certf, d.keyf)
		if err != nil {
			return nil, err
		}
	}
	tr := &http.Transport{
		TLSClientConfig: d.tlsconfig,
		Dial:            shared.RFC3493Dialer,
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

func (d *Daemon) httpGetFile(url string) (*http.Response, error) {
	var err error
	if d.tlsconfig == nil {
		d.tlsconfig, err = shared.GetTLSConfig(d.certf, d.keyf)
		if err != nil {
			return nil, err
		}
	}
	tr := &http.Transport{
		TLSClientConfig: d.tlsconfig,
		Dial:            shared.RFC3493Dialer,
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
	shared.Log.Info("looking for existing certificates:", log.Ctx{"cert": certf, "key": keyf})

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
			shared.Log.Info(
				"handling",
				log.Ctx{"method": r.Method, "url": r.URL.RequestURI(), "ip": r.RemoteAddr})
		} else if r.Method == "GET" && c.untrustedGet {
			shared.Log.Info(
				"allowing untrusted GET",
				log.Ctx{"url": r.URL.RequestURI(), "ip": r.RemoteAddr})
		} else if r.Method == "POST" && c.untrustedPost {
			shared.Log.Info(
				"allowing untrusted POST",
				log.Ctx{"url": r.URL.RequestURI(), "ip": r.RemoteAddr})
		} else {
			shared.Log.Warn(
				"rejecting request from untrusted client",
				log.Ctx{"ip": r.RemoteAddr})
			Forbidden.Render(w)
			return
		}

		if *debug && r.Method != "GET" && isJSONRequest(r) {
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
				shared.Log.Error("failed writing error for error, giving up.")
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
	vgName, err := d.ConfigValueGet("core.lvm_vg_name")
	if err != nil {
		return fmt.Errorf("Couldn't read config: %s", err)
	}
	if vgName != "" {
		d.Storage, err = newStorage(d, storageTypeLvm)
	} else if d.BackingFs == "btrfs" {
		d.Storage, err = newStorage(d, storageTypeBtrfs)
	} else {
		d.Storage, err = newStorage(d, -1)
	}
	return err
}

func setupSharedMounts() error {
	path := shared.VarPath("shmounts")
	if shared.IsOnSharedMount(path) {
		// / may already be ms-shared, or shmounts may have
		// been mounted by a previous lxd run
		return nil
	}
	if !shared.PathExists(path) {
		if err := os.Mkdir(path, 0755); err != nil {
			return err
		}
	}
	if err := syscall.Mount(path, path, "none", syscall.MS_BIND, ""); err != nil {
		return err
	}
	var flags uintptr = syscall.MS_SHARED | syscall.MS_REC
	if err := syscall.Mount(path, path, "none", flags, ""); err != nil {
		return err
	}
	return nil
}

func (d *Daemon) UpdateHTTPsPort(oldAddress string, newAddress string) error {
	var sockets []net.Listener

	if oldAddress != "" {
		_, _, err := net.SplitHostPort(oldAddress)
		if err != nil {
			oldAddress = fmt.Sprintf("%s:%s", oldAddress, shared.DefaultPort)
		}

		for _, socket := range d.Sockets {
			if socket.Addr().String() == oldAddress {
				socket.Close()
			} else {
				sockets = append(sockets, socket)
			}
		}
	} else {
		sockets = d.Sockets
	}

	if newAddress != "" {
		_, _, err := net.SplitHostPort(newAddress)
		if err != nil {
			newAddress = fmt.Sprintf("%s:%s", newAddress, shared.DefaultPort)
		}

		tlsConfig, err := shared.GetTLSConfig(d.certf, d.keyf)
		if err != nil {
			return err
		}

		tcpl, err := tls.Listen("tcp", newAddress, tlsConfig)
		if err != nil {
			return fmt.Errorf("cannot listen on https socket: %v", err)
		}

		d.tomb.Go(func() error { return http.Serve(tcpl, d.mux) })
		sockets = append(sockets, tcpl)
	}

	d.Sockets = sockets
	return nil
}

// StartDaemon starts the shared daemon with the provided configuration.
func StartDaemon() (*Daemon, error) {
	d := &Daemon{}

	/* Setup logging */
	if shared.Log == nil {
		shared.SetLogger("", "", true, true)
	}

	shared.Log.Info("LXD is starting.")

	/* Get the list of supported architectures */
	var architectures = []int{}

	uname := syscall.Utsname{}
	if err := syscall.Uname(&uname); err != nil {
		return nil, err
	}

	architectureName := ""
	for _, c := range uname.Machine {
		if c == 0 {
			break
		}
		architectureName += string(byte(c))
	}

	architecture, err := shared.ArchitectureId(architectureName)
	if err != nil {
		return nil, err
	}
	architectures = append(architectures, architecture)

	personalities, err := shared.ArchitecturePersonalities(architecture)
	if err != nil {
		return nil, err
	}
	for _, personality := range personalities {
		architectures = append(architectures, personality)
	}
	d.architectures = architectures

	/* Create required paths */
	d.lxcpath = shared.VarPath("containers")
	err = os.MkdirAll(d.lxcpath, 0755)
	if err != nil {
		return nil, err
	}

	// Create default directories
	if err := os.MkdirAll(shared.VarPath("images"), 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(shared.VarPath("snapshots"), 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(shared.VarPath("devlxd"), 0755); err != nil {
		return nil, err
	}

	/* Detect the filesystem */
	d.BackingFs, err = filesystemDetect(d.lxcpath)
	if err != nil {
		shared.Log.Error("Error detecting backing fs", log.Ctx{"err": err})
	}

	/* Read the uid/gid allocation */
	d.IdmapSet, err = shared.DefaultIdmapSet()
	if err != nil {
		shared.Log.Warn("error reading idmap", log.Ctx{"err": err.Error()})
		shared.Log.Warn("operations requiring idmap will not be available")
	} else {
		shared.Log.Info("Default uid/gid map:")
		for _, lxcmap := range d.IdmapSet.ToLxcString() {
			shared.Log.Info(strings.TrimRight(" - "+lxcmap, "\n"))
		}
	}

	/* Initialize the database */
	err = initDb(d)
	if err != nil {
		return nil, err
	}

	/* Setup the TLS authentication */
	certf, keyf, err := readMyCert()
	if err != nil {
		return nil, err
	}
	d.certf = certf
	d.keyf = keyf
	readSavedClientCAList(d)

	tlsConfig, err := shared.GetTLSConfig(d.certf, d.keyf)
	if err != nil {
		return nil, err
	}

	/* Setup /dev/lxd */
	d.devlxd, err = createAndBindDevLxd()
	if err != nil {
		return nil, err
	}

	if err := setupSharedMounts(); err != nil {
		return nil, err
	}

	/* Restart containers */
	containersRestart(d)
	containersWatch(d)

	err = d.SetupStorageDriver()
	if err != nil {
		return nil, fmt.Errorf("Failed to setup storage: %s", err)
	}

	/* Setup the web server */
	d.mux = mux.NewRouter()

	d.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		SyncResponse(true, []string{"/1.0"}).Render(w)
	})

	for _, c := range api10 {
		d.createCmd("1.0", c)
	}

	d.mux.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		shared.Log.Debug("sending top level 404", log.Ctx{"url": r.URL})
		w.Header().Set("Content-Type", "application/json")
		NotFound.Render(w)
	})

	listeners, err := activation.Listeners(false)
	if err != nil {
		return nil, err
	}

	var sockets []net.Listener

	if len(listeners) > 0 {
		shared.Log.Info("LXD is socket activated.")

		for _, listener := range listeners {
			if shared.PathExists(listener.Addr().String()) {
				sockets = append(sockets, listener)
			} else {
				tlsListener := tls.NewListener(listener, tlsConfig)
				sockets = append(sockets, tlsListener)
			}
		}
	} else {
		shared.Log.Info("LXD isn't socket activated.")

		localSocketPath := shared.VarPath("unix.socket")

		// If the socket exists, let's try to connect to it and see if there's
		// a lxd running.
		if shared.PathExists(localSocketPath) {
			c := &lxd.Config{Remotes: map[string]lxd.RemoteConfig{}}
			_, err := lxd.NewClient(c, "")
			if err != nil {
				shared.Log.Debug("Detected old but dead unix socket, deleting it...")
				// Connecting failed, so let's delete the socket and
				// listen on it ourselves.
				err = os.Remove(localSocketPath)
				if err != nil {
					return nil, err
				}
			}
		}

		unixAddr, err := net.ResolveUnixAddr("unix", localSocketPath)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve unix socket address: %v", err)
		}

		unixl, err := net.ListenUnix("unix", unixAddr)
		if err != nil {
			return nil, fmt.Errorf("cannot listen on unix socket: %v", err)
		}

		if err := os.Chmod(localSocketPath, 0660); err != nil {
			return nil, err
		}

		gid, err := shared.GroupId(*group)
		if err != nil {
			return nil, err
		}

		if err := os.Chown(localSocketPath, os.Getuid(), gid); err != nil {
			return nil, err
		}

		sockets = append(sockets, unixl)
	}

	listenAddr, err := d.ConfigValueGet("core.https_address")
	if err != nil {
		return nil, err
	}

	if listenAddr != "" {
		tcpl, err := tls.Listen("tcp", listenAddr, tlsConfig)
		if err != nil {
			return nil, fmt.Errorf("cannot listen on https socket: %v", err)
		}

		sockets = append(sockets, tcpl)
	}

	d.Sockets = sockets

	d.tomb.Go(func() error {
		for _, socket := range d.Sockets {
			shared.Log.Info(" - binding socket", log.Ctx{"socket": socket.Addr()})
			current_socket := socket
			d.tomb.Go(func() error { return http.Serve(current_socket, d.mux) })
		}

		d.tomb.Go(func() error {
			server := devLxdServer(d)
			return server.Serve(d.devlxd)
		})
		return nil
	})

	return d, nil
}

// CheckTrustState returns True if the client is trusted else false.
func (d *Daemon) CheckTrustState(cert x509.Certificate) bool {
	for k, v := range d.clientCerts {
		if bytes.Compare(cert.Raw, v.Raw) == 0 {
			shared.Log.Debug("found cert", log.Ctx{"k": k})
			return true
		}
		shared.Log.Debug("client cert != key", log.Ctx{"k": k})
	}
	return false
}

func (d *Daemon) ListRegularContainers() ([]string, error) {
	q := fmt.Sprintf("SELECT name FROM containers WHERE type=?")
	inargs := []interface{}{cTypeRegular}
	var name string
	outfmt := []interface{}{name}

	list := []string{}
	result, err := dbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return list, err
	}
	for _, r := range result {
		list = append(list, r[0].(string))
	}
	return list, nil
}

func (d *Daemon) numRunningContainers() (int, error) {
	results, err := d.ListRegularContainers()
	if err != nil {
		return 0, err
	}

	count := 0
	for _, r := range results {
		container, err := newLxdContainer(r, d)
		if err != nil {
			continue
		}

		if container.c.State() != lxc.STOPPED {
			count = count + 1
		}
	}

	return count, nil
}

var errStop = fmt.Errorf("requested stop")

// Stop stops the shared daemon.
func (d *Daemon) Stop() error {
	d.tomb.Kill(errStop)
	for _, socket := range d.Sockets {
		socket.Close()
	}

	if n, err := d.numRunningContainers(); err != nil || n == 0 {
		shared.Debugf("daemon.Stop: unmounting shmounts: err %s n %d", err, n)
		syscall.Unmount(shared.VarPath("shmounts"), syscall.MNT_DETACH)
		os.RemoveAll(shared.VarPath("shmounts"))
	} else {
		shared.Debugf("daemon.Stop: not unmounting shmounts")
	}

	d.db.Close()

	d.devlxd.Close()

	err := d.tomb.Wait()
	if err == errStop {
		return nil
	}
	return err
}

// ConfigKeyIsValid returns if the given key is a known config value.
func (d *Daemon) ConfigKeyIsValid(key string) bool {
	switch key {
	case "core.https_address":
		return true
	case "core.trust_password":
		return true
	case "core.lvm_vg_name":
		return true
	case "core.lvm_thinpool_name":
		return true
	}

	return false
}

// ConfigValueGet returns a config value from the memory,
// calls ConfigValuesGet if required.
// It returns a empty result if the config key isn't given.
func (d *Daemon) ConfigValueGet(key string) (string, error) {
	if d.configValues == nil {
		if _, err := d.ConfigValuesGet(); err != nil {
			return "", err
		}
	}

	if val, ok := d.configValues[key]; ok {
		return val, nil
	}

	return "", nil
}

// ConfigValuesGet fetches all config values and stores them in memory.
func (d *Daemon) ConfigValuesGet() (map[string]string, error) {
	if d.configValues == nil {
		var err error
		d.configValues, err = dbConfigValuesGet(d.db)
		if err != nil {
			return d.configValues, err
		}
	}

	return d.configValues, nil
}

// ConfigValueSet sets a new or updates a config value,
// it updates the value in the DB and in memory.
func (d *Daemon) ConfigValueSet(key string, value string) error {
	if err := dbConfigValueSet(d.db, key, value); err != nil {
		return err
	}

	if d.configValues == nil {
		if _, err := d.ConfigValuesGet(); err != nil {
			return err
		}
	}

	if value == "" {
		delete(d.configValues, key)
	} else {
		d.configValues[key] = value
	}

	return nil
}

// PasswordSet sets the password to the new value.
func (d *Daemon) PasswordSet(password string) error {
	shared.Log.Info("setting new password")
	var value = password
	if password != "" {
		buf := make([]byte, pwSaltBytes)
		_, err := io.ReadFull(rand.Reader, buf)
		if err != nil {
			return err
		}

		hash, err := scrypt.Key([]byte(password), buf, 1<<14, 8, 1, pwHashBytes)
		if err != nil {
			return err
		}

		buf = append(buf, hash...)
		value = hex.EncodeToString(buf)
	}

	err := d.ConfigValueSet("core.trust_password", value)
	if err != nil {
		return err
	}

	return nil
}

// PasswordCheck checks if the given password is the same
// as we have in the DB.
func (d *Daemon) PasswordCheck(password string) bool {
	value, err := d.ConfigValueGet("core.trust_password")
	if err != nil {
		shared.Log.Error("verifyAdminPwd", log.Ctx{"err": err})
		return false
	}

	buff, err := hex.DecodeString(value)
	if err != nil {
		shared.Log.Error("hex decode failed", log.Ctx{"err": err})
		return false
	}

	salt := buff[0:pwSaltBytes]
	hash, err := scrypt.Key([]byte(password), salt, 1<<14, 8, 1, pwHashBytes)
	if err != nil {
		shared.Log.Error("failed to create hash to check", log.Ctx{"err": err})
		return false
	}
	if !bytes.Equal(hash, buff[pwSaltBytes:]) {
		shared.Log.Error("Bad password received", log.Ctx{"err": err})
		return false
	}
	shared.Log.Debug("Verified the admin password")
	return true
}
