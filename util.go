package lxd

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strconv"

	"github.com/gorilla/websocket"
)

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

func WebsocketMirror(conn *websocket.Conn, w io.Writer, r io.ReadCloser) {
	done := make(chan bool, 1)

	go func() {
		for {
			select {
			case <-done:
				return
			default:
				break
			}

			mt, r, err := conn.NextReader()
			if mt == websocket.CloseMessage {
				break
			}

			if err != nil {
				Debugf("got error getting next reader %s", err)
				break
			}
			_, err = io.Copy(w, r)
			if err != nil {
				Debugf("got error writing to writer %s", err)
				break
			}
		}

		done <- true
		r.Close()
		conn.Close()
	}()

	in := ReaderToChannel(r)

	for {
		select {
		case <-done:
			return
		case buf, ok := <-in:
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
	}
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
