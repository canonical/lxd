package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"

	"github.com/lxc/lxd"
	"gopkg.in/lxc/go-lxc.v2"
	"gopkg.in/tomb.v2"
)

// A Daemon can respond to requests from a lxd client.
type Daemon struct {
	tomb        tomb.Tomb
	unixl       net.Listener
	tcpl        net.Listener
	id_map      *Idmap
	lxcpath     string
	certf       string
	keyf        string
	mux         *http.ServeMux
	clientCerts map[string]x509.Certificate
}

type Command struct {
	name   string
	GET    func(d *Daemon, w http.ResponseWriter, r *http.Request)
	PUT    func(d *Daemon, w http.ResponseWriter, r *http.Request)
	POST   func(d *Daemon, w http.ResponseWriter, r *http.Request)
	DELETE func(d *Daemon, w http.ResponseWriter, r *http.Request)
}

func readMyCert() (string, string, error) {
	certf := lxd.VarPath("cert.pem")
	keyf := lxd.VarPath("key.pem")
	lxd.Debugf("looking for existing certificates: %s %s", certf, keyf)

	err := lxd.FindOrGenCert(certf, keyf)

	return certf, keyf, err
}

func readSavedClientCAList(d *Daemon) {
	dirpath := lxd.VarPath("clientcerts")
	d.clientCerts = make(map[string]x509.Certificate)
	fil, err := ioutil.ReadDir(dirpath)
	if err != nil {
		return
	}
	for i := range fil {
		n := fil[i].Name()
		fnam := fmt.Sprintf("%s/%s", dirpath, n)
		cf, err := ioutil.ReadFile(fnam)
		if err != nil {
			continue
		}

		cert, err := x509.ParseCertificate(cf)
		if err != nil {
			continue
		}
		d.clientCerts[n] = *cert
		lxd.Debugf("Loaded cert %s", fnam)
	}
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
	uri := fmt.Sprintf("/%s/%s", version, c.name)
	d.mux.HandleFunc(uri, func(w http.ResponseWriter, r *http.Request) {

		lxd.Debugf("handling %s %s", r.Method, r.URL.RequestURI())
		switch r.Method {
		case "GET":
			if c.GET == nil {
				NotImplemented(w)
			} else {
				c.GET(d, w, r)
			}
		case "PUT":
			if c.PUT == nil {
				NotImplemented(w)
			} else {
				c.PUT(d, w, r)
			}
		case "POST":
			if c.POST == nil {
				NotImplemented(w)
			} else {
				c.POST(d, w, r)
			}
		case "DELETE":
			if c.DELETE == nil {
				NotImplemented(w)
			} else {
				c.DELETE(d, w, r)
			}
		}
	})
}

// StartDaemon starts the lxd daemon with the provided configuration.
func StartDaemon(listenAddr string) (*Daemon, error) {
	d := &Daemon{}

	d.lxcpath = lxd.VarPath("lxc")
	err := os.MkdirAll(lxd.VarPath("/"), 0755)
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

	// TODO load known client certificates
	readSavedClientCAList(d)

	d.mux = http.NewServeMux()

	d.mux.HandleFunc("/trust", d.serveTrust)
	d.mux.HandleFunc("/trust/add", d.serveTrustAdd)
	d.mux.HandleFunc("/shell", d.serveShell)
	d.mux.HandleFunc("/list", d.serveList)
	d.mux.HandleFunc("/start", buildByNameServe("start", func(c *lxc.Container) error { return c.Start() }, d))
	d.mux.HandleFunc("/stop", buildByNameServe("stop", func(c *lxc.Container) error { return c.Stop() }, d))
	d.mux.HandleFunc("/delete", buildByNameServe("delete", func(c *lxc.Container) error { return c.Destroy() }, d))
	d.mux.HandleFunc("/restart", buildByNameServe("restart", func(c *lxc.Container) error { return c.Reboot() }, d))

	for _, c := range api10 {
		d.createCmd("1.0", c)
	}

	d.id_map, err = NewIdmap()
	if err != nil {
		lxd.Logf("error reading idmap: %s", err.Error())
		lxd.Logf("operations requiring idmap will not be available")
	} else {
		lxd.Debugf("idmap is %d %d %d %d\n",
			d.id_map.Uidmin,
			d.id_map.Uidrange,
			d.id_map.Gidmin,
			d.id_map.Gidrange)
	}

	unixAddr, err := net.ResolveUnixAddr("unix", lxd.VarPath("unix.socket"))
	if err != nil {
		return nil, fmt.Errorf("cannot resolve unix socket address: %v", err)
	}
	unixl, err := net.ListenUnix("unix", unixAddr)
	if err != nil {
		return nil, fmt.Errorf("cannot listen on unix socket: %v", err)
	}
	d.unixl = unixl

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
		d.tomb.Go(func() error { return http.Serve(d.tcpl, d.mux) })
	}

	d.tomb.Go(func() error { return http.Serve(d.unixl, d.mux) })
	return d, nil
}

func (d *Daemon) CheckTrustState(cert x509.Certificate) bool {
	for k, v := range d.clientCerts {
		if bytes.Compare(cert.Raw, v.Raw) == 0 {
			lxd.Debugf("found cert for %s", k)
			return true
		} else {
			lxd.Debugf("client cert != key for %s", k)
		}
	}
	return false
}

var errStop = fmt.Errorf("requested stop")

// Stop stops the lxd daemon.
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

// None of the daemon methods should print anything to stdout or stderr. If
// there's a local issue in the daemon that the admin should know about, it
// should be logged using either Logf or Debugf.
//
// Then, all of those issues that prevent the request from being served properly
// for any reason (bad parameters or any other local error) should be notified
// back to the client by writing an error json document to w, which in turn will
// be read by the client and returned via the API as an error result. These
// errors then surface via the CLI (cmd/lxd/*) in os.Stderr.
//
// Together, these ideas ensure that we have a proper daemon, and a proper client,
// which can both be used independently and also embedded into other applications.
