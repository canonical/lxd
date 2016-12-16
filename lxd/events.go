package main

import (
	"net/http"

	"github.com/lxc/lxd/lxd/events"
	"github.com/lxc/lxd/lxd/response"
)

func eventsGet(d *Daemon, r *http.Request) response.Response {
	return &events.EventsServe{Req: r}
}

var eventsCmd = Command{name: "events", get: eventsGet}
