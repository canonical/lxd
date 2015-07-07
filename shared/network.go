package shared

import (
	"errors"
	"io"
	"io/ioutil"
	"net"

	"github.com/gorilla/websocket"
)

func RFC3493Dialer(network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}

	addrs, err := net.LookupHost(host)
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		c, err := net.Dial(network, net.JoinHostPort(a, port))
		if err != nil {
			continue
		}
		return c, err
	}
	return nil, errors.New("Unable to connect to: " + address)
}

func IsLoopback(iface *net.Interface) bool {
	return int(iface.Flags&net.FlagLoopback) > 0
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
	done := make(chan bool, 2)
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
