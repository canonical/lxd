package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/satori/go.uuid"

	"github.com/lxc/lxd/shared"
)

var eventsLock sync.Mutex
var eventListeners map[string]*eventListener = make(map[string]*eventListener)

type eventListener struct {
	connection   *websocket.Conn
	messageTypes []string
	active       chan bool
	id           string
}

type eventsServe struct {
	req *http.Request
}

func (r *eventsServe) Render(w http.ResponseWriter) error {
	return eventsSocket(r.req, w)
}

func eventsSocket(r *http.Request, w http.ResponseWriter) error {
	listener := eventListener{}

	typeStr := r.FormValue("type")
	if typeStr == "" {
		typeStr = "logging,operations"
	}

	c, err := shared.WebsocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}

	listener.active = make(chan bool, 1)
	listener.connection = c
	listener.id = uuid.NewV4().String()
	listener.messageTypes = strings.Split(typeStr, ",")

	eventsLock.Lock()
	eventListeners[listener.id] = &listener
	eventsLock.Unlock()

	shared.Debugf("New events listener: %s", listener.id)

	<-listener.active

	return nil
}

func eventsGet(d *Daemon, r *http.Request) Response {
	return &eventsServe{r}
}

var eventsCmd = Command{name: "events", get: eventsGet}

func eventSend(eventType string, eventMessage interface{}) error {
	event := shared.Jmap{}
	event["type"] = eventType
	event["timestamp"] = time.Now()
	event["metadata"] = eventMessage

	body, err := json.Marshal(event)
	if err != nil {
		return err
	}

	eventsLock.Lock()
	listeners := eventListeners
	eventsLock.Unlock()

	for _, listener := range listeners {
		go func(listener *eventListener, body []byte) {
			err = listener.connection.WriteMessage(websocket.TextMessage, body)
			if err != nil {
				listener.connection.Close()
				listener.active <- false

				eventsLock.Lock()
				delete(eventListeners, listener.id)
				eventsLock.Unlock()

				shared.Debugf("Disconnected events listener: %s", listener.id)
			}
		}(listener, body)
	}

	return nil
}
