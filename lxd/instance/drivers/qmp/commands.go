package qmp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/shared/api"
)

// FdsetFdInfo contains information about a file descriptor that belongs to an FD set.
type FdsetFdInfo struct {
	FD     int    `json:"fd"`
	Opaque string `json:"opaque"`
}

// FdsetInfo contains information about an FD set.
type FdsetInfo struct {
	ID  int           `json:"fdset-id"`
	FDs []FdsetFdInfo `json:"fds"`
}

// AddFdInfo contains information about a file descriptor that was added to an fd set.
type AddFdInfo struct {
	ID int `json:"fdset-id"`
	FD int `json:"fd"`
}

// CPUInstanceProperties contains CPU instance properties.
type CPUInstanceProperties struct {
	NodeID    int `json:"node-id,omitempty"`
	SocketID  int `json:"socket-id,omitempty"`
	DieID     int `json:"die-id,omitempty"`
	ClusterID int `json:"cluster-id,omitempty"`
	CoreID    int `json:"core-id,omitempty"`
	ThreadID  int `json:"thread-id,omitempty"`
}

// HotpluggableCPU contains information about a hotpluggable CPU.
type HotpluggableCPU struct {
	Type       string                `json:"type"`
	Props      CPUInstanceProperties `json:"props"`
	VCPUsCount int                   `json:"vcpus-count"`
	QOMPath    string                `json:"qom-path,omitempty"`
}

// QueryHotpluggableCPUs returns a list of hotpluggable CPUs.
func (m *Monitor) QueryHotpluggableCPUs() ([]HotpluggableCPU, error) {
	// Check if disconnected
	if m.disconnected {
		return nil, ErrMonitorDisconnect
	}

	// Prepare the response.
	var resp struct {
		Return []HotpluggableCPU `json:"return"`
	}

	err := m.run("query-hotpluggable-cpus", nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed to query hotpluggable CPUs: %w", err)
	}

	return resp.Return, nil
}

// Status returns the current VM status.
func (m *Monitor) Status() (string, error) {
	// Prepare the response.
	var resp struct {
		Return struct {
			Status string `json:"status"`
		} `json:"return"`
	}

	// Query the status.
	err := m.run("query-status", nil, &resp)
	if err != nil {
		return "", err
	}

	return resp.Return.Status, nil
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

// SendFileWithFDSet adds a new file descriptor to an FD set.
func (m *Monitor) SendFileWithFDSet(name string, file *os.File, readonly bool) (AddFdInfo, error) {
	// Prepare the response.
	var resp struct {
		Return AddFdInfo `json:"return"`
	}

	// Check if disconnected
	if m.disconnected {
		return resp.Return, ErrMonitorDisconnect
	}

	permissions := "rdwr"

	if readonly {
		permissions = "rdonly"
	}

	// Query the status.
	ret, err := m.qmp.RunWithFile([]byte(fmt.Sprintf("{'execute': 'add-fd', 'arguments': {'opaque': '%s:%s'}}", permissions, name)), file)
	if err != nil {
		// Confirm the daemon didn't die.
		errPing := m.ping()
		if errPing != nil {
			return resp.Return, errPing
		}

		return resp.Return, err
	}

	err = json.Unmarshal(ret, &resp)
	if err != nil {
		return resp.Return, err
	}

	return resp.Return, nil
}

// RemoveFDFromFDSet removes an FD with the given name from an FD set.
func (m *Monitor) RemoveFDFromFDSet(name string) error {
	// Check if disconnected
	if m.disconnected {
		return ErrMonitorDisconnect
	}

	// Prepare the response.
	var resp struct {
		Return []FdsetInfo `json:"return"`
	}

	err := m.run("query-fdsets", nil, &resp)
	if err != nil {
		return fmt.Errorf("Failed to query fd sets: %w", err)
	}

	for _, fdSet := range resp.Return {
		for _, fd := range fdSet.FDs {
			fields := strings.SplitN(fd.Opaque, ":", 2)
			opaque := ""

			if len(fields) == 2 {
				opaque = fields[1]
			} else {
				opaque = fields[0]
			}

			if opaque == name {
				args := map[string]any{
					"fdset-id": fdSet.ID,
				}

				err = m.run("remove-fd", args, nil)
				if err != nil {
					return fmt.Errorf("Failed to remove fd from fd set: %w", err)
				}
			}
		}
	}

	return nil
}

// Migrate starts a migration stream.
func (m *Monitor) Migrate(uri string) error {
	// Query the status.
	args := map[string]string{"uri": uri}
	err := m.run("migrate", args, nil)
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

		err := m.run("query-migrate", nil, &resp)
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
	args := map[string]string{"uri": uri}
	err := m.run("migrate-incoming", args, nil)
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

		err := m.run("query-migrate", nil, &resp)
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
	return m.run("system_powerdown", nil, nil)
}

// Start tells QEMU to start the emulation.
func (m *Monitor) Start() error {
	return m.run("cont", nil, nil)
}

// Pause tells QEMU to temporarily stop the emulation.
func (m *Monitor) Pause() error {
	return m.run("stop", nil, nil)
}

// Quit tells QEMU to exit immediately.
func (m *Monitor) Quit() error {
	return m.run("quit", nil, nil)
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
	err := m.run("query-cpus-fast", nil, &resp)
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

	err := m.run("query-memory-size-summary", nil, &resp)
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

	err := m.run("query-balloon", nil, &resp)
	if err != nil {
		return -1, err
	}

	return resp.Return.Actual, nil
}

// SetMemoryBalloonSizeBytes sets the size of the memory in bytes (which will resize the balloon as needed).
func (m *Monitor) SetMemoryBalloonSizeBytes(sizeBytes int64) error {
	args := map[string]int64{"value": sizeBytes}
	return m.run("balloon", args, nil)
}

// AddBlockDevice adds a block device.
func (m *Monitor) AddBlockDevice(blockDev map[string]any, device map[string]string) error {
	revert := revert.New()
	defer revert.Fail()

	if blockDev != nil {
		err := m.run("blockdev-add", blockDev, nil)
		if err != nil {
			return fmt.Errorf("Failed adding block device: %w", err)
		}

		revert.Add(func() {
			blockDevDel := map[string]any{
				"node-name": blockDev["devName"],
			}

			_ = m.run("blockdev-del", blockDevDel, nil)
		})
	}

	err := m.AddDevice(device)
	if err != nil {
		return fmt.Errorf("Failed adding device: %w", err)
	}

	revert.Success()
	return nil
}

// RemoveBlockDevice removes a block device.
func (m *Monitor) RemoveBlockDevice(blockDevName string) error {
	if blockDevName != "" {
		blockDevName := map[string]string{
			"node-name": blockDevName,
		}

		// Retry a few times in case the blockdev is in use.
		err := m.run("blockdev-del", blockDevName, nil)
		if err != nil {
			if strings.Contains(err.Error(), "is in use") {
				return api.StatusErrorf(http.StatusLocked, err.Error())
			}

			if strings.Contains(err.Error(), "Failed to find") {
				return nil
			}

			return fmt.Errorf("Failed removing block device: %w", err)
		}
	}

	return nil
}

// AddDevice adds a new device.
func (m *Monitor) AddDevice(device map[string]string) error {
	// Check if disconnected
	if m.disconnected {
		return ErrMonitorDisconnect
	}

	if device != nil {
		err := m.run("device_add", device, nil)
		if err != nil {
			return err
		}
	}

	return nil
}

// RemoveDevice removes a device.
func (m *Monitor) RemoveDevice(deviceID string) error {
	// Check if disconnected
	if m.disconnected {
		return ErrMonitorDisconnect
	}

	if deviceID != "" {
		deviceID := map[string]string{
			"id": deviceID,
		}

		err := m.run("device_del", deviceID, nil)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return nil
			}

			return err
		}
	}

	return nil
}

