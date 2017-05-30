package lxd

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared/api"
)

// The Operation type represents an ongoing LXD operation (asynchronous processing)
type Operation struct {
	api.Operation

	r            *ProtocolLXD
	listener     *EventListener
	listenerLock sync.Mutex
	chActive     chan bool
}

// AddHandler adds a function to be called whenever an event is received
func (op *Operation) AddHandler(function func(api.Operation)) (*EventTarget, error) {
	// Make sure we have a listener setup
	err := op.setupListener()
	if err != nil {
		return nil, err
	}

	// Make sure we're not racing with ourselves
	op.listenerLock.Lock()
	defer op.listenerLock.Unlock()

	// If we're done already, just return
	if op.StatusCode.IsFinal() {
		return nil, nil
	}

	// Wrap the function to filter unwanted messages
	wrapped := func(data interface{}) {
		newOp := op.extractOperation(data)
		if newOp == nil {
			return
		}

		function(*newOp)
	}

	return op.listener.AddHandler([]string{"operation"}, wrapped)
}

// Cancel will request that LXD cancels the operation (if supported)
func (op *Operation) Cancel() error {
	return op.r.DeleteOperation(op.ID)
}

// GetWebsocket returns a raw websocket connection from the operation
func (op *Operation) GetWebsocket(secret string) (*websocket.Conn, error) {
	return op.r.GetOperationWebsocket(op.ID, secret)
}

// RemoveHandler removes a function to be called whenever an event is received
func (op *Operation) RemoveHandler(target *EventTarget) error {
	// Make sure we're not racing with ourselves
	op.listenerLock.Lock()
	defer op.listenerLock.Unlock()

	// If the listener is gone, just return
	if op.listener == nil {
		return nil
	}

	return op.listener.RemoveHandler(target)
}

// Refresh pulls the current version of the operation and updates the struct
func (op *Operation) Refresh() error {
	// Don't bother with a manual update if we are listening for events
	if op.listener != nil {
		return nil
	}

	// Get the current version of the operation
	newOp, _, err := op.r.GetOperation(op.ID)
	if err != nil {
		return err
	}

	// Update the operation struct
	op.Operation = *newOp

	return nil
}

// Wait lets you wait until the operation reaches a final state
func (op *Operation) Wait() error {
	// Check if not done already
	if op.StatusCode.IsFinal() {
		if op.Err != "" {
			return fmt.Errorf(op.Err)
		}

		return nil
	}

	// Make sure we have a listener setup
	err := op.setupListener()
	if err != nil {
		return err
	}

	<-op.chActive

	// We're done, parse the result
	if op.Err != "" {
		return fmt.Errorf(op.Err)
	}

	return nil
}

func (op *Operation) setupListener() error {
	// Make sure we're not racing with ourselves
	op.listenerLock.Lock()
	defer op.listenerLock.Unlock()

	// We already have a listener setup
	if op.listener != nil {
		return nil
	}

	// Get a new listener
	listener, err := op.r.GetEvents()
	if err != nil {
		return err
	}

	// Setup the handler
	chReady := make(chan bool)
	_, err = listener.AddHandler([]string{"operation"}, func(data interface{}) {
		<-chReady

		// Get an operation struct out of this data
		newOp := op.extractOperation(data)
		if newOp == nil {
			return
		}

		// We don't want concurrency while processing events
		op.listenerLock.Lock()
		defer op.listenerLock.Unlock()

		// Check if we're done already (because of another event)
		if op.listener == nil {
			return
		}

		// Update the struct
		op.Operation = *newOp

		// And check if we're done
		if op.StatusCode.IsFinal() {
			op.listener.Disconnect()
			op.listener = nil
			close(op.chActive)
			return
		}
	})
	if err != nil {
		listener.Disconnect()
		return err
	}

	// And do a manual refresh to avoid races
	err = op.Refresh()
	if err != nil {
		listener.Disconnect()
		return err
	}

	// Check if not done already
	if op.StatusCode.IsFinal() {
		listener.Disconnect()
		close(op.chActive)

		if op.Err != "" {
			return fmt.Errorf(op.Err)
		}

		return nil
	}

	// Start processing background updates
	op.listener = listener
	close(chReady)

	return nil
}

func (op *Operation) extractOperation(data interface{}) *api.Operation {
	// Extract the metadata
	meta, ok := data.(map[string]interface{})["metadata"]
	if !ok {
		return nil
	}

	// And attempt to decode it as JSON operation data
	encoded, err := json.Marshal(meta)
	if err != nil {
		return nil
	}

	newOp := api.Operation{}
	err = json.Unmarshal(encoded, &newOp)
	if err != nil {
		return nil
	}

	// And now check that it's what we want
	if newOp.ID != op.ID {
		return nil
	}

	return &newOp
}

// The RemoteOperation type represents an ongoing LXD operation between two servers
type RemoteOperation struct {
	targetOp *Operation

	handlers []func(api.Operation)

	chDone chan bool
	chPost chan bool
	err    error
}

// AddHandler adds a function to be called whenever an event is received
func (op *RemoteOperation) AddHandler(function func(api.Operation)) (*EventTarget, error) {
	var err error
	var target *EventTarget

	// Attach to the existing target operation
	if op.targetOp != nil {
		target, err = op.targetOp.AddHandler(function)
		if err != nil {
			return nil, err
		}
	} else {
		// Generate a mock EventTarget
		target = &EventTarget{
			function: func(interface{}) { function(api.Operation{}) },
			types:    []string{"operation"},
		}
	}

	// Add the handler to our list
	op.handlers = append(op.handlers, function)

	return target, nil
}

// GetTarget returns the target operation
func (op *RemoteOperation) GetTarget() (*api.Operation, error) {
	if op.targetOp == nil {
		return nil, fmt.Errorf("No associated target operation")
	}

	return &op.targetOp.Operation, nil
}

// Wait lets you wait until the operation reaches a final state
func (op *RemoteOperation) Wait() error {
	<-op.chDone

	if op.chPost != nil {
		<-op.chPost
	}

	return op.err
}
