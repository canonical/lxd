package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/pborman/uuid"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

var eventsCmd = Command{
	name: "events",
	get:  eventsGet,
}

type eventsHandler struct {
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

func (h eventsHandler) Log(r *log.Record) error {
	eventSend("", "logging", api.EventLogging{
		Message: r.Msg,
		Level:   r.Lvl.String(),
		Context: logContextMap(r.Ctx)})
	return nil
}

func eventSendLifecycle(project, action, source string,
	context map[string]interface{}) error {
	eventSend(project, "lifecycle", api.EventLifecycle{
		Action:  action,
		Source:  source,
		Context: context})
	return nil
}

var eventsLock sync.Mutex
var eventListeners map[string]*eventListener = make(map[string]*eventListener)

type eventListener struct {
	project      string
	connection   *websocket.Conn
	messageTypes []string
	active       chan bool
	id           string
	lock         sync.Mutex
	done         bool

	// If true, this listener won't get events forwarded from other
	// nodes. It only used by listeners created internally by LXD nodes
	// connecting to other LXD nodes to get their local events only.
	noForward bool
}

type eventsServe struct {
	req *http.Request
}

func (r *eventsServe) Render(w http.ResponseWriter) error {
	return eventsSocket(r.req, w)
}

func (r *eventsServe) String() string {
	return "event handler"
}

func eventsSocket(r *http.Request, w http.ResponseWriter) error {
	project := projectParam(r)
	typeStr := r.FormValue("type")
	if typeStr == "" {
		typeStr = "logging,operation,lifecycle"
	}

	c, err := shared.WebsocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}

	listener := eventListener{
		project:      project,
		active:       make(chan bool, 1),
		connection:   c,
		id:           uuid.NewRandom().String(),
		messageTypes: strings.Split(typeStr, ","),
	}

	// If this request is an internal one initiated by another node wanting
	// to watch the events on this node, set the listener to broadcast only
	// local events.
	listener.noForward = isClusterNotification(r)

	eventsLock.Lock()
	eventListeners[listener.id] = &listener
	eventsLock.Unlock()

	logger.Debugf("New event listener: %s", listener.id)

	<-listener.active

	return nil
}

func eventsGet(d *Daemon, r *http.Request) Response {
	return &eventsServe{req: r}
}

func eventSend(project, eventType string, eventMessage interface{}) error {
	encodedMessage, err := json.Marshal(eventMessage)
	if err != nil {
		return err
	}
	event := api.Event{
		Type:      eventType,
		Timestamp: time.Now(),
		Metadata:  encodedMessage,
	}

	return eventBroadcast(project, event, false)
}

func eventBroadcast(project string, event api.Event, isForward bool) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}

	eventsLock.Lock()
	listeners := eventListeners
	for _, listener := range listeners {
		if project != "" && listener.project != "*" && project != listener.project {
			continue
		}

		if isForward && listener.noForward {
			continue
		}

		if !shared.StringInSlice(event.Type, listener.messageTypes) {
			continue
		}

		go func(listener *eventListener, body []byte) {
			// Check that the listener still exists
			if listener == nil {
				return
			}

			// Ensure there is only a single even going out at the time
			listener.lock.Lock()
			defer listener.lock.Unlock()

			// Make sure we're not done already
			if listener.done {
				return
			}

			err := listener.connection.WriteMessage(websocket.TextMessage, body)
			if err != nil {
				// Remove the listener from the list
				eventsLock.Lock()
				delete(eventListeners, listener.id)
				eventsLock.Unlock()

				// Disconnect the listener
				listener.connection.Close()
				listener.active <- false
				listener.done = true
				logger.Debugf("Disconnected event listener: %s", listener.id)
			}
		}(listener, body)
	}
	eventsLock.Unlock()

	return nil
}

// Forward to the local events dispatcher an event received from another node .
func eventForward(id int64, event api.Event) {
	err := eventBroadcast("", event, true)
	if err != nil {
		logger.Warnf("Failed to forward event from node %d: %v", id, err)
	}
}
