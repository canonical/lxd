// +build linux
// +build cgo

package shared

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/gosexy/gettext"
)

// #cgo LDFLAGS: -lutil
/*
#include <unistd.h>
#include <stdlib.h>
#include <sys/types.h>
#include <grp.h>
#include <pty.h>
#include <errno.h>
#include <fcntl.h>
#include <string.h>
#include <stdio.h>

// This is an adaption from https://codereview.appspot.com/4589049, to be
// included in the stdlib with the stdlib's license.

static int mygetgrgid_r(int gid, struct group *grp,
	char *buf, size_t buflen, struct group **result) {
	return getgrgid_r(gid, grp, buf, buflen, result);
}

void configure_pty(int fd) {
	struct termios term_settings;
	struct winsize win;

	if (tcgetattr(fd, &term_settings) < 0) {
		printf("Failed to get settings: %s\n", strerror(errno));
		return;
	}

	term_settings.c_iflag |= IMAXBEL;
	term_settings.c_iflag |= IUTF8;
	term_settings.c_iflag |= BRKINT;
	term_settings.c_iflag |= IXANY;

	term_settings.c_cflag |= HUPCL;

	if (tcsetattr(fd, TCSANOW, &term_settings) < 0) {
		printf("Failed to set settings: %s\n", strerror(errno));
		return;
	}

	if (ioctl(fd, TIOCGWINSZ, &win) < 0) {
		printf("Failed to get the terminal size: %s\n", strerror(errno));
		return;
	}

	win.ws_col = 80;
	win.ws_row = 25;

	if (ioctl(fd, TIOCSWINSZ, &win) < 0) {
		printf("Failed to set the terminal size: %s\n", strerror(errno));
		return;
	}

	if (fcntl(fd, F_SETFD, FD_CLOEXEC) < 0) {
		printf("Failed to set FD_CLOEXEC: %s\n", strerror(errno));
		return;
	}

	return;
}

void create_pty(int *master, int *slave) {
	if (openpty(master, slave, NULL, NULL, NULL) < 0) {
		printf("Failed to openpty: %s\n", strerror(errno));
		return;
	}

	configure_pty(*master);
	configure_pty(*slave);
}

void create_pipe(int *master, int *slave) {
	int pipefd[2];

	if (pipe2(pipefd, O_CLOEXEC) < 0) {
		printf("Failed to create a pipe: %s\n", strerror(errno));
		return;
	}

	*master = pipefd[0];
	*slave = pipefd[1];
}

*/
import "C"

func OpenPty() (master *os.File, slave *os.File, err error) {
	fd_master := C.int(-1)
	fd_slave := C.int(-1)

	C.create_pty(&fd_master, &fd_slave)

	if fd_master == -1 || fd_slave == -1 {
		return nil, nil, errors.New("Failed to create a new pts pair")
	}

	master = os.NewFile(uintptr(fd_master), "master")
	slave = os.NewFile(uintptr(fd_slave), "slave")

	return master, slave, nil
}

func Pipe() (master *os.File, slave *os.File, err error) {
	fd_master := C.int(-1)
	fd_slave := C.int(-1)

	C.create_pipe(&fd_master, &fd_slave)

	if fd_master == -1 || fd_slave == -1 {
		return nil, nil, errors.New("Failed to create a new pipe")
	}

	master = os.NewFile(uintptr(fd_master), "master")
	slave = os.NewFile(uintptr(fd_slave), "slave")

	return master, slave, nil
}

// VarPath returns the provided path elements joined by a slash and
// appended to the end of $LXD_DIR, which defaults to /var/lib/lxd.
func VarPath(path ...string) string {
	varDir := os.Getenv("LXD_DIR")
	if varDir == "" {
		varDir = "/var/lib/lxd"
	}
	items := []string{varDir}
	items = append(items, path...)
	return filepath.Join(items...)
}

