package main

import (
	"io"
	"io/ioutil"
	"os"
	"path"
	"testing"

	"github.com/lxc/lxd/shared"
)

const helloWorld = "hello world\n"

func TestRsyncSendRecv(t *testing.T) {
	source, err := ioutil.TempDir("", "lxd_test_source_")
	if err != nil {
		t.Error(err)
		return
	}
	defer os.RemoveAll(source)

	sink, err := ioutil.TempDir("", "lxd_test_sink_")
	if err != nil {
		t.Error(err)
		return
	}
	defer os.RemoveAll(sink)

	/* now, write something to rsync over */
	f, err := os.Create(path.Join(source, "foo"))
	if err != nil {
		t.Error(err)
		return
	}
	f.Write([]byte(helloWorld))
	f.Close()

	send, sendConn, _, err := rsyncSendSetup(shared.AddSlash(source))
	if err != nil {
		t.Error(err)
		return
	}

	recv := rsyncRecvCmd(sink)

	recvOut, err := recv.StdoutPipe()
	if err != nil {
		t.Error(err)
		return
	}

	recvIn, err := recv.StdinPipe()
	if err != nil {
		t.Error(err)
		return
	}

	if err := recv.Start(); err != nil {
		t.Error(err)
		return
	}

	go func() {
		defer sendConn.Close()
		if _, err := io.Copy(sendConn, recvOut); err != nil {
			t.Error(err)
		}

		if err := recv.Wait(); err != nil {
			t.Error(err)
		}

	}()

	/*
	 * We close the socket in the above gofunc, but go tells us
	 * https://github.com/golang/go/issues/4373 that this is an error
	 * because we were reading from a socket that was closed. Thus, we
	 * ignore it
	 */
	io.Copy(recvIn, sendConn)

	if err := send.Wait(); err != nil {
		t.Error(err)
		return
	}

	f, err = os.Open(path.Join(sink, "foo"))
	if err != nil {
		t.Error(err)
		return
	}
	defer f.Close()

	buf, err := ioutil.ReadAll(f)
	if err != nil {
		t.Error(err)
		return
	}

	if string(buf) != helloWorld {
		t.Errorf("expected %s got %s", helloWorld, buf)
		return
	}
}
