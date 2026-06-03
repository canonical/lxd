package loki

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/canonical/lxd/shared/api"
)

// newBenchClient returns a Client with a buffered entries channel, used by benchmarks to avoid
// blocking on the send at the end of HandleEvent without starting the background run() goroutine.
func newBenchClient(eventTypes []string) *Client {
	return &Client{
		cfg: config{
			logLevel: "debug",
			types:    eventTypes,
			instance: "lxd01",
			location: "cluster-member-01",
		},
		entries: make(chan entry, 128),
	}
}

// BenchmarkHandleEventLifecycle benchmarks the lifecycle event path of HandleEvent,
// which builds a log line from a context map containing requestor and action fields.
func BenchmarkHandleEventLifecycle(b *testing.B) {
	lifecycle := api.EventLifecycle{
		Action:  "instance-started",
		Source:  "/1.0/instances/my-instance",
		Name:    "my-instance",
		Project: "default",
		Requestor: &api.EventLifecycleRequestor{
			Username: "admin",
			Protocol: "tls",
			Address:  "10.0.0.1",
		},
		Context: map[string]any{
			"architecture": "x86_64",
			"type":         "container",
		},
	}

	metadata, err := json.Marshal(lifecycle)
	if err != nil {
		b.Fatal(err)
	}

	event := api.Event{
		Type:      api.EventTypeLifecycle,
		Timestamp: time.Now(),
		Metadata:  metadata,
	}

	c := newBenchClient([]string{api.EventTypeLifecycle})

	// Drain the entries channel so HandleEvent never blocks.
	go func() {
		for range c.entries { //nolint:revive
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		c.HandleEvent(event)
	}
}

// BenchmarkHandleEventLogging benchmarks the logging event path of HandleEvent,
// which builds a sorted-key log line from a context map and appends the log message.
func BenchmarkHandleEventLogging(b *testing.B) {
	logEvent := api.EventLogging{
		Message: "instance started successfully",
		Level:   "info",
		Context: map[string]string{
			"instance": "my-instance",
			"project":  "default",
			"driver":   "lxc",
			"action":   "start",
		},
	}

	metadata, err := json.Marshal(logEvent)
	if err != nil {
		b.Fatal(err)
	}

	event := api.Event{
		Type:      api.EventTypeLogging,
		Timestamp: time.Now(),
		Metadata:  metadata,
	}

	c := newBenchClient([]string{api.EventTypeLogging})

	// Drain the entries channel so HandleEvent never blocks.
	go func() {
		for range c.entries { //nolint:revive
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		c.HandleEvent(event)
	}
}
