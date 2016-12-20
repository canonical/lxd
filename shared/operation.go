package shared

import (
	"time"
)

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
