package drivers

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

type common struct {
	name        string
	config      map[string]string
	getVolID    func(volType VolumeType, volName string) (int64, error)
	commonRules *Validators
	state       *state.State
	logger      logger.Logger
	patches     map[string]func() error
}

func (d *common) init(state *state.State, name string, config map[string]string, logger logger.Logger, volIDFunc func(volType VolumeType, volName string) (int64, error), commonRules *Validators) {
	d.name = name
	d.config = config
	d.getVolID = volIDFunc
	d.commonRules = commonRules
	d.state = state
	d.logger = logger
}

// isRemote returns false indicating this driver does not use remote storage.
func (d *common) isRemote() bool {
	return false
}

// validatePool validates a pool config against common rules and optional driver specific rules.
func (d *common) validatePool(config map[string]string, driverRules map[string]func(value string) error, volumeRules map[string]func(value string) error) error {
	checkedFields := map[string]struct{}{}

	// Get rules common for all drivers.
	rules := d.commonRules.PoolRules()

	// Merge driver specific rules into common rules.
	for field, validator := range driverRules {
		rules[field] = validator
	}

	// Add to pool volume configuration options as volume.* options.
	// These will be used as default configuration options for volume.
	for volRule, volValidator := range volumeRules {
		rules[fmt.Sprintf("volume.%s", volRule)] = volValidator
	}

	// Run the validator against each field.
	for k, validator := range rules {
		checkedFields[k] = struct{}{} //Mark field as checked.
		err := validator(config[k])
		if err != nil {
			return fmt.Errorf("Invalid value for option %q: %w", k, err)
		}
	}

	// Look for any unchecked fields, as these are unknown fields and validation should fail.
	for k := range config {
		_, checked := checkedFields[k]
		if checked {
			continue
		}

		// User keys are not validated.
		if strings.HasPrefix(k, "user.") {
			continue
		}

		return fmt.Errorf("Invalid option %q", k)
	}

	return nil
}

// fillVolumeConfig populates volume config with defaults from pool.
// excludeKeys allow exclude some keys from copying to volume config.
// Sometimes that can be useful when copying is dependant from specific conditions
// and shouldn't be done in generic way.
func (d *common) fillVolumeConfig(vol *Volume, excludedKeys ...string) error {
	for k := range d.config {
		if !strings.HasPrefix(k, "volume.") {
			continue
		}

		volKey := strings.TrimPrefix(k, "volume.")

		isExcluded := false
		for _, excludedKey := range excludedKeys {
			if excludedKey == volKey {
				isExcluded = true
				break
			}
		}

		if isExcluded {
			continue
		}

		// If volume type is not custom, don't copy "size" property to volume config.
		if vol.volType != VolumeTypeCustom && volKey == "size" {
			continue
		}

		// security.shifted and security.unmapped are only relevant for custom filesystem volumes.
		if (vol.Type() != VolumeTypeCustom || vol.ContentType() != ContentTypeFS) && (volKey == "security.shifted" || volKey == "security.unmapped") {
			continue
		}

		if vol.config[volKey] == "" {
			vol.config[volKey] = d.config[k]
		}
	}

	return nil
}

// FillVolumeConfig populate volume with default config.
func (d *common) FillVolumeConfig(vol Volume) error {
	return d.fillVolumeConfig(&vol)
}

// validateVolume validates a volume config against common rules and optional driver specific rules.
// This functions has a removeUnknownKeys option that if set to true will remove any unknown fields
// (excluding those starting with "user.") which can be used when translating a volume config to a
// different storage driver that has different options.
func (d *common) validateVolume(vol Volume, driverRules map[string]func(value string) error, removeUnknownKeys bool) error {
	checkedFields := map[string]struct{}{}

	// Get rules common for all drivers.
	rules := d.commonRules.VolumeRules(vol)

	// Merge driver specific rules into common rules.
	for field, validator := range driverRules {
		rules[field] = validator
	}

	// Run the validator against each field.
	for k, validator := range rules {
		checkedFields[k] = struct{}{} //Mark field as checked.
		err := validator(vol.config[k])
		if err != nil {
			return fmt.Errorf("Invalid value for volume %q option %q: %w", vol.name, k, err)
		}
	}

	// Look for any unchecked fields, as these are unknown fields and validation should fail.
	for k := range vol.config {
		_, checked := checkedFields[k]
		if checked {
			continue
		}

		// User keys are not validated.
		if strings.HasPrefix(k, "user.") {
			continue
		}

		if removeUnknownKeys {
			delete(vol.config, k)
		} else {
			return fmt.Errorf("Invalid option for volume %q option %q", vol.name, k)
		}
	}

	// If volume type is not custom, don't allow "size" property.
	if vol.volType != VolumeTypeCustom && vol.config["size"] != "" {
		return fmt.Errorf("Volume %q property is only valid for custom volume types", "size")
	}

	return nil
}

