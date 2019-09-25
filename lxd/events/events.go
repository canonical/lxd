package events

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/pborman/uuid"

	"github.com/gorilla/websocket"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

var debug bool
var verbose bool

var eventsLock sync.Mutex
var eventListeners map[string]*Listener = make(map[string]*Listener)

// Listener describes an event listener.
type Listener struct {
	project      string
	connection   *websocket.Conn
	messageTypes []string
	active       chan bool
	id           string
	lock         sync.Mutex
	done         bool
	location     string

	// If true, this listener won't get events forwarded from other
	// nodes. It only used by listeners created internally by LXD nodes
	// connecting to other LXD nodes to get their local events only.
	noForward bool
}

// NewEventListener creates and returns a new event listener.
func NewEventListener(project string, connection *websocket.Conn, messageTypes []string, location string, noForward bool) *Listener {
	return &Listener{
		project:      project,
		connection:   connection,
		messageTypes: messageTypes,
		location:     location,
		noForward:    noForward,
		active:       make(chan bool, 1),
		id:           uuid.NewRandom().String(),
	}
}

// MessageTypes returns a list of message types the listener will be notified of.
func (e *Listener) MessageTypes() []string {
	return e.messageTypes
}

// IsDone returns true if the listener is done.
func (e *Listener) IsDone() bool {
	return e.done
}

// Connection returns the underlying websocket connection.
func (e *Listener) Connection() *websocket.Conn {
	return e.connection
}

// ID returns the listener ID.
func (e *Listener) ID() string {
	return e.id
}

// Wait waits for a message on its active channel, then returns.
func (e *Listener) Wait() {
	<-e.active
}

// Lock locks the internal mutex.
func (e *Listener) Lock() {
	e.lock.Lock()
}

// Unlock unlocks the internal mutex.
func (e *Listener) Unlock() {
	e.lock.Unlock()
}

// Deactivate deactivates the event listener.
func (e *Listener) Deactivate() {
	e.active <- false
	e.done = true
}

// Handler describes an event handler.
type Handler struct {
}

// NewEventHandler creates and returns a new event handler.
func NewEventHandler() *Handler {
	return &Handler{}
}

// Log sends a new logging event.
func (h Handler) Log(r *log.Record) error {
	Send("", "logging", api.EventLogging{
		Message: r.Msg,
		Level:   r.Lvl.String(),
		Context: logContextMap(r.Ctx)})
	return nil
}

// Init sets the debug and verbose flags.
func Init(d bool, v bool) {
	debug = d
	verbose = v
}

// AddListener adds the given listener to the internal list of listeners which
// are notified when events are broadcasted.
func AddListener(listener *Listener) {
	eventsLock.Lock()
	eventListeners[listener.id] = listener
	eventsLock.Unlock()
}

// SendLifecycle broadcasts a lifecycle event.
func SendLifecycle(project, action, source string,
	context map[string]interface{}) error {
	Send(project, "lifecycle", api.EventLifecycle{
		Action:  action,
		Source:  source,
		Context: context})
	return nil
}

// Send broadcasts a custom event.
func Send(project, eventType string, eventMessage interface{}) error {
	encodedMessage, err := json.Marshal(eventMessage)
	if err != nil {
		return err
	}
	event := api.Event{
		Type:      eventType,
		Timestamp: time.Now(),
		Metadata:  encodedMessage,
	}

	return broadcast(project, event, false)
}

// Forward to the local events dispatcher an event received from another node.
func Forward(id int64, event api.Event) {
	if event.Type == "logging" {
		// Parse the message
		logEntry := api.EventLogging{}
		err := json.Unmarshal(event.Metadata, &logEntry)
		if err != nil {
			return
		}

		if !debug && logEntry.Level == "dbug" {
			return
		}

		if !debug && !verbose && logEntry.Level == "info" {
			return
		}
	}

	err := broadcast("", event, true)
	if err != nil {
		logger.Warnf("Failed to forward event from node %d: %v", id, err)
	}
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

func broadcast(project string, event api.Event, isForward bool) error {
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

		go func(listener *Listener, event api.Event) {
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

			// Set the Location to the expected serverName
			if event.Location == "" {
				eventCopy := api.Event{}
				err := shared.DeepCopy(&event, &eventCopy)
				if err != nil {
					return
				}
				eventCopy.Location = listener.location

				event = eventCopy
			}

			body, err := json.Marshal(event)
			if err != nil {
				return
			}

			err = listener.connection.WriteMessage(websocket.TextMessage, body)
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
		}(listener, event)
	}
	eventsLock.Unlock()

	return nil
}
