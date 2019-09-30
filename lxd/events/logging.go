package events

import (
	"fmt"

	log "github.com/lxc/lxd/shared/log15"

	"github.com/lxc/lxd/shared/api"
)

// LoggingServer controls what server to use for messages coming from the logger.
var LoggingServer *Server

// Handler describes an event handler.
type Handler struct {
}

// NewEventHandler creates and returns a new event handler.
func NewEventHandler() *Handler {
	return &Handler{}
}

// Log sends a new logging event.
func (h Handler) Log(r *log.Record) error {
	if LoggingServer == nil {
		return fmt.Errorf("No configured event server for logging messages")
	}

	LoggingServer.Send("", "logging", api.EventLogging{
		Message: r.Msg,
		Level:   r.Lvl.String(),
		Context: logContextMap(r.Ctx)})
	return nil
}

func logContextMap(ctx []interface{}) map[string]string {
	var key string
	ctxMap := map[string]string{}

	for _, entry := range ctx {
		if key == "" {
			key = entry.(string)
		} else {
			ctxMap[key] = fmt.Sprintf("%v", entry)
			key = ""
		}
	}

	return ctxMap
}
