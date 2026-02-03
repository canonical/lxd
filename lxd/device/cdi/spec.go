//go:build !armhf && !arm && !arm32

package cdi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/NVIDIA/nvidia-container-toolkit/pkg/nvcdi"
	"github.com/NVIDIA/nvidia-container-toolkit/pkg/nvcdi/transform"
	"tags.cncf.io/container-device-interface/specs-go"

	"github.com/canonical/lxd/lxd/instance"
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
func generateNvidiaSpec(isCore bool, cdiID ID, inst instance.Instance) (*specs.Spec, error) {
	l := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "cdiID": cdiID.String()})
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
	configSearchPaths := []string{}
	if isCore {
		devRootPath = "/"

		gpuCore24Root := os.Getenv("SNAP") + "/gpu-2404"
		gpuInterfaceProviderWrapper := gpuCore24Root + "/bin/gpu-2404-provider-wrapper"

		// Let's ensure that user has mesa-2404 snap connected.
		if !shared.PathExists(gpuInterfaceProviderWrapper) {
			return nil, errors.New("Failed to find gpu-2404-provider-wrapper. Please ensure that mesa-2404 snap is connected to lxd.")
		}

		//
		// NVIDIA_DRIVER_ROOT environment variable name comes from:
		// https://git.launchpad.net/~canonical-kernel-snaps/canonical-kernel-snaps/+git/kernel-snaps-u24.04/commit/?id=928d273d881abc8599f9cb754eeb753aa7113852
		//
		// You may wonder why we need this gpu-2404-provider-wrapper printenv
		// NVIDIA_DRIVER_ROOT machinery instead of simple
		// os.Getenv("NVIDIA_DRIVER_ROOT"). Reason is that mesa-2404 or
		// pc-kernel may be upgraded (refreshed) while LXD snap version remains
		// the same and there is no guarantee that NVIDIA_DRIVER_ROOT value
		// won't change between those refreshes...
		//
		cmd := []string{
			gpuInterfaceProviderWrapper,
			"printenv",
			"NVIDIA_DRIVER_ROOT",
		}

		rootPath, err = shared.RunCommand(context.TODO(), cmd[0], cmd[1:]...)
		if err != nil {
			return nil, fmt.Errorf("Failed to determine NVIDIA driver root path: %w", err)
		}

		rootPath = strings.TrimSuffix(rootPath, "\n")
		configSearchPaths = []string{rootPath + "/usr/share", gpuCore24Root + "/usr/share"}

		// Let's ensure that user did:
		// snap connect mesa-2404:kernel-gpu-2404 pc-kernel
		if !shared.PathExists(rootPath + "/usr/bin/nvidia-smi") {
			return nil, fmt.Errorf("Failed to find nvidia-smi tool in %q. Please ensure that pc-kernel snap is connected to mesa-2404.", rootPath)
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
		nvcdi.WithConfigSearchPaths(configSearchPaths),
		nvcdi.WithMergedDeviceOptions(transform.WithName("all")),
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
		return nil, errors.New("CDI spec is nil")
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
var generateSpec = func(isCore bool, cdiID ID, inst instance.Instance) (*specs.Spec, error) {
	switch cdiID.Vendor {
	case NVIDIA:
		return generateNvidiaSpec(isCore, cdiID, inst)
	default:
		return nil, fmt.Errorf("Unsupported CDI vendor (%q) for the spec generation", cdiID.Vendor)
	}
}
