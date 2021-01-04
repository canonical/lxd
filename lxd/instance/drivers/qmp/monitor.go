package qmp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/digitalocean/go-qemu/qmp"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

var monitors = map[string]*Monitor{}
var monitorsLock sync.Mutex

// RingbufSize is the size of the agent serial ringbuffer in bytes
var RingbufSize = 16

// Monitor represents a QMP monitor.
type Monitor struct {
	path string
	qmp  *qmp.SocketMonitor

	agentReady    bool
	disconnected  bool
	chDisconnect  chan struct{}
	eventHandler  func(name string, data map[string]interface{})
	serialCharDev string
}

// Connect creates or retrieves an existing QMP monitor for the path.
func Connect(path string, serialCharDev string, eventHandler func(name string, data map[string]interface{})) (*Monitor, error) {
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

	err = qmpConn.Connect()
	if err != nil {
		return nil, err
	}

	// Setup the monitor struct.
	monitor = &Monitor{}
	monitor.path = path
	monitor.qmp = qmpConn
	monitor.chDisconnect = make(chan struct{}, 1)
	monitor.eventHandler = eventHandler
	monitor.serialCharDev = serialCharDev

	// Spawn goroutines.
	err = monitor.run()
	if err != nil {
		return nil, err
	}

	// Register in global map.
	monitors[path] = monitor

	return monitor, nil
}

