package cdi

import (
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/NVIDIA/nvidia-container-toolkit/pkg/nvcdi"
	cdiSpec "tags.cncf.io/container-device-interface/pkg/cdi"
	"tags.cncf.io/container-device-interface/specs-go"

	"github.com/canonical/lxd/shared"
)

const (
	// defaultNvidiaTegraMountSpecPath is default location of CSV files that define the modifications required to the OCI spec.
	defaultNvidiaTegraMountSpecPath = "/etc/nvidia-container-runtime/host-files-for-container.d"
)

// DefaultNvidiaTegraCSVFiles returns the default CSV files for the Nvidia Tegra platform.
func DefaultNvidiaTegraCSVFiles() []string {
	files := []string{
		"devices.csv",
		"drivers.csv",
		"l4t.csv",
	}

	var paths []string
	for _, file := range files {
		paths = append(paths, filepath.Join(defaultNvidiaTegraMountSpecPath, file))
	}

	return paths
}

// generateNvidiaSpec generates a CDI spec for an Nvidia vendor.
func generateNvidiaSpec(cdiID ID) (*specs.Spec, error) {
	mode := nvcdi.ModeAuto
	if cdiID.DeviceType() == IGPU {
		mode = nvcdi.ModeCSV
	}

	deviceNamer, err := nvcdi.NewDeviceNamer(nvcdi.DeviceNameStrategyIndex)
	if err != nil {
		return nil, fmt.Errorf("Failed to create device namer: %v", err)
	}

	deviceNamers := []nvcdi.DeviceNamer{deviceNamer}
	nvidiaCTKPath, err := exec.LookPath("nvidia-ctk")
	if err != nil {
		return nil, fmt.Errorf("Failed to find the nvidia-ctk binary: %v", err)
	}

	rootPath := ""
	if shared.InSnap() {
		rootPath = "/var/lib/snapd/hostfs"
	}

	cdilib, err := nvcdi.New(
		nvcdi.WithLogger(&CDILogger{}),
		nvcdi.WithDriverRoot(rootPath),
		nvcdi.WithDevRoot(rootPath),
		nvcdi.WithNVIDIACTKPath(nvidiaCTKPath),
		nvcdi.WithDeviceNamers(deviceNamers...),
		nvcdi.WithMode(mode),
		nvcdi.WithCSVFiles(DefaultNvidiaTegraCSVFiles()),
	)
	if err != nil {
		return nil, fmt.Errorf("Failed to create CDI library: %v", err)
	}

	deviceSpecs, err := cdilib.GetAllDeviceSpecs()
	if err != nil {
		return nil, fmt.Errorf("Failed to create device CDI specs: %v", err)
	}

	commonEdits, err := cdilib.GetCommonEdits()
	if err != nil {
		return nil, fmt.Errorf("Failed to create edits common for entities: %v", err)
	}

	return &specs.Spec{
		Version:        cdiSpec.CurrentVersion,
		Kind:           fmt.Sprintf("%s/%s", cdiID.Vendor(), cdiID.Product()),
		Devices:        deviceSpecs,
		ContainerEdits: *commonEdits.ContainerEdits,
	}, nil
}

// GenerateSpec generates a CDI spec for the given CDI ID.
func GenerateSpec(cdiID ID) (*specs.Spec, error) {
	switch cdiID.Vendor() {
	case Nvidia:
		return generateNvidiaSpec(cdiID)
	default:
		return nil, fmt.Errorf("unsupported CDI vendor (%q) for the spec generation", cdiID.Vendor())
	}
}