func ParseLXDFileHeaders(headers http.Header) (uid int, gid int, mode os.FileMode, err error) {

	uid, err = strconv.Atoi(headers.Get("X-LXD-uid"))
	if err != nil {
		return 0, 0, 0, err
	}

	gid, err = strconv.Atoi(headers.Get("X-LXD-gid"))
	if err != nil {
		return 0, 0, 0, err
	}

	/* Allow people to send stuff with a leading 0 for octal or a regular
	 * int that represents the perms when redered in octal. */
	rawMode, err := strconv.ParseInt(headers.Get("X-LXD-mode"), 0, 0)
	if err != nil {
		return 0, 0, 0, err
	}
	mode = os.FileMode(rawMode)

	return uid, gid, mode, nil
}

func ReadToJSON(r io.Reader, req interface{}) error {

	buf, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}

	return json.Unmarshal(buf, req)
}

func GenerateFingerprint(cert *x509.Certificate) string {
	return fmt.Sprintf("% x", sha256.Sum256(cert.Raw))
}

func ReaderToChannel(r io.Reader) <-chan []byte {

	ch := make(chan ([]byte))

	go func() {
		for {
			/* io.Copy uses a 32KB buffer, so we might as well too. */
			buf := make([]byte, 32*1024)
			nr, err := r.Read(buf)
			if nr > 0 {
				ch <- buf[0:nr]
			}

			if err != nil {
				close(ch)
				break
			}
		}
	}()

	return ch
}

func WebsocketSendStream(conn *websocket.Conn, r io.Reader) chan bool {
	ch := make(chan bool)

	go func(conn *websocket.Conn, r io.Reader) {
		in := ReaderToChannel(r)
		for {
			buf, ok := <-in
			if !ok {
				break
			}

			w, err := conn.NextWriter(websocket.BinaryMessage)
			if err != nil {
				Debugf("got error getting next writer %s", err)
				break
			}

			_, err = w.Write(buf)
			w.Close()
			if err != nil {
				Debugf("got err writing %s", err)
				break
			}
		}
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		conn.WriteMessage(websocket.CloseMessage, closeMsg)
		ch <- true
	}(conn, r)

	return ch
}

func WebsocketRecvStream(w io.WriteCloser, conn *websocket.Conn) chan bool {
	ch := make(chan bool)

	go func(w io.WriteCloser, conn *websocket.Conn) {
		for {
			mt, r, err := conn.NextReader()
			if mt == websocket.CloseMessage {
				Debugf("got close message for reader")
				break
			}

			if err != nil {
				Debugf("got error getting next reader %s, %s", err, w)
				break
			}

			buf, err := ioutil.ReadAll(r)
			if err != nil {
				Debugf("got error writing to writer %s", err)
				break
			}

			i, err := w.Write(buf)
			if i != len(buf) {
				Debugf("didn't write all of buf")
				break
			}
			if err != nil {
				Debugf("error writing buf %s", err)
				break
			}
		}
		ch <- true
	}(w, conn)

	return ch
}

// WebsocketMirror allows mirroring a reader to a websocket and taking the
// result and writing it to a writer.
func WebsocketMirror(conn *websocket.Conn, w io.WriteCloser, r io.Reader) chan bool {
	done := make(chan bool, 1)
	go func(conn *websocket.Conn, w io.WriteCloser) {
		for {
			mt, r, err := conn.NextReader()
			if mt == websocket.CloseMessage {
				Debugf("got close message for reader")
				break
			}

			if err != nil {
				Debugf("got error getting next reader %s, %s", err, w)
				break
			}
			buf, err := ioutil.ReadAll(r)
			if err != nil {
				Debugf("got error writing to writer %s", err)
				break
			}
			i, err := w.Write(buf)
			if i != len(buf) {
				Debugf("didn't write all of buf")
				break
			}
			if err != nil {
				Debugf("error writing buf %s", err)
				break
			}
		}
		done <- true
		w.Close()
	}(conn, w)

	go func(conn *websocket.Conn, r io.Reader) {
		in := ReaderToChannel(r)
		for {
			buf, ok := <-in
			if !ok {
				done <- true
				conn.WriteMessage(websocket.CloseMessage, []byte{})
				conn.Close()
				return
			}
			w, err := conn.NextWriter(websocket.BinaryMessage)
			if err != nil {
				Debugf("got error getting next writer %s", err)
				break
			}

			_, err = w.Write(buf)
			w.Close()
			if err != nil {
				Debugf("got err writing %s", err)
				break
			}
		}
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		conn.WriteMessage(websocket.CloseMessage, closeMsg)
		done <- true
	}(conn, r)

	return done
}

