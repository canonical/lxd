package api

import (
	"time"
)

// Operation represents a LXD background operation
type Operation struct {
	ID         string                 `json:"id"`
	Class      string                 `json:"class"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
	Status     string                 `json:"status"`
	StatusCode StatusCode             `json:"status_code"`
	Resources  map[string][]string    `json:"resources"`
	Metadata   map[string]interface{} `json:"metadata"`
	MayCancel  bool                   `json:"may_cancel"`
	Err        string                 `json:"err"`
}