func (m *Monitor) run() error {
	// Ringbuffer monitoring function.
	checkBuffer := func() {
		// Read the ringbuffer.
		resp, err := m.qmp.Run([]byte(fmt.Sprintf(`{"execute": "ringbuf-read", "arguments": {"device": "%s", "size": %d, "format": "utf8"}}`, m.serialCharDev, RingbufSize)))
		if err != nil {
			// Failure to send a command, assume disconnected/crashed.
			m.Disconnect()
			return
		}

		// Decode the response.
		var respDecoded struct {
			Return string `json:"return"`
		}

		err = json.Unmarshal(resp, &respDecoded)
		if err != nil {
			// Received bad data, assume disconnected/crashed.
			m.Disconnect()
			return
		}

		// Extract the last entry.
		entries := strings.Split(respDecoded.Return, "\n")
		if len(entries) > 1 {
			status := entries[len(entries)-2]

			if status == "STARTED" {
				m.agentReady = true
			} else if status == "STOPPED" {
				m.agentReady = false
			}
		}
	}

	// Start event monitoring go routine.
	chEvents, err := m.qmp.Events(context.Background())
	if err != nil {
		return err
	}

	go func() {
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

// Wait returns a channel that will be closed on disconnection.
func (m *Monitor) Wait() (chan struct{}, error) {
	// Check if disconnected
	if m.disconnected {
		return nil, ErrMonitorDisconnect
	}

	return m.chDisconnect, nil
}

// Disconnect forces a disconnection from QEMU.
func (m *Monitor) Disconnect() {
	monitorsLock.Lock()
	defer monitorsLock.Unlock()

	// Stop all go routines and disconnect from socket.
	if !m.disconnected {
		close(m.chDisconnect)
		m.disconnected = true
		m.qmp.Disconnect()
	}

	// Remove from the map.
	delete(monitors, m.path)
}

// Status returns the current VM status.
func (m *Monitor) Status() (string, error) {
	// Check if disconnected
	if m.disconnected {
		return "", ErrMonitorDisconnect
	}

	// Query the status.
	respRaw, err := m.qmp.Run([]byte("{'execute': 'query-status'}"))
	if err != nil {
		m.Disconnect()
		return "", ErrMonitorDisconnect
	}

	// Process the response.
	var respDecoded struct {
		Return struct {
			Status string `json:"status"`
		} `json:"return"`
	}

	err = json.Unmarshal(respRaw, &respDecoded)
	if err != nil {
		return "", ErrMonitorBadReturn
	}

	return respDecoded.Return.Status, nil
}

// Console fetches the File for a particular console.
func (m *Monitor) Console(target string) (*os.File, error) {
	// Check if disconnected
	if m.disconnected {
		return nil, ErrMonitorDisconnect
	}

	// Query the consoles.
	respRaw, err := m.qmp.Run([]byte("{'execute': 'query-chardev'}"))
	if err != nil {
		m.Disconnect()
		return nil, ErrMonitorDisconnect
	}

	// Process the response.
	var respDecoded struct {
		Return []struct {
			Label    string `json:"label"`
			Filename string `json:"filename"`
		} `json:"return"`
	}

	err = json.Unmarshal(respRaw, &respDecoded)
	if err != nil {
		return nil, ErrMonitorBadReturn
	}

	// Look for the requested console.
	for _, v := range respDecoded.Return {
		if v.Label == target {
			ptyPath := strings.TrimPrefix(v.Filename, "pty:")

			if !shared.PathExists(ptyPath) {
				continue
			}

			// Open the PTS device
			console, err := os.OpenFile(ptyPath, os.O_RDWR, 0600)
			if err != nil {
				return nil, err
			}

			return console, nil
		}
	}

	return nil, ErrMonitorBadConsole
}

func (m *Monitor) runCmd(cmd string) error {
	// Check if disconnected
	if m.disconnected {
		return ErrMonitorDisconnect
	}

	// Query the status.
	_, err := m.qmp.Run([]byte(fmt.Sprintf("{'execute': '%s'}", cmd)))
	if err != nil {
		m.Disconnect()
		return ErrMonitorDisconnect
	}

	return nil
}

// Powerdown tells the VM to gracefully shutdown.
func (m *Monitor) Powerdown() error {
	return m.runCmd("system_powerdown")
}

// Start tells QEMU to start the emulation.
func (m *Monitor) Start() error {
	return m.runCmd("cont")
}

// Pause tells QEMU to temporarily stop the emulation.
func (m *Monitor) Pause() error {
	return m.runCmd("stop")
}

// Quit tells QEMU to exit immediately.
func (m *Monitor) Quit() error {
	return m.runCmd("quit")
}

// AgentReady indicates whether an agent has been detected.
func (m *Monitor) AgentReady() bool {
	return m.agentReady
}

// GetCPUs fetches the vCPU information for pinning.
func (m *Monitor) GetCPUs() ([]int, error) {
	// Check if disconnected
	if m.disconnected {
		return nil, ErrMonitorDisconnect
	}

	// Query the consoles.
	respRaw, err := m.qmp.Run([]byte("{'execute': 'query-cpus'}"))
	if err != nil {
		m.Disconnect()
		return nil, ErrMonitorDisconnect
	}

	// Process the response.
	var respDecoded struct {
		Return []struct {
			CPU int `json:"CPU"`
			PID int `json:"thread_id"`
		} `json:"return"`
	}

	err = json.Unmarshal(respRaw, &respDecoded)
	if err != nil {
		return nil, ErrMonitorBadReturn
	}

	// Make a slice of PIDs.
	pids := []int{}
	for _, cpu := range respDecoded.Return {
		pids = append(pids, cpu.PID)
	}

	return pids, nil
}

// GetMemorySizeBytes returns the current size of the base memory in bytes.
func (m *Monitor) GetMemorySizeBytes() (int64, error) {
	respRaw, err := m.qmp.Run([]byte("{'execute': 'query-memory-size-summary'}"))
	if err != nil {
		m.Disconnect()
		return -1, ErrMonitorDisconnect
	}

	// Process the response.
	var respDecoded struct {
		Return struct {
			BaseMemory int64 `json:"base-memory"`
		} `json:"return"`
	}

	err = json.Unmarshal(respRaw, &respDecoded)
	if err != nil {
		return -1, ErrMonitorBadReturn
	}

	return respDecoded.Return.BaseMemory, nil
}

// GetMemoryBalloonSizeBytes returns effective size of the memory in bytes (considering the current balloon size).
func (m *Monitor) GetMemoryBalloonSizeBytes() (int64, error) {
	respRaw, err := m.qmp.Run([]byte("{'execute': 'query-balloon'}"))
	if err != nil {
		m.Disconnect()
		return -1, ErrMonitorDisconnect
	}

	// Process the response.
	var respDecoded struct {
		Return struct {
			Actual int64 `json:"actual"`
		} `json:"return"`
	}

	err = json.Unmarshal(respRaw, &respDecoded)
	if err != nil {
		return -1, ErrMonitorBadReturn
	}

	return respDecoded.Return.Actual, nil
}

// SetMemoryBalloonSizeBytes sets the size of the memory in bytes (which will resize the balloon as needed).
func (m *Monitor) SetMemoryBalloonSizeBytes(sizeBytes int64) error {
	respRaw, err := m.qmp.Run([]byte(fmt.Sprintf("{'execute': 'balloon', 'arguments': {'value': %d}}", sizeBytes)))
	if err != nil {
		m.Disconnect()
		return ErrMonitorDisconnect
	}

	if string(respRaw) != `{"return": {}}` {
		return ErrMonitorBadReturn
	}

	return nil
}
