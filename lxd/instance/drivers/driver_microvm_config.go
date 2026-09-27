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
	CPUs        uint8               `json:"cpus"`
	MemoryMiB   uint32              `json:"memory_mib"`
	Kernel      MicroVMConfigKernel `json:"kernel"`
	RootDisk    string              `json:"root_disk"`
	ConfigDrive string              `json:"config_drive"`
	Console     string              `json:"console"`
	ExitFile    string              `json:"exit_file"`
	NICs        []MicroVMConfigNIC  `json:"nics,omitempty"`
	Vsock       *MicroVMConfigVsock `json:"vsock,omitempty"`
}

// MicroVMConfigKernel specifies the kernel image, optional initrd, format, and boot parameters.
type MicroVMConfigKernel struct {
	Path    string `json:"path"`
	Format  string `json:"format"`
	Initrd  string `json:"initrd,omitempty"`
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

// Validate checks that all required fields in the MicroVM configuration are present and valid.
func (c *MicroVMConfig) Validate() error {
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

// WriteMicroVMConfig serializes the configuration as indented JSON and writes it atomically to path with mode 0640.
func WriteMicroVMConfig(path string, cfg MicroVMConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
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
// Unknown fields are rejected to prevent typos and configuration drift.
func ReadMicroVMConfig(path string) (*MicroVMConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("Failed opening MicroVM config: %w", err)
	}

	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()

	var cfg MicroVMConfig
	err = dec.Decode(&cfg)
	if err != nil {
		return nil, fmt.Errorf("Failed decoding MicroVM config: %w", err)
	}

	err = cfg.Validate()
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}