// AddNIC adds a NIC device.
func (m *Monitor) AddNIC(netDev map[string]any, device map[string]string) error {
	revert := revert.New()
	defer revert.Fail()

	if netDev != nil {
		err := m.run("netdev_add", netDev, nil)
		if err != nil {
			return fmt.Errorf("Failed adding NIC netdev: %w", err)
		}

		revert.Add(func() {
			netDevDel := map[string]any{
				"id": netDev["id"],
			}

			err = m.run("netdev_del", netDevDel, nil)
			if err != nil {
				return
			}
		})
	}

	err := m.AddDevice(device)
	if err != nil {
		return fmt.Errorf("Failed adding NIC device: %w", err)
	}

	revert.Success()
	return nil
}

// RemoveNIC removes a NIC device.
func (m *Monitor) RemoveNIC(netDevID string) error {
	if netDevID != "" {
		netDevID := map[string]string{
			"id": netDevID,
		}

		err := m.run("netdev_del", netDevID, nil)

		// Not all NICs need a netdev, so if its missing, its not a problem.
		if err != nil && !strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("Failed removing NIC netdev: %w", err)
		}
	}

	return nil
}

// Reset VM.
func (m *Monitor) Reset() error {
	err := m.run("system_reset", nil, nil)
	if err != nil {
		return fmt.Errorf("Failed resetting: %w", err)
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

	err := m.run("query-pci", nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed querying PCI devices: %w", err)
	}

	if len(resp.Return) > 0 {
		return resp.Return[0].Devices, nil
	}

	return nil, nil
}

// BlockStats represents block device stats.
type BlockStats struct {
	BytesWritten    int `json:"wr_bytes"`
	WritesCompleted int `json:"wr_operations"`
	BytesRead       int `json:"rd_bytes"`
	ReadsCompleted  int `json:"rd_operations"`
}

// GetBlockStats return block device stats.
func (m *Monitor) GetBlockStats() (map[string]BlockStats, error) {
	// Prepare the response
	var resp struct {
		Return []struct {
			Stats BlockStats `json:"stats"`
			QDev  string     `json:"qdev"`
		} `json:"return"`
	}

	err := m.run("query-blockstats", nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("Failed querying block stats: %w", err)
	}

	out := make(map[string]BlockStats)

	for _, res := range resp.Return {
		out[res.QDev] = res.Stats
	}

	return out, nil
}

// AddSecret adds a secret object with the given ID and secret. This function won't return an error
// if the secret object already exists.
func (m *Monitor) AddSecret(id string, secret string) error {
	args := map[string]any{
		"qom-type": "secret",
		"id":       id,
		"data":     secret,
		"format":   "base64",
	}

	err := m.run("object-add", &args, nil)
	if err != nil && !strings.Contains(err.Error(), "attempt to add duplicate property") {
		return fmt.Errorf("Failed adding object: %w", err)
	}

	return nil
}
