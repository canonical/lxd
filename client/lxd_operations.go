package lxd

import (
	"fmt"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared/api"
)

// GetOperation returns an Operation entry for the provided uuid
func (r *ProtocolLXD) GetOperation(uuid string) (*api.Operation, string, error) {
	op := api.Operation{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/operations/%s", uuid), nil, "", &op)
	if err != nil {
		return nil, "", err
	}

	return &op, etag, nil
}

// GetOperationWebsocket returns a websocket connection for the provided operation
func (r *ProtocolLXD) GetOperationWebsocket(uuid string, secret string) (*websocket.Conn, error) {
	path := fmt.Sprintf("/operations/%s/websocket", uuid)
	if secret != "" {
		path = fmt.Sprintf("%s?secret=%s", path, secret)
	}

	return r.websocket(path)
}

// DeleteOperation deletes (cancels) a running operation
func (r *ProtocolLXD) DeleteOperation(uuid string) error {
	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/operations/%s", uuid), nil, "")
	if err != nil {
		return err
	}

	return nil
}
