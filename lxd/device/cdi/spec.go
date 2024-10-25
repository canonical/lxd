//go:build !armhf && !arm && !arm32

package cdi

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/NVIDIA/nvidia-container-toolkit/pkg/nvcdi"
	"tags.cncf.io/container-device-interface/specs-go"

	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

const (
	// defaultNvidiaTegraMountSpecPath is default location of CSV files that define the modifications required to the OCI spec.
	defaultNvidiaTegraMountSpecPath = "/etc/nvidia-container-runtime/host-files-for-container.d"
)

// defaultNvidiaTegraCSVFiles returns the default CSV files for the Nvidia Tegra platform.
func defaultNvidiaTegraCSVFiles(rootPath string) []string {
	files := []string{
		"devices.csv",
		"drivers.csv",
		"l4t.csv",
	}

	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, filepath.Join(rootPath, defaultNvidiaTegraMountSpecPath, file))
	}

	return paths
}

// generateNvidiaSpec generates a CDI spec for an Nvidia vendor.
func generateNvidiaSpec(s *state.State, cdiID ID, inst instance.Instance) (*specs.Spec, error) {
	l := logger.AddContext(logger.Ctx{"instanceName": inst.Name(), "projectName": inst.Project().Name, "cdiID": cdiID.String()})
	mode := nvcdi.ModeAuto
	if cdiID.Class == IGPU {
		mode = nvcdi.ModeCSV
	}

	indexDeviceNamer, err := nvcdi.NewDeviceNamer(nvcdi.DeviceNameStrategyIndex)
	if err != nil {
		return nil, fmt.Errorf("Failed to create device namer with index strategy: %w", err)
	}

	uuidDeviceNamer, err := nvcdi.NewDeviceNamer(nvcdi.DeviceNameStrategyUUID)
	if err != nil {
		return nil, fmt.Errorf("Failed to create device namer with uuid strategy: %w", err)
	}

	nvidiaCTKPath, err := exec.LookPath("nvidia-ctk")
	if err != nil {
		return nil, fmt.Errorf("Failed to find the nvidia-ctk binary: %w", err)
	}

	rootPath := ""
	devRootPath := ""
	if s.OS.InUbuntuCore() {
		//
		// This magic "gpu-2404-2" name comes from:
		// https://github.com/canonical/mesa-2404/blob/0e48b4d1b8e5cb4d3098d64417025824decd9846/scripts/bin/gpu-2404-provider-wrapper.in#L5
		//
		// Also, we can't use `/snap/lxd/current/gpu-2404-2` as a rootPath because of a bug in nvcdi package
		// which make nvcdi to fail to lookup for a library when driver root path contains a symlink
		// (in our case it's `/snap/lxd/current`).
		// We workaround it by using $SNAP environment variable which is not a symlink but a path to lxd snap
		// with a revision number like `/snap/lxd/12345`.
		//
		rootPath = os.Getenv("SNAP") + "/gpu-2404-2"
		devRootPath = "/"

		// Let's ensure that user did:
		// snap connect mesa-2404:kernel-gpu-2404 pc-kernel
		if !shared.PathExists("/snap/lxd/current/gpu-2404-2/usr/bin/nvidia-smi") {
			return nil, fmt.Errorf("Failed to find nvidia-smi tool. Please ensure that pc-kernel snap is connected to mesa-2404.")
		}
	} else if shared.InSnap() {
		rootPath = "/var/lib/snapd/hostfs"
		devRootPath = rootPath
	}

	cdilib, err := nvcdi.New(
		nvcdi.WithDeviceNamers(indexDeviceNamer, uuidDeviceNamer),
		nvcdi.WithLogger(NewCDILogger(l)),
		nvcdi.WithDriverRoot(rootPath),
		nvcdi.WithDevRoot(devRootPath),
		nvcdi.WithNVIDIACDIHookPath(nvidiaCTKPath),
		nvcdi.WithMode(mode),
		nvcdi.WithCSVFiles(defaultNvidiaTegraCSVFiles(rootPath)),
	)
	if err != nil {
		return nil, fmt.Errorf("Failed to create CDI library: %w", err)
	}

	specIface, err := cdilib.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("Failed to get CDI spec interface: %w", err)
	}

	spec := specIface.Raw()
	if spec == nil {
		return nil, fmt.Errorf("CDI spec is nil")
	}

	// The spec definition can be quite large so we log it to a file.
	specPath := filepath.Join(inst.LogPath(), fmt.Sprintf("nvidia_cdi_spec.%s.log", strings.ReplaceAll(cdiID.String(), "/", "_")))
	specFile, err := os.Create(specPath)
	if err != nil {
		l.Warn("Failed to create a log file to hold a CDI spec", logger.Ctx{"specPath": specPath, "error": err})
		return spec, nil
	}

	defer specFile.Close()

	_, err = specFile.WriteString(logger.Pretty(spec))
	if err != nil {
		return nil, fmt.Errorf("Failed to write spec to %q: %v", specPath, err)
	}

	l.Debug("CDI spec has been successfully generated", logger.Ctx{"specPath": specPath})
	return spec, nil
}

// generateSpec generates a CDI spec for the given CDI ID.
func generateSpec(s *state.State, cdiID ID, inst instance.Instance) (*specs.Spec, error) {
	switch cdiID.Vendor {
	case NVIDIA:
		return generateNvidiaSpec(s, cdiID, inst)
	default:
		return nil, fmt.Errorf("Unsupported CDI vendor (%q) for the spec generation", cdiID.Vendor)
	}
}
