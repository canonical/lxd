package shared

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var WebsocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type Operation struct {
	Id         string              `json:"id"`
	Class      string              `json:"class"`
	CreatedAt  time.Time           `json:"created_at"`
	UpdatedAt  time.Time           `json:"updated_at"`
	Status     string              `json:"status"`
	StatusCode StatusCode          `json:"status_code"`
	Resources  map[string][]string `json:"resources"`
	Metadata   *Jmap               `json:"metadata"`
	MayCancel  bool                `json:"may_cancel"`
	Err        string              `json:"err"`
}
