package connectors

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/revert"
)

var _ Connector = &connectorSCSIFC{}

type connectorSCSIFC struct {
	common
}

// FCDiscoveryRecord represents an FC target port found on the fabric.
type FCDiscoveryRecord struct {
	PortName string // Target WWPN (for example "2100001b32abcdef").
}

// Type returns the type of the connector.
func (c *connectorSCSIFC) Type() string {
	return TypeSCSIFC
}

// Version returns a non-empty string if FC host adapters are present on the system, error otherwise.
func (c *connectorSCSIFC) Version() (string, error) {
	entries, err := os.ReadDir("/sys/class/fc_host")
	if err != nil {
		return "", fmt.Errorf("No FC host adapters found: %w", err)
	}

	if len(entries) == 0 {
		return "", errors.New("No FC host adapters found")
	}

	return "detected (" + TypeSCSIFC + ")", nil
}

// LoadModules loads the FC SCSI transport kernel module.
func (c *connectorSCSIFC) LoadModules() error {
	return util.LoadModule("scsi_transport_fc")
}

// QualifiedName returns the World Wide Port Name (WWPN) of the first FC host initiator.
func (c *connectorSCSIFC) QualifiedName() (string, error) {
	fcHostPath := "/sys/class/fc_host"

	hosts, err := os.ReadDir(fcHostPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("No FC hosts found: Directory %q does not exist", fcHostPath)
		}

		return "", fmt.Errorf("Failed reading FC hosts: %w", err)
	}

	for _, host := range hosts {
		portNameBytes, err := os.ReadFile(filepath.Join(fcHostPath, host.Name(), "port_name"))
		if err != nil {
			continue
		}

		wwpn := normalizeWWPN(string(portNameBytes))
		return wwpn, nil
	}

	return "", errors.New("No FC host initiators found")
}

func (c *connectorSCSIFC) Connect(ctx context.Context, WWPN string, luns ...string) (revert.Hook, error) {
	return nil, nil
}

// Disconnect is a no-op for FC.
func (c *connectorSCSIFC) Disconnect(targetQN string) error {
	return nil
}

// findSession returns nil as FC doesn't have sessions.
func (c *connectorSCSIFC) findSession(targetQN string) (*session, error) {
	return nil, nil
}

// Discover returns the FC target ports visible on the fabric.
// If WWPNs are provided they act as an allowlist.
func (c *connectorSCSIFC) Discover(ctx context.Context, wwpns ...string) ([]any, error) {
	rportBasePath := "/sys/class/fc_remote_ports"

	rports, err := os.ReadDir(rportBasePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errors.New("No FC remote ports found")
		}

		return nil, fmt.Errorf("Failed reading FC remote ports: %w", err)
	}

	result := make([]any, 0, len(rports))
	for _, rport := range rports {
		portNameBytes, err := os.ReadFile(filepath.Join(rportBasePath, rport.Name(), "port_name"))
		if err != nil {
			continue
		}

		portName := normalizeWWPN(string(portNameBytes))

		if len(wwpns) > 0 {
			found := false
			for _, wwpn := range wwpns {
				if strings.EqualFold(portName, normalizeWWPN(wwpn)) {
					found = true
					break
				}
			}

			if !found {
				continue
			}
		}

		stateBytes, err := os.ReadFile(filepath.Join(rportBasePath, rport.Name(), "port_state"))
		if err != nil {
			continue
		}

		state := strings.TrimSpace(string(stateBytes))
		if state != "Online" {
			// Skip offline or blocked ports, as they are not usable.
			continue
		}

		record := FCDiscoveryRecord{
			PortName: normalizeWWPN(portName),
		}

		result = append(result, record)
	}

	if len(result) == 0 {
		return nil, errors.New("No SCSI/FC targets found on the fabric")
	}

	return result, nil
}

func (c *connectorSCSIFC) WaitDiskDevicePath(ctx context.Context, diskPathFilter block.DevicePathFilterFunc) (string, error) {
	return "", nil
}

// GetDiskDevicePath returns the path of the mapped SCSI/FC device if it already exists.
func (c *connectorSCSIFC) GetDiskDevicePath(diskPathFilter block.DevicePathFilterFunc) (string, error) {
	return block.GetDiskDevicePath(scsiDiskDevicePrefix, diskPathFilter)
}

func (c *connectorSCSIFC) RemoveDiskDevice(ctx context.Context, devicePath string) error {
	return nil
}

func (c *connectorSCSIFC) WaitDiskDeviceResize(ctx context.Context, devicePath string, newSizeBytes int64) error {
	return block.WaitDiskDeviceResize(ctx, devicePath, newSizeBytes)
}

// normalizeWWPN normalizes the WWPN string to make it comparable regardless of the format
// it's provided in. Linux sysfs reports WWPNs as "0x" with 16 hex chars ("0x210034800d7035b3"),
// while storage array might report it using colon-separated byte format ("21:00:34:80:0d:70:35:b3").
func normalizeWWPN(wwpn string) string {
	wwpn = strings.TrimSpace(wwpn)
	wwpn = strings.ToLower(wwpn)
	wwpn = strings.TrimPrefix(wwpn, "0x")
	wwpn = strings.ReplaceAll(wwpn, ":", "")
	return wwpn
}
