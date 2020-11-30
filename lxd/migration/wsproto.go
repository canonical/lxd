package migration

import (
	"fmt"
	"io/ioutil"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
)

// ProtoRecv gets a protobuf message from a websocket
func ProtoRecv(ws *websocket.Conn, msg proto.Message) error {
	mt, r, err := ws.NextReader()
	if err != nil {
		return err
	}

	if mt != websocket.BinaryMessage {
		return fmt.Errorf("Only binary messages allowed")
	}

	buf, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}

	err = proto.Unmarshal(buf, msg)
	if err != nil {
		return err
	}

	return nil
}

// ProtoSend sends a protobuf message over a websocket
func ProtoSend(ws *websocket.Conn, msg proto.Message) error {
	w, err := ws.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return err
	}
	defer w.Close()

	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}

	err = shared.WriteAll(w, data)
	if err != nil {
		return err
	}

	return nil
}

// ProtoSendControl sends a migration control message over a websocket
func ProtoSendControl(ws *websocket.Conn, err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}

	msg := MigrationControl{
		Success: proto.Bool(err == nil),
		Message: proto.String(message),
	}

	ProtoSend(ws, &msg)
}
