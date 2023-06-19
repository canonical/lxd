package qmp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/digitalocean/go-qemu/qmp"

	"github.com/lxc/lxd/shared/logger"
)

var monitors = map[string]*Monitor{}
var monitorsLock sync.Mutex

// RingbufSize is the size of the agent serial ringbuffer in bytes.
var RingbufSize = 16

// EventAgentStarted is the event sent once the lxd-agent has started.
var EventAgentStarted = "LXD-AGENT-STARTED"

// EventVMShutdown is the event sent when VM guest shuts down.
var EventVMShutdown = "SHUTDOWN"

// Monitor represents a QMP monitor.
type Monitor struct {
	path string
	qmp  *qmp.SocketMonitor

	agentStarted   bool
	agentStartedMu sync.Mutex
	disconnected   bool
	chDisconnect   chan struct{}
	eventHandler   func(name string, data map[string]any)
	serialCharDev  string
}

// start handles the background goroutines for event handling and monitoring the ringbuffer.
func (m *Monitor) start() error {
	// Ringbuffer monitoring function.
	checkBuffer := func() {
		// Prepare the response.
		var resp struct {
			Return string `json:"return"`
		}

		// Read the ringbuffer.
		args := map[string]any{
			"device": m.serialCharDev,
			"size":   RingbufSize,
			"format": "utf8",
		}

		err := m.run("ringbuf-read", args, &resp)
		if err != nil {
			return
		}

		// Extract the last entry.
		entries := strings.Split(resp.Return, "\n")
		if len(entries) > 1 {
			status := entries[len(entries)-2]

			m.agentStartedMu.Lock()
			if status == "STARTED" {
				if !m.agentStarted && m.eventHandler != nil {
					go m.eventHandler(EventAgentStarted, nil)
				}

				m.agentStarted = true
			} else if status == "STOPPED" {
				m.agentStarted = false
			}

			m.agentStartedMu.Unlock()
		}
	}

	// Start event monitoring go routine.
	chEvents, err := m.qmp.Events(context.Background())
	if err != nil {
		return err
	}

	go func() {
		logger.Debug("QMP monitor started", logger.Ctx{"path": m.path})
		defer logger.Debug("QMP monitor stopped", logger.Ctx{"path": m.path})

		// Initial read from the ringbuffer.
		go checkBuffer()

		for {
			// Wait for an event, disconnection or timeout.
			select {
			case <-m.chDisconnect:
				return
			case e, more := <-chEvents:
				// Deliver non-empty events to the event handler.
				if m.eventHandler != nil && e.Event != "" {
					go m.eventHandler(e.Event, e.Data)
				}

				// Event channel is closed, lets disconnect.
				if !more {
					m.Disconnect()
					return
				}

				if e.Event == "" {
					logger.Warnf("Unexpected empty event received from qmp event channel")
					time.Sleep(time.Second) // Don't spin if we receive a lot of these.
					continue
				}

				// Check if the ringbuffer was updated (non-blocking).
				go checkBuffer()
			case <-time.After(10 * time.Second):
				// Check if the ringbuffer was updated (non-blocking).
				go checkBuffer()

				continue
			}
		}
	}()

	return nil
}

// ping is used to validate if the QMP socket is still active.
func (m *Monitor) ping() error {
	// Check if disconnected
	if m.disconnected {
		return ErrMonitorDisconnect
	}

	// Query the capabilities to validate the monitor.
	_, err := m.qmp.Run([]byte("{'execute': 'query-version'}"))
	if err != nil {
		m.Disconnect()
		return ErrMonitorDisconnect
	}

	return nil
}

// run executes a command.
func (m *Monitor) run(cmd string, args any, resp any) error {
	// Check if disconnected
	if m.disconnected {
		return ErrMonitorDisconnect
	}

	// Run the command.
	requestArgs := struct {
		Execute   string `json:"execute"`
		Arguments any    `json:"arguments,omitempty"`
	}{
		Execute:   cmd,
		Arguments: args,
	}

	request, err := json.Marshal(requestArgs)
	if err != nil {
		return err
	}

	out, err := m.qmp.Run(request)
	if err != nil {
		// Confirm the daemon didn't die.
		errPing := m.ping()
		if errPing != nil {
			return errPing
		}

		return err
	}

	// Decode the response if needed.
	if resp != nil {
		err = json.Unmarshal(out, &resp)
		if err != nil {
			// Confirm the daemon didn't die.
			errPing := m.ping()
			if errPing != nil {
				return errPing
			}

			return fmt.Errorf("Unexpected monitor response: %w (%q)", err, string(out))
		}
	}

	return nil
}

// Connect creates or retrieves an existing QMP monitor for the path.
func Connect(path string, serialCharDev string, eventHandler func(name string, data map[string]any)) (*Monitor, error) {
	monitorsLock.Lock()
	defer monitorsLock.Unlock()

	// Look for an existing monitor.
	monitor, ok := monitors[path]
	if ok {
		monitor.eventHandler = eventHandler
		return monitor, nil
	}

	// Setup the connection.
	qmpConn, err := qmp.NewSocketMonitor("unix", path, time.Second)
	if err != nil {
		return nil, err
	}

	chError := make(chan error, 1)
	go func() {
		err = qmpConn.Connect()
		chError <- err
	}()

	select {
	case err := <-chError:
		if err != nil {
			return nil, err
		}

	case <-time.After(5 * time.Second):
		_ = qmpConn.Disconnect()
		return nil, fmt.Errorf("QMP connection timed out")
	}

	// Setup the monitor struct.
	monitor = &Monitor{}
	monitor.path = path
	monitor.qmp = qmpConn
	monitor.chDisconnect = make(chan struct{}, 1)
	monitor.eventHandler = eventHandler
	monitor.serialCharDev = serialCharDev

	// Spawn goroutines.
	err = monitor.start()
	if err != nil {
		return nil, err
	}

	// Register in global map.
	monitors[path] = monitor

	return monitor, nil
}

// AgenStarted indicates whether an agent has been detected.
func (m *Monitor) AgenStarted() bool {
	m.agentStartedMu.Lock()
	defer m.agentStartedMu.Unlock()

	return m.agentStarted
}

// Disconnect forces a disconnection from QEMU.
func (m *Monitor) Disconnect() {
	monitorsLock.Lock()
	defer monitorsLock.Unlock()

	// Stop all go routines and disconnect from socket.
	if !m.disconnected {
		close(m.chDisconnect)
		m.disconnected = true
		_ = m.qmp.Disconnect()
	}

	// Remove from the map.
	delete(monitors, m.path)
}

// Wait returns a channel that will be closed on disconnection.
func (m *Monitor) Wait() (chan struct{}, error) {
	// Check if disconnected
	if m.disconnected {
		return nil, ErrMonitorDisconnect
	}

	return m.chDisconnect, nil
}
