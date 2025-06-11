package api

import (
	"encoding/json"
	"fmt"
	"time"
)

// LXD event types.
const (
	EventTypeLifecycle = "lifecycle"
	EventTypeLogging   = "logging"
	EventTypeOperation = "operation"
	EventTypeOVN       = "ovn"
)

// Event represents an event entry (over websocket)
//
// swagger:model
type Event struct {
	// Event type (one of operation, logging or lifecycle)
	// Example: lifecycle
	Type string `json:"type" yaml:"type"`

	// Time at which the event was sent
	// Example: 2021-02-24T19:00:45.452649098-05:00
	Timestamp time.Time `json:"timestamp" yaml:"timestamp"`

	// JSON encoded metadata (see EventLogging, EventLifecycle or Operation)
	// Example: {"action": "instance-started", "source": "/1.0/instances/c1", "context": {}}
	Metadata json.RawMessage `json:"metadata" yaml:"metadata"`

	// Originating cluster member
	// Example: lxd01
	//
	// API extension: event_location
	Location string `json:"location,omitempty" yaml:"location,omitempty"`

	// Project the event belongs to.
	// Example: default
	//
	// API extension: event_project
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
}

// ToLogging creates log record for the event.
func (event *Event) ToLogging() (EventLogRecord, error) {
	switch event.Type {
	case EventTypeLogging, EventTypeOVN:
		e := &EventLogging{}
		err := json.Unmarshal(event.Metadata, &e)
		if err != nil {
			return EventLogRecord{}, err
		}

		ctx := []any{}
		for k, v := range e.Context {
			ctx = append(ctx, k)
			ctx = append(ctx, v)
		}

		record := EventLogRecord{
			Time: event.Timestamp,
			Lvl:  e.Level,
			Msg:  e.Message,
			Ctx:  ctx,
		}

		return record, nil
	case EventTypeLifecycle:
		e := &EventLifecycle{}
		err := json.Unmarshal(event.Metadata, &e)
		if err != nil {
			return EventLogRecord{}, err
		}

		ctx := []any{}
		for k, v := range e.Context {
			ctx = append(ctx, k)
			ctx = append(ctx, v)
		}

		record := EventLogRecord{
			Time: event.Timestamp,
			Lvl:  "info",
			Ctx:  ctx,
		}

		if e.Requestor != nil {
			requestor := e.Requestor.Protocol + "/" + e.Requestor.Username + " (" + e.Requestor.Address + ")"
			record.Msg = "Action: " + e.Action + ", Source: " + e.Source + ", Requestor: " + requestor
		} else {
			record.Msg = "Action: " + e.Action + ", Source: " + e.Source
		}

		return record, nil
	case EventTypeOperation:
		e := &Operation{}
		err := json.Unmarshal(event.Metadata, &e)
		if err != nil {
			return EventLogRecord{}, err
		}

		record := EventLogRecord{
			Time: event.Timestamp,
			Lvl:  "info",
			Msg:  "ID: " + e.ID + ", Class: " + e.Class + ", Description: " + e.Description,
			Ctx: []any{
				"CreatedAt", e.CreatedAt,
				"UpdatedAt", e.UpdatedAt,
				"Status", e.Status,
				"StatusCode", e.StatusCode,
				"Resources", e.Resources,
				"Metadata", e.Metadata,
				"MayCancel", e.MayCancel,
				"Err", e.Err,
				"Location", e.Location,
			},
		}

		return record, nil
	}

	return EventLogRecord{}, fmt.Errorf("Not supported event type: %s", event.Type)
}

// EventLogRecord represents single log record.
type EventLogRecord struct {
	Time time.Time
	Lvl  string
	Msg  string
	Ctx  []any
}

// EventLogging represents a logging type event entry (admin only).
type EventLogging struct {
	Message string            `json:"message" yaml:"message"`
	Level   string            `json:"level"   yaml:"level"`
	Context map[string]string `json:"context" yaml:"context"`
}

// EventLifecycle represets a lifecycle type event entry
//
// API extension: event_lifecycle.
type EventLifecycle struct {
	Action  string         `json:"action"            yaml:"action"`
	Source  string         `json:"source"            yaml:"source"`
	Context map[string]any `json:"context,omitempty" yaml:"context,omitempty"`

	// API extension: event_lifecycle_requestor
	Requestor *EventLifecycleRequestor `json:"requestor,omitempty" yaml:"requestor,omitempty"`

	// API extension: event_lifecycle_name_and_project
	Name    string `json:"name,omitempty"    yaml:"name,omitempty"`
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
}

// EventLifecycleRequestor represents the initial requestor for an event
//
// API extension: event_lifecycle_requestor.
type EventLifecycleRequestor struct {
	Username string `json:"username" yaml:"username"`
	Protocol string `json:"protocol" yaml:"protocol"`

	// Requestor address
	// Example: 10.0.2.15
	//
	// API extension: event_lifecycle_requestor_address
	Address string `json:"address" yaml:"address"`
}
