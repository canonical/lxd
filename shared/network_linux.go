// +build linux

package shared

import (
	"io"
	"io/ioutil"

	"github.com/gorilla/websocket"
)

func WebsocketExecMirror(conn *websocket.Conn, w io.WriteCloser, r io.ReadCloser, exited chan bool, fd int) (chan bool, chan bool) {
	readDone := make(chan bool, 1)
	writeDone := make(chan bool, 1)

	go func(conn *websocket.Conn, w io.WriteCloser) {
		for {

			mt, r, err := conn.NextReader()
			if err != nil {
				LogDebugf("Got error getting next reader %s, %s", err, w)
				break
			}

			if mt == websocket.CloseMessage {
				LogDebugf("Got close message for reader")
				break
			}

			if mt == websocket.TextMessage {
				LogDebugf("Got message barrier, resetting stream")
				break
			}

			buf, err := ioutil.ReadAll(r)
			if err != nil {
				LogDebugf("Got error writing to writer %s", err)
				break
			}
			i, err := w.Write(buf)
			if i != len(buf) {
				LogDebugf("Didn't write all of buf")
				break
			}
			if err != nil {
				LogDebugf("Error writing buf %s", err)
				break
			}
		}
		writeDone <- true
		w.Close()
	}(conn, w)

	go func(conn *websocket.Conn, r io.ReadCloser) {
		/* For now, we don't need to adjust buffer sizes in
		 * WebsocketMirror, since it's used for interactive things like
		 * exec.
		 */
		in := ReaderToChannel(r, -1)
		written := 0
		out := false
		for {
			var buf []byte
			var ok bool

			select {
			case buf, ok = <-in:
				if !ok {
					r.Close()
					LogDebugf("sending write barrier")
					conn.WriteMessage(websocket.TextMessage, []byte{})
					readDone <- true
					return
				}
				if out {
					/* If the attached child exited and a
					* background process is still holding
					* stdout open, we can assume that one
					* full tty output buffer at maximum
					* still holds output from the attached
					* child so we spew that out. Everything
					* after this is from the background
					* process. The default buffer size seems
					* to be 65536. Maybe we'll come up with
					* a smarter way of handling this later.
					 */
					written += len(buf)
					if written > 65536 || FdHasData(fd) == 0 {
						r.Close()
						LogDebugf("sending write barrier")
						conn.WriteMessage(websocket.TextMessage, []byte{})
						readDone <- true
						return
					}
				}
				break
			case <-exited:
				out = true
				/* In case the attached child has exited before
				* all data from the tty input or output buffer
				* has been read, FdHasData() will always return
				* 1.
				 */
				if FdHasData(fd) == 0 {
					closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
					conn.WriteMessage(websocket.CloseMessage, closeMsg)
					readDone <- true
					r.Close()
					return
				}
			}

			w, err := conn.NextWriter(websocket.BinaryMessage)
			if err != nil {
				LogDebugf("Got error getting next writer %s", err)
				break
			}

			_, err = w.Write(buf)
			w.Close()
			if err != nil {
				LogDebugf("Got err writing %s", err)
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
