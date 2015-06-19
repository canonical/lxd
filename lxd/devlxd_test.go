package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lxc/lxd"
)

func setupDir() error {
	os.RemoveAll("/tmp/tester")
	return os.Setenv("LXD_DIR", "/tmp/tester")
}

func setupSocket() (*net.UnixListener, error) {
	setupDir()

	os.MkdirAll("/tmp/tester", 0700)

	return createAndBindDevLxd()
}

func dialAndSendCreds(path string) (*net.UnixConn, error) {
	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return nil, err
	}

	conn, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		return nil, err
	}

	ucred := syscall.Ucred{
		Pid: int32(syscall.Gettid()),
		Uid: uint32(os.Getuid()),
		Gid: uint32(os.Getgid()),
	}

	oob := syscall.UnixCredentials(&ucred)
	fmt.Printf("dialAndSendCreds: oob is %q\n", oob)
	n, oobn, err := conn.WriteMsgUnix(nil, oob, nil)
	fmt.Printf("dialAndSendCreds: got back %d %d %q", n, oobn, err)
	if n != 0 {
		return nil, fmt.Errorf("wrote nonzero number bytes?")
	}

	if oobn != len(oob) {
		return nil, fmt.Errorf("didn't write all of unix creds")
	}

	if err != nil {
		return nil, err
	}

	return conn, nil
}

func TestCredsSendRecv(t *testing.T) {

	result := make(chan int32, 1)

	listener, err := setupSocket()
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	defer os.RemoveAll("/tmp/tester")

	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			t.Log(err)
			result <- -1
			return
		}
		defer conn.Close()

		fmt.Printf("yes i can debug here")
		pid, err := getPid(conn)
		if err != nil {
			t.Log(err)
			result <- -1
			return
		}
		result <- pid
	}()

	time.Sleep(3 * time.Second)
	conn, err := dialAndSendCreds("/tmp/tester/devlxd")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	pid := <-result
	fmt.Printf("got back pid %d;  I am tid %d\n", int(pid), int(syscall.Gettid()))
	if pid != int32(os.Getpid()) {
		t.Fatal("pid mismatch: ", pid, os.Getpid())
	}
}

/*
 * Here we're not really testing the API functionality (we can't, since it
 * expects us to be inside a container to work), but it is useful to test that
 * all the grotty connection extracting stuff works (that is, it gets to the
 * point where it realizes the pid isn't in a container without crashing).
 */
func TestHttpRequest(t *testing.T) {

	if err := setupDir(); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll("/tmp/tester")

	d, err := StartDaemon("")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	c := http.Client{Transport: &http.Transport{Dial: lxd.DevLxdDialer{Path: "/tmp/tester/devlxd"}.DevLxdDial}}

	raw, err := c.Get("http://1.0")
	if err != nil {
		t.Fatal(err)
	}

	if raw.StatusCode != 500 {
		t.Fatal(err)
	}

	resp, err := ioutil.ReadAll(raw.Body)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(resp), pidNotInContainerErr.Error()) {
		t.Fatal("resp error not expected: ", string(resp))
	}
}
