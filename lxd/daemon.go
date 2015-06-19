package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stgraber/lxd-go-systemd/activation"
	"gopkg.in/tomb.v2"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
)

// A Daemon can respond to requests from a shared client.
type Daemon struct {
	tomb        tomb.Tomb
	IdmapSet    *shared.IdmapSet
	lxcpath     string
	certf       string
	keyf        string
	mux         *mux.Router
	clientCerts []x509.Certificate
	db          *sql.DB

	localSockets  []net.Listener
	remoteSockets []net.Listener

	tlsconfig *tls.Config

	devlxd *net.UnixListener
}

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
		return nil, fmt.Errorf("unexpected non-sync response!")
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
	shared.Debugf("looking for existing certificates: %s %s", certf, keyf)

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

func isJsonRequest(r *http.Request) bool {
	for k, vs := range r.Header {
		if strings.ToLower(k) == "content-type" &&
			len(vs) == 1 && strings.ToLower(vs[0]) == "application/json" {
			return true
		}
	}

	return false
}

func (d *Daemon) isRecursionRequest(r *http.Request) bool {
	recursion_str := r.FormValue("recursion")
	recursion, err := strconv.Atoi(recursion_str)
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
			shared.Debugf("handling %s %s", r.Method, r.URL.RequestURI())
		} else if r.Method == "GET" && c.untrustedGet {
			shared.Debugf("allowing untrusted GET to %s", r.URL.RequestURI())
		} else if r.Method == "POST" && c.untrustedPost {
			shared.Debugf("allowing untrusted POST to %s", r.URL.RequestURI())
		} else {
			shared.Debugf("rejecting request from untrusted client")
			Forbidden.Render(w)
			return
		}

		if *debug && r.Method != "GET" && isJsonRequest(r) {
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
				shared.Debugf("failed writing error for error, giving up.")
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

// StartDaemon starts the shared daemon with the provided configuration.
func StartDaemon(listenAddr string) (*Daemon, error) {
	d := &Daemon{}

	d.lxcpath = shared.VarPath("lxc")
	err := os.MkdirAll(shared.VarPath("/"), 0755)
	if err != nil {
		return nil, err
	}
	err = os.MkdirAll(d.lxcpath, 0755)
	if err != nil {
		return nil, err
	}

	certf, keyf, err := readMyCert()
	if err != nil {
		return nil, err
	}
	d.certf = certf
	d.keyf = keyf

	err = initDb(d)
	if err != nil {
		return nil, err
	}

	readSavedClientCAList(d)

	d.mux = mux.NewRouter()

	d.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		SyncResponse(true, []string{"/1.0"}).Render(w)
	})

	for _, c := range api10 {
		d.createCmd("1.0", c)
	}

	d.mux.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		shared.Debugf("sending top level 404: %s", r.URL)
		w.Header().Set("Content-Type", "application/json")
		NotFound.Render(w)
	})

	d.IdmapSet, err = shared.DefaultIdmapSet()
	if err != nil {
		shared.Logf("error reading idmap: %s", err.Error())
		shared.Logf("operations requiring idmap will not be available")
	} else {
		shared.Debugf("Default uid/gid map:")
		for _, lxcmap := range d.IdmapSet.ToLxcString() {
			shared.Debugf(" - " + lxcmap)
		}
	}

	tlsConfig, err := shared.GetTLSConfig(d.certf, d.keyf)
	if err != nil {
		return nil, err
	}

	listeners, err := activation.Listeners(false)
	if err != nil {
		return nil, err
	}

	var localSockets []net.Listener
	var remoteSockets []net.Listener

	if len(listeners) > 0 {
		shared.Debugf("LXD is socket activated.\n")

		for _, listener := range listeners {
			if _, err := os.Stat(listener.Addr().String()); err == nil {
				localSockets = append(localSockets, listener)
			} else {
				tlsListener := tls.NewListener(listener, tlsConfig)
				remoteSockets = append(remoteSockets, tlsListener)
			}
		}
	} else {
		shared.Debugf("LXD isn't socket activated.\n")

		localSocketPath := shared.VarPath("unix.socket")

		// If the socket exists, let's try to connect to it and see if there's
		// a lxd running.
		if _, err := os.Stat(localSocketPath); err == nil {
			c := &lxd.Config{Remotes: map[string]lxd.RemoteConfig{}}
			_, err := lxd.NewClient(c, "")
			if err != nil {
				shared.Debugf("Detected old but dead unix socket, deleting it...")
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

		localSockets = append(localSockets, unixl)

		if listenAddr != "" {
			tcpl, err := tls.Listen("tcp", listenAddr, tlsConfig)
			if err != nil {
				return nil, fmt.Errorf("cannot listen on unix socket: %v", err)
			}

			remoteSockets = append(remoteSockets, tcpl)
		}
	}

	d.localSockets = localSockets
	d.remoteSockets = remoteSockets
	d.devlxd, err = createAndBindDevLxd()
	if err != nil {
		return nil, err
	}

	containersRestart(d)
	containersWatch(d)

	d.tomb.Go(func() error {
		for _, socket := range d.localSockets {
			shared.Debugf(" - binding local socket: %s\n", socket.Addr())
			d.tomb.Go(func() error { return http.Serve(socket, d.mux) })
		}
		for _, socket := range d.remoteSockets {
			shared.Debugf(" - binding remote socket: %s\n", socket.Addr())
			d.tomb.Go(func() error { return http.Serve(socket, d.mux) })
		}

		d.tomb.Go(func() error {
			server := devLxdServer(d)
			return server.Serve(d.devlxd)
		})
		return nil
	})

	return d, nil
}

func (d *Daemon) CheckTrustState(cert x509.Certificate) bool {
	for k, v := range d.clientCerts {
		if bytes.Compare(cert.Raw, v.Raw) == 0 {
			shared.Debugf("found cert for %s", k)
			return true
		}
		shared.Debugf("client cert != key for %s", k)
	}
	return false
}

var errStop = fmt.Errorf("requested stop")

// Stop stops the shared daemon.
func (d *Daemon) Stop() error {
	d.tomb.Kill(errStop)
	for _, socket := range d.localSockets {
		socket.Close()
	}
	for _, socket := range d.remoteSockets {
		socket.Close()
	}

	d.devlxd.Close()

	err := d.tomb.Wait()
	if err == errStop {
		return nil
	}
	return err
}
