package daemon

import (
	"net/http"
)

type Response interface {
	Render(w http.ResponseWriter) error
	String() string
}
