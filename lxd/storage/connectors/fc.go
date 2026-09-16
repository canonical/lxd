package connectors

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// fcHostPort is a local FC HBA port as reported by the FC transport class.
// The node name and the port name are the raw sysfs values (e.g. "0x2000.."),
// which the callers normalize when comparing them.
type fcHostPort struct {
	host      string
	nodeName  string
	portName  string
	portState string
}

// fcRemotePort is an FC target port that a local HBA is logged into. The port name is the raw
// sysfs value (e.g. "0x5001.."), which the callers normalize when comparing it.
type fcRemotePort struct {
	host         string
	channel      string
	portName     string
	portState    string
	scsiTargetID string
}

// fcHostPorts returns the local FC HBA ports. A port whose node name or port name cannot be
// read is omitted, and a port state that cannot be read is reported empty, so that a caller
// requiring an online port skips it.
func fcHostPorts() ([]fcHostPort, error) {
	fcHostBasePath := "/sys/class/fc_host"

	hosts, err := os.ReadDir(fcHostBasePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}

		return nil, fmt.Errorf("Failed reading FC hosts: %w", err)
	}

	ports := make([]fcHostPort, 0, len(hosts))
	for _, host := range hosts {
		hostPath := filepath.Join(fcHostBasePath, host.Name())

		nodeName, err := os.ReadFile(filepath.Join(hostPath, "node_name"))
		if err != nil {
			continue
		}

		portName, err := os.ReadFile(filepath.Join(hostPath, "port_name"))
		if err != nil {
			continue
		}

		portState, _ := os.ReadFile(filepath.Join(hostPath, "port_state"))

		port := fcHostPort{
			host:      host.Name(),
			nodeName:  strings.TrimSpace(string(nodeName)),
			portName:  strings.TrimSpace(string(portName)),
			portState: strings.TrimSpace(string(portState)),
		}

		ports = append(ports, port)
	}

	// os.ReadDir orders by filename, but we cannot rely on this order being stable across
	// reboots or HBA re-enumeration. Sort ports by port name so that the reported identity
	// of the host does not change between reboots.
	slices.SortFunc(ports, func(a, b fcHostPort) int {
		return strings.Compare(a.portName, b.portName)
	})

	return ports, nil
}

// fcRemotePorts returns the FC target ports that the local HBAs are logged into. The fabric
// name server returns only the ports of the same zone, therefore the remote ports of a local
// HBA are the target ports that HBA is zoned to.
//
// A port whose port name cannot be read is omitted. The port state and the SCSI target id are
// reported empty when they cannot be read, so that a caller not requiring them keeps the port.
// The FC transport class being absent yields no ports and no error.
func fcRemotePorts() ([]fcRemotePort, error) {
	rportBasePath := "/sys/class/fc_remote_ports"

	rports, err := os.ReadDir(rportBasePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}

		return nil, fmt.Errorf("Failed reading FC remote ports: %w", err)
	}

	ports := make([]fcRemotePort, 0, len(rports))
	for _, rport := range rports {
		// rport directory name has form "rport-H:C-R":
		// H = local SCSI host index, C = channel, R = rport index.
		hostIdx, rest, ok := strings.Cut(strings.TrimPrefix(rport.Name(), "rport-"), ":")
		if !ok {
			continue
		}

		channel, _, ok := strings.Cut(rest, "-")
		if !ok {
			continue
		}

		rportPath := filepath.Join(rportBasePath, rport.Name())

		portName, err := os.ReadFile(filepath.Join(rportPath, "port_name"))
		if err != nil {
			continue
		}

		portState, _ := os.ReadFile(filepath.Join(rportPath, "port_state"))
		scsiTargetID, _ := os.ReadFile(filepath.Join(rportPath, "scsi_target_id"))

		port := fcRemotePort{
			host:         "host" + hostIdx,
			channel:      channel,
			portName:     strings.TrimSpace(string(portName)),
			portState:    strings.TrimSpace(string(portState)),
			scsiTargetID: strings.TrimSpace(string(scsiTargetID)),
		}

		ports = append(ports, port)
	}

	// os.ReadDir orders by filename, but we cannot rely on this order being stable across
	// reboots or HBA re-enumeration. Sort ports by port name so that the reported identity
	// of the host does not change between reboots.
	slices.SortFunc(ports, func(a, b fcRemotePort) int {
		return strings.Compare(a.portName, b.portName)
	})

	return ports, nil
}
