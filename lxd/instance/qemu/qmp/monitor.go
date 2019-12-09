package qmp

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/digitalocean/go-qemu/qmp"

	"github.com/lxc/lxd/shared"
)

var monitors = map[string]*Monitor{}
var monitorsLock sync.Mutex

// RingbufSize is the size of the agent serial ringbuffer in bytes
var RingbufSize = 16

// Monitor represents a QMP monitor.
type Monitor struct {
	path string
	qmp  *qmp.SocketMonitor

	agentReady   bool
	disconnected bool
	chDisconnect chan struct{}
	eventHandler func(name string, data map[string]interface{})
}

// Connect creates or retrieves an existing QMP monitor for the path.
func Connect(path string, eventHandler func(name string, data map[string]interface{})) (*Monitor, error) {
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
	// Start ringbuffer monitoring go routine.
	go func() {
		for {
			// Read the ringbuffer.
			resp, err := m.qmp.Run([]byte(fmt.Sprintf(`{"execute": "ringbuf-read", "arguments": {"device": "vserial", "size": %d, "format": "utf8"}}`, RingbufSize)))
			if err != nil {
				m.Disconnect()
				return
			}

			// Decode the response.
			var respDecoded struct {
				Return string `json:"return"`
			}

			err = json.Unmarshal(resp, &respDecoded)
			if err != nil {
				continue
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

			// Wait until next read or cancel.
			select {
			case <-m.chDisconnect:
				return
			case <-time.After(10 * time.Second):
				continue
			}
		}
	}()

	// Start event monitoring go routine.
	chEvents, err := m.qmp.Events()
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case <-m.chDisconnect:
				return
			case e := <-chEvents:
				if e.Event == "" {
					continue
				}

				if m.eventHandler != nil {
					m.eventHandler(e.Event, e.Data)
				}
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
	// Stop all go routines and disconnect from socket.
	close(m.chDisconnect)
	m.disconnected = true
	m.qmp.Disconnect()

	// Remove from the map.
	monitorsLock.Lock()
	defer monitorsLock.Unlock()
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
			ptsPath := strings.TrimPrefix(v.Filename, "pty:")

			if !shared.PathExists(ptsPath) {
				continue
			}

			// Open the PTS device
			console, err := os.OpenFile(ptsPath, os.O_RDWR, 0600)
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
