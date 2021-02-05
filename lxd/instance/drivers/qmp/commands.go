package qmp

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lxc/lxd/shared"
)

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

// SendFile adds a new file to the QMP fd table.
func (m *Monitor) SendFile(name string, file *os.File) error {
	// Check if disconnected
	if m.disconnected {
		return ErrMonitorDisconnect
	}

	// Query the status.
	_, err := m.qmp.RunWithFile([]byte(fmt.Sprintf("{'execute': 'getfd', 'arguments': {'fdname': '%s'}}", name)), file)
	if err != nil {
		return err
	}

	return nil
}

// Migrate starts a migration stream.
func (m *Monitor) Migrate(uri string) error {
	// Check if disconnected
	if m.disconnected {
		return ErrMonitorDisconnect
	}

	// Query the status.
	_, err := m.qmp.Run([]byte(fmt.Sprintf("{'execute': 'migrate', 'arguments': {'uri': '%s'}}", uri)))
	if err != nil {
		return err
	}

	// Wait until it completes or fails.
	for {
		time.Sleep(1 * time.Second)

		respRaw, err := m.qmp.Run([]byte("{'execute': 'query-migrate'}"))
		if err != nil {
			return err
		}

		// Process the response.
		var respDecoded struct {
			Return struct {
				Status string `json:"status"`
			} `json:"return"`
		}

		err = json.Unmarshal(respRaw, &respDecoded)
		if err != nil {
			return ErrMonitorBadReturn
		}

		if respDecoded.Return.Status == "failed" {
			return fmt.Errorf("Migration call failed")
		}

		if respDecoded.Return.Status == "completed" {
			break
		}
	}

	return nil
}

// MigrateIncoming starts the receiver of a migration stream.
func (m *Monitor) MigrateIncoming(uri string) error {
	// Check if disconnected
	if m.disconnected {
		return ErrMonitorDisconnect
	}

	// Query the status.
	_, err := m.qmp.Run([]byte(fmt.Sprintf("{'execute': 'migrate-incoming', 'arguments': {'uri': '%s'}}", uri)))
	if err != nil {
		return err
	}

	// Wait until it completes or fails.
	for {
		time.Sleep(1 * time.Second)

		respRaw, err := m.qmp.Run([]byte("{'execute': 'query-migrate'}"))
		if err != nil {
			return err
		}

		// Process the response.
		var respDecoded struct {
			Return struct {
				Status string `json:"status"`
			} `json:"return"`
		}

		err = json.Unmarshal(respRaw, &respDecoded)
		if err != nil {
			return ErrMonitorBadReturn
		}

		if respDecoded.Return.Status == "failed" {
			return fmt.Errorf("Migration call failed")
		}

		if respDecoded.Return.Status == "completed" {
			break
		}
	}

	return nil
}

// Powerdown tells the VM to gracefully shutdown.
func (m *Monitor) Powerdown() error {
	return m.run("system_powerdown", "", nil)
}

// Start tells QEMU to start the emulation.
func (m *Monitor) Start() error {
	return m.run("cont", "", nil)
}

// Pause tells QEMU to temporarily stop the emulation.
func (m *Monitor) Pause() error {
	return m.run("stop", "", nil)
}

// Quit tells QEMU to exit immediately.
func (m *Monitor) Quit() error {
	return m.run("quit", "", nil)
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