// MigrationType returns the type of transfer methods to be used when doing migrations between pools
// in preference order.
func (d *common) MigrationTypes(contentType ContentType, refresh bool) []migration.Type {
	var transportType migration.MigrationFSType
	var rsyncFeatures []string

	// Do not pass compression argument to rsync if the associated
	// config key, that is rsync.compression, is set to false.
	if shared.IsFalse(d.Config()["rsync.compression"]) {
		rsyncFeatures = []string{"xattrs", "delete", "bidirectional"}
	} else {
		rsyncFeatures = []string{"xattrs", "delete", "compress", "bidirectional"}
	}

	if contentType == ContentTypeBlock {
		transportType = migration.MigrationFSType_BLOCK_AND_RSYNC
	} else {
		transportType = migration.MigrationFSType_RSYNC
	}

	return []migration.Type{
		{
			FSType:   transportType,
			Features: rsyncFeatures,
		},
	}
}

// Name returns the pool name.
func (d *common) Name() string {
	return d.name
}

// Logger returns the current logger.
func (d *common) Logger() logger.Logger {
	return d.logger
}

// Config returns the storage pool config (as a copy, so not modifiable).
func (d *common) Config() map[string]string {
	confCopy := make(map[string]string, len(d.config))
	for k, v := range d.config {
		confCopy[k] = v
	}

	return confCopy
}

// ApplyPatch looks for a suitable patch and runs it.
func (d *common) ApplyPatch(name string) error {
	if d.patches == nil {
		return fmt.Errorf("The patch mechanism isn't implemented on pool %q", d.name)
	}

	// Locate the patch.
	patch, ok := d.patches[name]
	if !ok {
		return fmt.Errorf("Patch %q isn't implemented on pool %q", name, d.name)
	}

	// Handle cases where a patch isn't needed.
	if patch == nil {
		return nil
	}

	return patch()
}

// moveGPTAltHeader moves the GPT alternative header to the end of the disk device supplied.
// If the device supplied is not detected as not being a GPT disk then no action is taken and nil is returned.
// If the required sgdisk command is not available a warning is logged, but no error is returned, as really it is
// the job of the VM quest to ensure the partitions are resized to the size of the disk (as LXD does not dicatate
// what partition structure (if any) the disk should have. However we do attempt to move the GPT alternative
// header where possible so that the backup header is where it is expected in case of any corruption with the
// primary header.
func (d *common) moveGPTAltHeader(devPath string) error {
	path, err := exec.LookPath("sgdisk")
	if err != nil {
		d.logger.Warn("Skipped moving GPT alternative header to end of disk as sgdisk command not found", logger.Ctx{"dev": devPath})
		return nil
	}

	_, err = shared.RunCommand(path, "--move-second-header", devPath)
	if err == nil {
		d.logger.Debug("Moved GPT alternative header to end of disk", logger.Ctx{"dev": devPath})
		return nil
	}

	runErr, ok := err.(shared.RunError)
	if ok {
		exitError, ok := runErr.Err.(*exec.ExitError)
		if ok {
			// sgdisk manpage says exit status 3 means:
			// "Non-GPT disk detected and no -g option, but operation requires a write action".
			if exitError.ExitCode() == 3 {
				return nil // Non-error as non-GPT disk specified.
			}
		}
	}

	return err
}

// runFiller runs the supplied filler, and setting the returned volume size back into filler.
func (d *common) runFiller(vol Volume, devPath string, filler *VolumeFiller, allowUnsafeResize bool) error {
	if filler == nil || filler.Fill == nil {
		return nil
	}

	vol.driver.Logger().Debug("Running filler function", logger.Ctx{"dev": devPath, "path": vol.MountPath()})
	volSize, err := filler.Fill(vol, devPath, allowUnsafeResize)
	if err != nil {
		return err
	}

	filler.Size = volSize

	return nil
}
