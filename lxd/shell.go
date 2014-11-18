package main

import (
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/kr/pty"
	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd"
)

func (d *Daemon) serveShell(w http.ResponseWriter, r *http.Request) {
	lxd.Debugf("responding to shell")

	if !d.is_trusted_client(r) {
		lxd.Debugf("Shell request from untrusted client")
		return
	}

	name := r.FormValue("name")
	if name == "" {
		fmt.Fprintf(w, "failed parsing name")
		return
	}

	command := r.FormValue("command")
	if command == "" {
		fmt.Fprintf(w, "failed parsing command")
		return
	}

	secret := r.FormValue("secret")
	if secret == "" {
		fmt.Fprintf(w, "failed parsing secret")
		return
	}

	c, err := lxc.NewContainer(name, d.lxcpath)
	if err != nil {
		lxd.Debugf("Error loading container %q: %q", name, err.Error())
		return
	}

	addr := ":0"
	// tcp6 doesn't seem to work with Dial("tcp", ) at the client
	l, err := net.Listen("tcp4", addr)
	if err != nil {
		fmt.Fprintf(w, "failed listening")
		return
	}
	fmt.Fprintf(w, "%s", l.Addr().String())

	go func(l net.Listener, name string, command string, secret string) {
		conn, err := l.Accept()
		l.Close()
		if err != nil {
			lxd.Debugf(err.Error())
			return
		}
		defer conn.Close()

		// FIXME(niemeyer): This likely works okay because the kernel tends to
		// be sane enough to not break down such a small amount of data into
		// multiple operations. That said, if we were to make it work
		// independent of the good will of the kernel and network layers, we'd
		// have to take into account that Read might also return a single byte,
		// for example, and then return more when it was next called. Or, it
		// might return a password plus more data that the client delivered
		// anticipating it would have a successful authentication.
		//
		// We could easily handle it using buffered io (bufio package), but that
		// would spoil the use of conn directly below when binding it to
		// the pty. So, given it's a trivial amount of data, I suggest calling
		// a local helper function that will read byte by byte until it finds
		// a predefined delimiter ('\n'?) and returns (data string, err error).
		//
		b := make([]byte, 100)
		n, err := conn.Read(b)
		if err != nil {
			lxd.Debugf("bad read: %s", err.Error())
			return
		}
		if n != len(secret) {
			lxd.Debugf("read %d characters, secret is %d", n, len(secret))
			return
		}
		if string(b[:n]) != secret {
			lxd.Debugf("Wrong secret received from shell client")
			return
		}

		pty, tty, err := pty.Open()

		if err != nil {
			lxd.Debugf("Failed opening getting a tty: %q", err.Error())
			return
		}

		defer pty.Close()
		defer tty.Close()

		/*
		 * The pty will be passed to the container's Attach.  The two
		 * below goroutines will copy output from the socket to the
		 * pty.stdin, and from pty.std{out,err} to the socket
		 * If the RunCommand exits, we want ourselves (the gofunc) and
		 * the copy-goroutines to exit.  If the connection closes, we
		 * also want to exit
		 */
		go func() {
			io.Copy(pty, conn)
			lxd.Debugf("shell to %q: conn->pty exiting", name)
			return
		}()
		go func() {
			io.Copy(conn, pty)
			lxd.Debugf("shell to %q: pty->conn exiting", name)
			return
		}()

		options := lxc.DefaultAttachOptions

		options.StdinFd = tty.Fd()
		options.StdoutFd = tty.Fd()
		options.StderrFd = tty.Fd()

		options.ClearEnv = true

		_, err = c.RunCommand([]string{command}, options)
		if err != nil {
			lxd.Debugf("Failed starting shell in %q: %q", name, err.Error())
			return
		}

		lxd.Debugf("RunCommand exited, stopping console")
	}(l, name, command, secret)
}
