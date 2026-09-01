package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

var _ Connector = &connectorNVMeFC{}

type connectorNVMeFC struct {
	common
}

// Type returns the type of the connector.
func (c *connectorNVMeFC) Type() string {
	return TypeNVMeFC
}

// Transport returns the transport type of the connector.
func (c *connectorNVMeFC) Transport() TransportType {
	return TransportFC
}

// Version returns the version of the NVMe CLI.
func (c *connectorNVMeFC) Version() (string, error) {
	return nvmeVersion()
}

// LoadModules loads the NVMe/FC kernel modules.
func (c *connectorNVMeFC) LoadModules() error {
	err := util.LoadModule("nvme_fabrics")
	if err != nil {
		return err
	}

	return util.LoadModule("nvme_fc")
}

// QualifiedName returns a custom host NQN generated from the server UUID.
func (c *connectorNVMeFC) QualifiedName() (string, error) {
	return nvmeQualifiedName(c.serverUUID), nil
}

// Connect establishes a connection with the target subsystem over the FC fabric.
// The provided target addresses are the subsystem's FC transport addresses ("nn-<wwnn>:pn-<wwpn>").
// Each target port is connected through every local FC HBA that can reach it, establishing a path
// per local HBA for multipathing.
func (c *connectorNVMeFC) Connect(ctx context.Context, targetQN string, targetAddresses ...string) (revert.Hook, error) {
	hostNQN, err := c.QualifiedName()
	if err != nil {
		return nil, err
	}

	hostAddrs, err := nvmeLocalFCTransportAddresses()
	if err != nil {
		return nil, err
	}

	connectFunc := func(ctx context.Context, session *session, targetAddr string) error {
		// Local HBAs that do not have a path to this target port yet.
		missingHostAddrs := nvmeFCMissingPaths(session, targetAddr, hostAddrs)
		if len(missingHostAddrs) == 0 {
			// Every local HBA is already connected to the target port.
			return nil
		}

		// An already established path counts as connected, so that a target port whose
		// remaining HBAs are unreachable does not fail an otherwise healthy connection.
		connected := len(missingHostAddrs) < len(hostAddrs)

		// Attempt to connect the target port through every local HBA that is still
		// missing a path. A port is generally reachable only through the HBAs it is
		// zoned to, so failures on the other HBAs are expected and ignored as long as
		// one path succeeds.
		var lastErr error
		for _, hostAddr := range missingHostAddrs {
			_, err := shared.RunCommand(ctx, "nvme", "connect", "--transport", "fc", "--traddr", targetAddr, "--host-traddr", hostAddr, "--nqn", targetQN, "--hostnqn", hostNQN, "--hostid", c.serverUUID)
			if err != nil {
				lastErr = err
				continue
			}

			connected = true
		}

		if !connected {
			return fmt.Errorf("Failed connecting to target %q on %q via NVMe/FC: %w", targetQN, targetAddr, lastErr)
		}

		return nil
	}

	return connect(ctx, c, targetQN, targetAddresses, connectFunc)
}

// Disconnect terminates a connection with the target.
func (c *connectorNVMeFC) Disconnect(targetQN string) error {
	return nvmeDisconnect(c, targetQN)
}

// findSession returns an active NVMe subsystem that matches the given targetQN.
func (c *connectorNVMeFC) findSession(targetQN string) (*session, error) {
	return nvmeFindSession(targetQN, c.Transport())
}

// Discover returns the NVMe subsystems reachable over the FC fabric.
//
// The provided target addresses are the array's NVMe FC target ports (obtained from the storage array).
// For each target address, the discovery is run against every local FC HBA until one succeeds.
// The array's discovery controller returns the full set of subsystem ports, so a single successful
// discovery per target is sufficient.
func (c *connectorNVMeFC) Discover(ctx context.Context, targetAddresses ...string) ([]any, error) {
	hostNQN, err := c.QualifiedName()
	if err != nil {
		return nil, err
	}

	// Set a deadline for the overall discovery.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	hostAddrs, err := nvmeLocalFCTransportAddresses()
	if err != nil {
		return nil, err
	}

	var discoveryLog nvmeDiscoveryLog
	seen := make(map[string]struct{})

	for _, targetAddr := range targetAddresses {
		for _, hostAddr := range hostAddrs {
			stdout, err := shared.RunCommand(ctx, "nvme", "discover", "--transport", "fc", "--traddr", targetAddr, "--host-traddr", hostAddr, "--hostnqn", hostNQN, "--hostid", c.serverUUID, "--output-format", "json")
			if err != nil {
				// The target is not reachable through this local HBA.
				logger.Warn("Failed connecting to NVMe/FC discovery target", logger.Ctx{"target_address": targetAddr, "host_address": hostAddr, "err": err})
				continue
			}

			// Successful discovery of log entries returns output as JSON object.
			if !strings.HasPrefix(strings.TrimSpace(stdout), "{") {
				logger.Warn("Failed finding discovery log entries", logger.Ctx{"target_address": targetAddr})
				continue
			}

			var log nvmeDiscoveryLog
			err = json.Unmarshal([]byte(stdout), &log)
			if err != nil {
				return nil, fmt.Errorf("Failed unmarshaling the returned discovery log entries from %q: %w", targetAddr, err)
			}

			records := nvmeFilterDiscoveryLog(log.Records, nvmeTransportTypeFC)

			// Accumulate unique records.
			for _, record := range records {
				key := record.SubNQN + "|" + record.TransportAddress
				_, ok := seen[key]
				if ok {
					continue
				}

				seen[key] = struct{}{}
				discoveryLog.Records = append(discoveryLog.Records, record)
			}

			// The discovery controller returned the full log for this target,
			// so there is no need to try the remaining local HBAs.
			break
		}
	}

	if len(discoveryLog.Records) == 0 {
		return nil, errors.New("Failed fetching a discovery log record from any of the FC targets")
	}

	result := make([]any, 0, len(discoveryLog.Records))
	for _, value := range discoveryLog.Records {
		result = append(result, value)
	}

	return result, nil
}

