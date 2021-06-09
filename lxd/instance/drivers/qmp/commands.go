package qmp

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/shared"
)

// Status returns the current VM status.
func (m *Monitor) Status() (string, error) {
	// Prepare the response.
	var resp struct {
		Return struct {
			Status string `json:"status"`
		} `json:"return"`
	}

	// Query the status.
	err := m.run("query-status", "", &resp)
	if err != nil {
		return "", err
	}

	return resp.Return.Status, nil
}

// Console fetches the File for a particular console.
func (m *Monitor) Console(target string) (*os.File, error) {
	// Prepare the response.
	var resp struct {
		Return []struct {
			Label    string `json:"label"`
			Filename string `json:"filename"`
		} `json:"return"`
	}

	// Query the consoles.
	err := m.run("query-chardev", "", &resp)
	if err != nil {
		return nil, err
	}

	// Look for the requested console.
	for _, v := range resp.Return {
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

// SendFile adds a new file descriptor to the QMP fd table associated to name.
func (m *Monitor) SendFile(name string, file *os.File) error {
	// Check if disconnected
	if m.disconnected {
		return ErrMonitorDisconnect
	}

	// Query the status.
	_, err := m.qmp.RunWithFile([]byte(fmt.Sprintf("{'execute': 'getfd', 'arguments': {'fdname': '%s'}}", name)), file)
	if err != nil {
		// Confirm the daemon didn't die.
		errPing := m.ping()
		if errPing != nil {
			return errPing
		}

		return err
	}

	return nil
}

// Migrate starts a migration stream.
func (m *Monitor) Migrate(uri string) error {
	// Query the status.
	err := m.run("migrate", fmt.Sprintf("{'uri': '%s'}", uri), nil)
	if err != nil {
		return err
	}

	// Wait until it completes or fails.
	for {
		time.Sleep(1 * time.Second)

		// Prepare the response.
		var resp struct {
			Return struct {
				Status string `json:"status"`
			} `json:"return"`
		}

		err := m.run("query-migrate", "", &resp)
		if err != nil {
			return err
		}

		if resp.Return.Status == "failed" {
			return fmt.Errorf("Migration call failed")
		}

		if resp.Return.Status == "completed" {
			break
		}
	}

	return nil
}

// MigrateIncoming starts the receiver of a migration stream.
func (m *Monitor) MigrateIncoming(uri string) error {
	// Query the status.
	err := m.run("migrate-incoming", fmt.Sprintf("{'uri': '%s'}", uri), nil)
	if err != nil {
		return err
	}

	// Wait until it completes or fails.
	for {
		time.Sleep(1 * time.Second)

		// Preapre the response.
		var resp struct {
			Return struct {
				Status string `json:"status"`
			} `json:"return"`
		}

		err := m.run("query-migrate", "", &resp)
		if err != nil {
			return err
		}

		if resp.Return.Status == "failed" {
			return fmt.Errorf("Migration call failed")
		}

		if resp.Return.Status == "completed" {
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
	// Prepare the response.
	var resp struct {
		Return []struct {
			CPU int `json:"cpu-index"`
			PID int `json:"thread-id"`
		} `json:"return"`
	}

	// Query the consoles.
	err := m.run("query-cpus-fast", "", &resp)
	if err != nil {
		return nil, err
	}

	// Make a slice of PIDs.
	pids := []int{}
	for _, cpu := range resp.Return {
		pids = append(pids, cpu.PID)
	}

	return pids, nil
}

// GetMemorySizeBytes returns the current size of the base memory in bytes.
func (m *Monitor) GetMemorySizeBytes() (int64, error) {
	// Prepare the response.
	var resp struct {
		Return struct {
			BaseMemory int64 `json:"base-memory"`
		} `json:"return"`
	}

	err := m.run("query-memory-size-summary", "", &resp)
	if err != nil {
		return -1, err
	}

	return resp.Return.BaseMemory, nil
}

// GetMemoryBalloonSizeBytes returns effective size of the memory in bytes (considering the current balloon size).
func (m *Monitor) GetMemoryBalloonSizeBytes() (int64, error) {
	// Prepare the response.
	var resp struct {
		Return struct {
			Actual int64 `json:"actual"`
		} `json:"return"`
	}

	err := m.run("query-balloon", "", &resp)
	if err != nil {
		return -1, err
	}

	return resp.Return.Actual, nil
}

// SetMemoryBalloonSizeBytes sets the size of the memory in bytes (which will resize the balloon as needed).
func (m *Monitor) SetMemoryBalloonSizeBytes(sizeBytes int64) error {
	return m.run("balloon", fmt.Sprintf("{'value': %d}", sizeBytes), nil)
}

// AddNIC adds a NIC device.
func (m *Monitor) AddNIC(netDev map[string]interface{}, device map[string]string) error {
	revert := revert.New()
	defer revert.Fail()

	if netDev != nil {
		args, err := json.Marshal(netDev)
		if err != nil {
			return err
		}

		err = m.run("netdev_add", string(args), nil)
		if err != nil {
			return errors.Wrapf(err, "Failed adding NIC netdev")
		}

		revert.Add(func() {
			netDevDel := map[string]interface{}{
				"id": netDev["id"],
			}

			args, err := json.Marshal(netDevDel)
			if err != nil {
				return
			}

			err = m.run("netdev_del", string(args), nil)
			if err != nil {
				return
			}
		})
	}

	if device != nil {
		args, err := json.Marshal(device)
		if err != nil {
			return err
		}

		err = m.run("device_add", string(args), nil)
		if err != nil {
			return errors.Wrapf(err, "Failed adding NIC device")
		}
	}

	revert.Success()
	return nil
}

// RemoveNIC removes a NIC device.
func (m *Monitor) RemoveNIC(netDevID string, deviceID string) error {
	if deviceID != "" {
		deviceID := map[string]string{
			"id": deviceID,
		}

		args, err := json.Marshal(deviceID)
		if err != nil {
			return err
		}

		err = m.run("device_del", string(args), nil)
		if err != nil {
			// If the device has already been removed then all good.
			if err != nil && !strings.Contains(err.Error(), "not found") {
				return errors.Wrapf(err, "Failed removing NIC device")
			}
		}
	}

	if netDevID != "" {
		netDevID := map[string]string{
			"id": netDevID,
		}

		args, err := json.Marshal(netDevID)
		if err != nil {
			return err
		}

		err = m.run("netdev_del", string(args), nil)

		// Not all NICs need a netdev, so if its missing, its not a problem.
		if err != nil && !strings.Contains(err.Error(), "not found") {
			return errors.Wrapf(err, "Failed removing NIC netdev")
		}
	}

	return nil
}

// Reset VM.
func (m *Monitor) Reset() error {
	err := m.run("system_reset", "", nil)
	if err != nil {
		return errors.Wrapf(err, "Failed resetting")
	}

	return nil
}

// PCIClassInfo info about a device's class.
type PCIClassInfo struct {
	Class       int    `json:"class"`
	Description string `json:"desc"`
}

// PCIDevice represents a PCI device.
type PCIDevice struct {
	DevID    string       `json:"qdev_id"`
	Bus      int          `json:"bus"`
	Slot     int          `json:"slot"`
	Function int          `json:"function"`
	Devices  []PCIDevice  `json:"devices"`
	Class    PCIClassInfo `json:"class_info"`
	Bridge   PCIBridge    `json:"pci_bridge"`
}

// PCIBridge represents a PCI bridge.
type PCIBridge struct {
	Devices []PCIDevice `json:"devices"`
}

// QueryPCI returns info about PCI devices.
func (m *Monitor) QueryPCI() ([]PCIDevice, error) {
	// Prepare the response.
	var resp struct {
		Return []struct {
			Devices []PCIDevice `json:"devices"`
		} `json:"return"`
	}

	err := m.run("query-pci", "", &resp)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed querying PCI devices")
	}

	return resp.Return[0].Devices, nil
}
