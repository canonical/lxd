package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	_ "github.com/mattn/go-sqlite3"
	"gopkg.in/tomb.v2"
)

// A Daemon can respond to requests from a shared client.
type Daemon struct {
	tomb        tomb.Tomb
	unixl       net.Listener
	tcpl        net.Listener
	id_map      *Idmap
	lxcpath     string
	certf       string
	keyf        string
	mux         *mux.Router
	clientCerts map[string]x509.Certificate
	db          *sql.DB
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

func (d *Daemon) createCmd(version string, c Command) {
	var uri string
	if c.name == "" {
		uri = fmt.Sprintf("/%s", version)
	} else {
		uri = fmt.Sprintf("/%s/%s", version, c.name)
	}

	d.mux.HandleFunc(uri, func(w http.ResponseWriter, r *http.Request) {

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
		SyncResponse(true, []string{"/1.0"}).Render(w)
	})

	for _, c := range api10 {
		d.createCmd("1.0", c)
	}

	d.mux.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		shared.Debugf("sending top level 404: %s", r.URL)
		NotFound.Render(w)
	})

	d.id_map, err = NewIdmap()
	if err != nil {
		shared.Logf("error reading idmap: %s", err.Error())
		shared.Logf("operations requiring idmap will not be available")
	} else {
		shared.Debugf("idmap is %d %d %d %d\n",
			d.id_map.Uidmin,
			d.id_map.Uidrange,
			d.id_map.Gidmin,
			d.id_map.Gidrange)
	}

	localSocket := shared.VarPath("unix.socket")

	// If the socket exists, let's try to connect to it and see if there's
	// a lxd running.
	if _, err := os.Stat(localSocket); err == nil {
		c := &lxd.Config{Remotes: map[string]lxd.RemoteConfig{}}
		_, err := lxd.NewClient(c, "")
		if err != nil {
			shared.Debugf("Detected old but dead unix socket, deleting it...")
			// Connecting failed, so let's delete the socket and
			// listen on it ourselves.
			err = os.Remove(localSocket)
			if err != nil {
				return nil, err
			}
		}
	}

	unixAddr, err := net.ResolveUnixAddr("unix", localSocket)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve unix socket address: %v", err)
	}
	unixl, err := net.ListenUnix("unix", unixAddr)
	if err != nil {
		return nil, fmt.Errorf("cannot listen on unix socket: %v", err)
	}
	d.unixl = unixl

	if err := os.Chmod(localSocket, 0775); err != nil {
		return nil, err
	}

	gid, err := shared.GroupId(*group)
	if err != nil {
		return nil, err
	}

	if err := os.Chown(localSocket, os.Getuid(), gid); err != nil {
		return nil, err
	}

	var tcpListen func() error
	if listenAddr != "" {
		// Watch out. There's a listener active which must be closed on errors.
		mycert, err := tls.LoadX509KeyPair(d.certf, d.keyf)
		if err != nil {
			return nil, err
		}
		config := tls.Config{Certificates: []tls.Certificate{mycert},
			ClientAuth: tls.RequireAnyClientCert,
			MinVersion: tls.VersionTLS12,
			MaxVersion: tls.VersionTLS12}
		tcpl, err := tls.Listen("tcp", listenAddr, &config)
		if err != nil {
			d.unixl.Close()
			return nil, fmt.Errorf("cannot listen on unix socket: %v", err)
		}
		d.tcpl = tcpl
		tcpListen = func() error { return http.Serve(d.tcpl, d.mux) }
	}

	d.tomb.Go(func() error {
		if tcpListen != nil {
			d.tomb.Go(tcpListen)
		}
		d.tomb.Go(func() error { return http.Serve(d.unixl, d.mux) })
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
	d.unixl.Close()
	if d.tcpl != nil {
		d.tcpl.Close()
	}
	err := d.tomb.Wait()
	if err == errStop {
		return nil
	}
	return err
}
