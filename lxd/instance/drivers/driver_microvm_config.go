package drivers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// MicroVMConfigFileName is the name of the generated libkrun VM config file in the instance log directory.
const MicroVMConfigFileName = "microvm.conf"

// MicroVMConfig is the libkrun VM configuration written by LXD and read by forklibkrun.
type MicroVMConfig struct {
	Version uint64          `json:"version"`
	Body    json.RawMessage `json:"body"`
}

// MicroVMConfigKernel specifies the kernel image, format, and boot parameters.
type MicroVMConfigKernel struct {
	Path    string `json:"path"`
	Format  string `json:"format"`
	Cmdline string `json:"cmdline"`
}

// MicroVMConfigNIC specifies a network interface to attach to the microVM.
type MicroVMConfigNIC struct {
	Tap    string `json:"tap"`
	HWAddr string `json:"hwaddr"`
}

// MicroVMConfigVsock specifies the virtio-vsock bridge parameters for lxd-agent connectivity.
type MicroVMConfigVsock struct {
	AgentSocket string `json:"agent_socket"`
	LXDPort     uint32 `json:"lxd_port"`
	LXDSocket   string `json:"lxd_socket"`
}

// MicroVMConfigV1 represents the v1 schema of the MicroVM configuration.
type MicroVMConfigV1 struct {
	CPUs        uint8               `json:"cpus"`
	MemoryMiB   uint32              `json:"memory_mib"`
	Kernel      MicroVMConfigKernel `json:"kernel"`
	RootDisk    string              `json:"root_disk"`
	ConfigDrive string              `json:"config_drive"`
	Console     string              `json:"console"`
	NICs        []MicroVMConfigNIC  `json:"nics,omitempty"`
	Vsock       *MicroVMConfigVsock `json:"vsock,omitempty"`
}

// Validate checks that all required fields in the MicroVM configuration are present and valid.
func (c *MicroVMConfigV1) Validate() error {
	if c.CPUs == 0 {
		return errors.New("Missing or invalid \"cpus\" in MicroVM config")
	}

	if c.MemoryMiB == 0 {
		return errors.New("Missing or invalid \"memory_mib\" in MicroVM config")
	}

	if c.Kernel.Path == "" {
		return errors.New("Missing required \"kernel.path\" in MicroVM config")
	}

	if c.RootDisk == "" {
		return errors.New("Missing required \"root_disk\" in MicroVM config")
	}

	if c.ConfigDrive == "" {
		return errors.New("Missing required \"config_drive\" in MicroVM config")
	}

	if c.Console == "" {
		return errors.New("Missing required \"console\" in MicroVM config")
	}

	for i, nic := range c.NICs {
		if nic.Tap == "" || nic.HWAddr == "" {
			return fmt.Errorf("Invalid NIC entry at index %d in MicroVM config: missing tap or hwaddr", i)
		}
	}

	if c.Vsock != nil {
		if c.Vsock.AgentSocket == "" || c.Vsock.LXDPort == 0 || c.Vsock.LXDSocket == "" {
			return errors.New("Incomplete \"vsock\" configuration in MicroVM config: agent_socket, lxd_port, and lxd_socket are required")
		}
	}

	return nil
}

// WriteMicroVMConfig wraps cfg in a version 1 envelope, serializes it as indented JSON,
// and writes it atomically to path with mode 0640.
func WriteMicroVMConfig(path string, cfg MicroVMConfigV1) error {
	body, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("Failed encoding MicroVM configuration: %w", err)
	}

	envelope := MicroVMConfig{
		Version: 1,
		Body:    body,
	}

	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return fmt.Errorf("Failed encoding MicroVM config: %w", err)
	}

	data = append(data, '\n')

	tmpPath := path + ".tmp"
	err = os.WriteFile(tmpPath, data, 0640)
	if err != nil {
		return fmt.Errorf("Failed writing temporary MicroVM config: %w", err)
	}

	err = os.Rename(tmpPath, path)
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("Failed moving MicroVM config into place: %w", err)
	}

	return nil
}

// ReadMicroVMConfig reads, parses, and validates a MicroVMConfig from a JSON file.
func ReadMicroVMConfig(path string) (*MicroVMConfigV1, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("Failed opening MicroVM config: %w", err)
	}

	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(f)

	var cfg MicroVMConfig
	err = dec.Decode(&cfg)
	if err != nil {
		return nil, fmt.Errorf("Failed decoding MicroVM config: %w", err)
	}

	// We implement the validation in this way in order to be able to add a v2
	// of the config file and have the logic in place already.
	var cfgV1 MicroVMConfigV1
	switch cfg.Version {
	case 1:
		err = json.Unmarshal(cfg.Body, &cfgV1)
		if err != nil {
			return nil, fmt.Errorf("Failed decoding MicroVM configuration (v1): %w", err)
		}

	default:
		return nil, fmt.Errorf(`Unknown MicroVM config version "%d"`, cfg.Version)
	}

	err = cfgV1.Validate()
	if err != nil {
		return nil, err
	}

	return &cfgV1, nil
}
