package main

import (
	"fmt"
	"net"
	"net/http"
	"os"

	"gopkg.in/tomb.v2"
	"github.com/lxc/lxd"
)

// A Daemon can respond to requests from a lxd client.
type Daemon struct {
	tomb    tomb.Tomb
	unixl   net.Listener
	tcpl    net.Listener
	id_map  *Idmap
	lxcpath string
	mux     *http.ServeMux
}

// StartDaemon starts the lxd daemon with the provided configuration.
func StartDaemon(listenAddr string) (*Daemon, error) {
	d := &Daemon{}
	d.mux = http.NewServeMux()
	d.mux.HandleFunc("/ping", d.servePing)
	d.mux.HandleFunc("/create", d.serveCreate)

	var err error
	d.id_map, err = NewIdmap()
	if err != nil {
		return nil, err
	}
	lxd.Debugf("idmap is %d %d %d %d\n",
		d.id_map.Uidmin,
		d.id_map.Uidrange,
		d.id_map.Gidmin,
		d.id_map.Gidrange)

	d.lxcpath = lxd.VarPath("lxc")
	err = os.MkdirAll(lxd.VarPath("/"), 0755)
	if err != nil {
		return nil, err
	}
	err = os.MkdirAll(d.lxcpath, 0755)
	if err != nil {
		return nil, err
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
		tcpAddr, err := net.ResolveTCPAddr("tcp", listenAddr)
		if err != nil {
			d.unixl.Close()
			return nil, fmt.Errorf("cannot resolve unix socket address: %v", err)
		}
		tcpl, err := net.ListenTCP("tcp", tcpAddr)
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
