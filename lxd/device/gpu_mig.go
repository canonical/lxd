package device

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/device/cdi"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	pcidev "github.com/canonical/lxd/lxd/device/pci"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/resources"
	"github.com/canonical/lxd/shared/api"
)

type gpuMIG struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *gpuMIG) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container) {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{}

	optionalFields := []string{
		"vendorid",
		"productid",
		"id",
		"pci",
		"mig.gi",
		"mig.ci",
		"mig.uuid",
	}

	err := d.config.Validate(gpuValidationRules(requiredFields, optionalFields))
	if err != nil {
		return err
	}

	if d.config["pci"] != "" {
		for _, field := range []string{"id", "productid", "vendorid"} {
			if d.config[field] != "" {
				return fmt.Errorf(`Cannot use %q when "pci" is set`, field)
			}
		}

		d.config["pci"] = pcidev.NormaliseAddress(d.config["pci"])
	}

	// Validate the "id" field.
	if d.config["id"] != "" {
		for _, field := range []string{"pci", "productid", "vendorid"} {
			if d.config[field] != "" {
				return fmt.Errorf(`Cannot use %q when "id" is set`, field)
			}
		}

		// Validate "id" is either an integer DRM card ID or a CDI identifier.
		_, err = strconv.ParseUint(d.config["id"], 10, 64)
		if err != nil {
			cdiID, err := cdi.ToCDI(d.config["id"])
			if err != nil {
				// Structurally incorrect CDI ID supplied.
				if api.StatusErrorCheck(err, http.StatusBadRequest) {
					return fmt.Errorf(`"id" must be an integer DRM card ID or a CDI ID: %w`, err)
				}

				// Structurally correct CDI ID supplied, but still invalid.
				return err
			}

			// CDI "id" must be of MIG class.
			if cdiID.Class != cdi.MIG {
				return fmt.Errorf("CDI identifier %q is not a MIG device; use gputype=physical for non-MIG CDI devices", d.config["id"])
			}

			// CDI "id" must refer to a single MIG device.
			if cdiID.Name == "all" {
				return fmt.Errorf(`CDI identifier %q must refer to a single MIG device, not "all"`, d.config["id"])
			}

			// CDI "id" is mutually exclusive with mig.* fields.
			for _, field := range []string{"mig.uuid", "mig.gi", "mig.ci"} {
				if d.config[field] != "" {
					return fmt.Errorf(`Cannot use %q when a CDI "id" is set`, field)
				}
			}

			// CDI "id" is the only MIG selector needed.
			return nil
		}
	}

	// Without a CDI "id", require mig.uuid or mig.gi+mig.ci.
	if d.config["mig.uuid"] != "" {
		for _, field := range []string{"mig.gi", "mig.ci"} {
			if d.config[field] != "" {
				return fmt.Errorf(`Cannot use %q when "mig.uuid" is set`, field)
			}
		}
	} else if d.config["mig.gi"] == "" || d.config["mig.ci"] == "" {
		return errors.New(`Either "mig.uuid", both "mig.gi" and "mig.ci", or a CDI "id" must be set`)
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *gpuMIG) validateEnvironment() error {
	return validatePCIDevice(d.config["pci"])
}

// migCDIID resolves the CDI identifier for the MIG device from the device config.
// It handles three cases: a direct CDI "id", a "mig.uuid", or a "mig.gi"+"mig.ci" pair.
func (d *gpuMIG) migCDIID() (cdi.ID, error) {
	// Case 1: direct CDI "id".
	if d.config["id"] != "" {
		cdiID, err := cdi.ToCDI(d.config["id"])
		if err == nil && cdiID != nil {
			return *cdiID, nil
		}
	}

	// Case 2: "mig.uuid". The UUID uniquely identifies the MIG device, no parent GPU lookup is needed.
	// However, if a parent GPU selector (id, pci, vendorid, productid) is present, perform the lookup
	// for validation to preserve backward compatibility with existing configurations.
	if d.config["mig.uuid"] != "" {
		migUUID := d.config["mig.uuid"]
		if !strings.HasPrefix(migUUID, "MIG-") {
			migUUID = "MIG-" + migUUID
		}

		hasSelector := d.config["id"] != "" || d.config["pci"] != "" || d.config["vendorid"] != "" || d.config["productid"] != ""
		if hasSelector {
			gpus, err := resources.GetGPU()
			if err != nil {
				return cdi.ID{}, fmt.Errorf("Failed getting GPU resources: %w", err)
			}

			var parentGPU *api.ResourcesGPUCard
			for i := range gpus.Cards {
				if !gpuSelected(d.Config(), gpus.Cards[i]) {
					continue
				}

				if parentGPU != nil {
					return cdi.ID{}, errors.New("More than one GPU matched the MIG device")
				}

				parentGPU = &gpus.Cards[i]
			}

			if parentGPU == nil {
				return cdi.ID{}, errors.New("Failed detecting requested GPU device")
			}

			if parentGPU.Nvidia == nil {
				return cdi.ID{}, errors.New("Card is not an NVIDIA GPU or driver is not properly set up")
			}
		}

		return cdi.ID{Vendor: cdi.NVIDIA, Class: cdi.MIG, Name: migUUID}, nil
	}

	// Case 3: "mig.gi"+"mig.ci". Resolve to UUID via NVML, which requires the parent GPU PCI address.
	gpus, err := resources.GetGPU()
	if err != nil {
		return cdi.ID{}, fmt.Errorf("Failed getting GPU resources: %w", err)
	}

	var parentGPU *api.ResourcesGPUCard
	for i := range gpus.Cards {
		if !gpuSelected(d.Config(), gpus.Cards[i]) {
			continue
		}

		if parentGPU != nil {
			return cdi.ID{}, errors.New("More than one GPU matched the MIG device")
		}

		parentGPU = &gpus.Cards[i]
	}

	if parentGPU == nil {
		return cdi.ID{}, errors.New("Failed detecting requested GPU device")
	}

	if parentGPU.Nvidia == nil {
		return cdi.ID{}, errors.New("Card is not an NVIDIA GPU or driver is not properly set up")
	}

	gi, err := strconv.Atoi(d.config["mig.gi"])
	if err != nil {
		return cdi.ID{}, fmt.Errorf("Invalid mig.gi value %q: %w", d.config["mig.gi"], err)
	}

	ci, err := strconv.Atoi(d.config["mig.ci"])
	if err != nil {
		return cdi.ID{}, fmt.Errorf("Invalid mig.ci value %q: %w", d.config["mig.ci"], err)
	}

	migUUID, err := cdi.MIGDeviceUUID(parentGPU.PCIAddress, gi, ci)
	if err != nil {
		return cdi.ID{}, fmt.Errorf("Failed resolving MIG device UUID for gi=%d ci=%d: %w", gi, ci, err)
	}

	return cdi.ID{Vendor: cdi.NVIDIA, Class: cdi.MIG, Name: migUUID}, nil
}

// CanHotPlug returns whether the device can be managed whilst the instance is running.
func (d *gpuMIG) CanHotPlug() bool {
	return false
}

// Start is run when the device is added to the container.
func (d *gpuMIG) Start() (*deviceConfig.RunConfig, error) {
	// Check the basic config.
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	// Resolve the CDI identifier for the MIG device.
	cdiID, err := d.migCDIID()
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	err = applyCDIDeviceToContainer(&d.deviceCommon, cdiID, &runConf)
	if err != nil {
		return nil, err
	}

	return &runConf, nil
}

// Stop is run when the device is removed from the container.
func (d *gpuMIG) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	configFilePath := cdiConfigDevicesFilePath(d.inst.DevicesPath(), d.name)
	configDevices, err := cdi.ReloadConfigDevicesFromDisk(configFilePath)
	if err != nil {
		// Instances started before the CDI migration have no config file on disk.
		// Treat missing file as a no-op to allow a clean stop.
		if errors.Is(err, fs.ErrNotExist) {
			return &runConf, nil
		}

		return nil, err
	}

	err = stopCDIDevices(&d.deviceCommon, configDevices, &runConf)
	if err != nil {
		return nil, err
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the container.
func (d *gpuMIG) postStop() error {
	// missingFilesOK=true because instances started before the CDI migration have no files to clean up.
	return postStopCDIDevice(&d.deviceCommon, true)
}
