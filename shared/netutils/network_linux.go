// +build linux

package netutils

import (
	"io"

	"github.com/gorilla/websocket"

	"github.com/grant-he/lxd/shared"
	"github.com/grant-he/lxd/shared/logger"
)

// WebsocketExecMirror mirrors a websocket connection with a set of Writer/Reader.
func WebsocketExecMirror(conn *websocket.Conn, w io.WriteCloser, r io.ReadCloser, exited chan struct{}, fd int) (chan bool, chan bool) {
	readDone := make(chan bool, 1)
	writeDone := make(chan bool, 1)

	go shared.DefaultWriter(conn, w, writeDone)

	go func(conn *websocket.Conn, r io.ReadCloser) {
		in := shared.ExecReaderToChannel(r, -1, exited, fd)
		for {
			buf, ok := <-in
			if !ok {
				r.Close()
				logger.Debugf("Sending write barrier")
				err := conn.WriteMessage(websocket.TextMessage, []byte{})
				if err != nil {
					logger.Debugf("Got err writing barrier %s", err)
				}
				readDone <- true
				return
			}

			err := conn.WriteMessage(websocket.BinaryMessage, buf)
			if err != nil {
				logger.Debugf("Got err writing %s", err)
				break
			}
		}

		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		conn.WriteMessage(websocket.CloseMessage, closeMsg)
		readDone <- true
		r.Close()
	}(conn, r)

	return readDone, writeDone
}
