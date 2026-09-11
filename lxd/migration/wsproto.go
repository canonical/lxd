package migration

import (
	"errors"
	"io"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/canonical/lxd/shared"
)

// ProtoRecv gets a protobuf message from a websocket.
func ProtoRecv(ws *websocket.Conn, msg proto.Message) error {
	if ws == nil {
		return errors.New("Empty websocket connection")
	}

	mt, r, err := ws.NextReader()
	if err != nil {
		return err
	}

	if mt != websocket.BinaryMessage {
		return errors.New("Only binary messages allowed")
	}

	buf, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	err = proto.Unmarshal(buf, msg)
	if err != nil {
		return err
	}

	return nil
}

// ProtoSend sends a protobuf message over a websocket.
func ProtoSend(ws *websocket.Conn, msg proto.Message) error {
	if ws == nil {
		return errors.New("Empty websocket connection")
	}

	w, err := ws.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return err
	}

	defer func() { _ = w.Close() }()

	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}

	err = shared.WriteAll(w, data)
	if err != nil {
		return err
	}

	return w.Close()
}

// ProtoSendFrame writes a protobuf message as one barrier framed message, for channels that drivers only see as an io.ReadWriteCloser rather than a websocket.
func ProtoSendFrame(conn io.ReadWriteCloser, msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}

	err = shared.WriteAll(conn, data)
	if err != nil {
		return err
	}

	return conn.Close() // End the frame.
}

// ProtoRecvFrame reads one barrier framed message into a protobuf message, for channels that drivers only see as an io.Reader rather than a websocket.
func ProtoRecvFrame(conn io.Reader, msg proto.Message) error {
	buf, err := io.ReadAll(conn)
	if err != nil {
		return err
	}

	// A peer that closed without writing reads as an empty buffer, which unmarshals into a zero message. The
	// caller would then negotiate against values nobody sent and report a later failure instead of this one.
	if len(buf) == 0 {
		return errors.New("Empty migration frame")
	}

	return proto.Unmarshal(buf, msg)
}

// ProtoSendControl sends a migration control message over a websocket.
func ProtoSendControl(ws *websocket.Conn, err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}

	msg := MigrationControl{
		Success: new(err == nil),
		Message: new(message),
	}

	_ = ProtoSend(ws, &msg)
}