// Returns a random base64 encoded string from crypto/rand.
func RandomCryptoString() (string, error) {
	buf := make([]byte, 100)
	n, err := rand.Read(buf)
	if err != nil {
		return "", err
	}

	if n != len(buf) {
		return "", fmt.Errorf("not enough random bytes read")
	}

	return base64.StdEncoding.EncodeToString(buf), nil
}

func ReadCert(fpath string) (*x509.Certificate, error) {
	cf, err := ioutil.ReadFile(fpath)
	if err != nil {
		return nil, err
	}

	certBlock, _ := pem.Decode(cf)
	return x509.ParseCertificate(certBlock.Bytes)
}

func SplitExt(fpath string) (string, string) {
	b := path.Base(fpath)
	ext := path.Ext(fpath)
	return b[:len(b)-len(ext)], ext
}

func AtoiEmptyDefault(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}

	return strconv.Atoi(s)
}

// GroupName is an adaption from https://codereview.appspot.com/4589049.
func GroupName(gid int) (string, error) {
	var grp C.struct_group
	var result *C.struct_group

	bufSize := C.size_t(C.sysconf(C._SC_GETGR_R_SIZE_MAX))
	buf := C.malloc(bufSize)
	if buf == nil {
		return "", fmt.Errorf(gettext.Gettext("allocation failed"))
	}
	defer C.free(buf)

	// mygetgrgid_r is a wrapper around getgrgid_r to
	// to avoid using gid_t because C.gid_t(gid) for
	// unknown reasons doesn't work on linux.
	rv := C.mygetgrgid_r(C.int(gid),
		&grp,
		(*C.char)(buf),
		bufSize,
		&result)

	if rv != 0 {
		return "", fmt.Errorf(gettext.Gettext("failed group lookup: %s"), syscall.Errno(rv))
	}

	if result == nil {
		return "", fmt.Errorf(gettext.Gettext("unknown group %s"), gid)
	}

	return C.GoString(result.gr_name), nil
}

// GroupId is an adaption from https://codereview.appspot.com/4589049.
func GroupId(name string) (int, error) {
	var grp C.struct_group
	var result *C.struct_group

	bufSize := C.size_t(C.sysconf(C._SC_GETGR_R_SIZE_MAX))
	buf := C.malloc(bufSize)
	if buf == nil {
		return -1, fmt.Errorf(gettext.Gettext("allocation failed"))
	}
	defer C.free(buf)

	// mygetgrgid_r is a wrapper around getgrgid_r to
	// to avoid using gid_t because C.gid_t(gid) for
	// unknown reasons doesn't work on linux.
	rv := C.getgrnam_r(C.CString(name),
		&grp,
		(*C.char)(buf),
		bufSize,
		&result)

	if rv != 0 {
		return -1, fmt.Errorf(gettext.Gettext("failed group lookup: %s"), syscall.Errno(rv))
	}

	if result == nil {
		return -1, fmt.Errorf(gettext.Gettext("unknown group %s"), name)
	}

	return int(C.int(result.gr_gid)), nil
}

func ReadStdin() ([]byte, error) {
	buf := bufio.NewReader(os.Stdin)
	line, _, err := buf.ReadLine()
	if err != nil {
		return nil, err
	}
	return line, nil
}

func PathExists(name string) bool {
	_, err := os.Lstat(name)
	if err != nil && os.IsNotExist(err) {
		return false
	}
	return true
}

func IsDir(name string) bool {
	stat, err := os.Lstat(name)
	if err != nil {
		return false
	}
	return stat.IsDir()
}

func GetTLSConfig(certf string, keyf string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certf, keyf)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		ClientAuth:         tls.RequireAnyClientCert,
		Certificates:       []tls.Certificate{cert},
		MinVersion:         tls.VersionTLS12,
		MaxVersion:         tls.VersionTLS12,
	}

	tlsConfig.BuildNameToCertificate()

	return tlsConfig, nil
}