// WaitDiskDevicePath waits for the mapped device to appear and returns its path.
func (c *connectorNVMeFC) WaitDiskDevicePath(ctx context.Context, diskPathFilter block.DevicePathFilterFunc) (string, error) {
	return block.WaitDiskDevicePath(ctx, nvmeDiskDevicePrefix, diskPathFilter)
}

// GetDiskDevicePath returns the path of the mapped device if it exists.
func (c *connectorNVMeFC) GetDiskDevicePath(diskPathFilter block.DevicePathFilterFunc) (string, error) {
	return block.GetDiskDevicePath(nvmeDiskDevicePrefix, diskPathFilter)
}

// RemoveDiskDevice does nothing. Device is removed when volume is unmapped on the storage array.
func (c *connectorNVMeFC) RemoveDiskDevice(ctx context.Context, devicePath string) error {
	return nil
}

// WaitDiskDeviceResize waits until the disk device reflects the new size.
func (c *connectorNVMeFC) WaitDiskDeviceResize(ctx context.Context, diskPath string, newSizeBytes int64) error {
	return block.WaitDiskDeviceResize(ctx, diskPath, newSizeBytes)
}

// nvmeFCMissingPaths returns the local host addresses that do not have an established path
// to the given target address yet (each local HBA forms a separate path).
func nvmeFCMissingPaths(session *session, targetAddr string, hostAddrs []string) []string {
	var connectedHostAddrs []string
	if session != nil {
		// Target addresses are compared case insensitively, as the requested one
		// originates from the storage array while the connected ones are reported
		// by the kernel.
		for addr, targetHostAddrs := range session.hostAddressesByTarget {
			if strings.EqualFold(addr, targetAddr) {
				connectedHostAddrs = append(connectedHostAddrs, targetHostAddrs...)
			}
		}
	}

	missing := make([]string, 0, len(hostAddrs))
	for _, hostAddr := range hostAddrs {
		isConnected := slices.ContainsFunc(connectedHostAddrs, func(addr string) bool {
			// Host addresses are compared case insensitively, as the local ones are
			// assembled from sysfs while the connected ones are reported by the kernel.
			return strings.EqualFold(addr, hostAddr)
		})

		if !isConnected {
			missing = append(missing, hostAddr)
		}
	}

	return missing
}

// nvmeFCTransportAddress builds an NVMe/FC transport address ("nn-<wwnn>:pn-<wwpn>")
// from the FC node name and port name as reported by sysfs (e.g. "0x2000..").
func nvmeFCTransportAddress(nodeName string, portName string) string {
	return "nn-" + strings.TrimSpace(nodeName) + ":pn-" + strings.TrimSpace(portName)
}

// nvmeLocalFCTransportAddresses returns the NVMe/FC transport addresses
// ("nn-<wwnn>:pn-<wwpn>") of the local FC HBA ports that are online. These are
// used as host transport addresses when discovering and connecting to targets.
func nvmeLocalFCTransportAddresses() ([]string, error) {
	fcHostBasePath := "/sys/class/fc_host"

	hosts, err := os.ReadDir(fcHostBasePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errors.New("No FC host adapters found")
		}

		return nil, fmt.Errorf("Failed reading FC hosts: %w", err)
	}

	var addresses []string
	for _, host := range hosts {
		hostPath := filepath.Join(fcHostBasePath, host.Name())

		// Skip ports that are not online.
		stateBytes, err := os.ReadFile(filepath.Join(hostPath, "port_state"))
		if err != nil || strings.TrimSpace(string(stateBytes)) != "Online" {
			continue
		}

		nodeName, err := os.ReadFile(filepath.Join(hostPath, "node_name"))
		if err != nil {
			return nil, err
		}

		portName, err := os.ReadFile(filepath.Join(hostPath, "port_name"))
		if err != nil {
			return nil, err
		}

		address := nvmeFCTransportAddress(string(nodeName), string(portName))
		addresses = append(addresses, address)
	}

	if len(addresses) == 0 {
		return nil, errors.New("No online local FC HBA ports found")
	}

	return addresses, nil
}
