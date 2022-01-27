package ws

import (
	"net/http"

	"github.com/gorilla/websocket"
)

// Upgrader is a websocket upgrader which ignores the request Origin.
var Upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}
