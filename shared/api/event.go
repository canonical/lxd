package api

import (
	"encoding/json"
	"time"
)

// Event represents an event entry (over websocket)
//
// swagger:model
type Event struct {
	// Event type (one of operation, logging or lifecycle)
	// Example: lifecycle
	Type string `yaml:"type" json:"type"`

	// Time at which the event was sent
	// Example: 2021-02-24T19:00:45.452649098-05:00
	Timestamp time.Time `yaml:"timestamp" json:"timestamp"`

	// JSON encoded metadata (see EventLogging, EventLifecycle or Operation)
	// Example: {"action": "instance-started", "source": "/1.0/instances/c1", "context": {}}
	Metadata json.RawMessage `yaml:"metadata" json:"metadata"`

	// Originating cluster member
	// Example: lxd01
	//
	// API extension: event_location
	Location string `yaml:"location,omitempty" json:"location,omitempty"`
}

// EventLogging represents a logging type event entry (admin only)
type EventLogging struct {
	Message string            `yaml:"message" json:"message"`
	Level   string            `yaml:"level" json:"level"`
	Context map[string]string `yaml:"context" json:"context"`
}

// EventLifecycle represets a lifecycle type event entry
//
// API extension: event_lifecycle
type EventLifecycle struct {
	Action  string                 `yaml:"action" json:"action"`
	Source  string                 `yaml:"source" json:"source"`
	Context map[string]interface{} `yaml:"context,omitempty" json:"context,omitempty"`
}
