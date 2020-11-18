package drivers

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/grant-he/lxd/shared"
)

func (d *zfs) patchStorageCreateVM() error {
	// Create any missing initial dataset.
	for _, dataset := range d.initialDatasets() {
		if d.checkDataset(filepath.Join(d.config["zfs.pool_name"], dataset)) {
			continue
		}

		err := d.createDataset(filepath.Join(d.config["zfs.pool_name"], dataset), "mountpoint=none")
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *zfs) patchStorageZFSMount() error {
	datasets, err := d.getDatasets(d.config["zfs.pool_name"])
	if err != nil {
		return err
	}

	for _, dataset := range datasets {
		// Skip snapshots.
		if strings.Contains(dataset, "@") {
			continue
		}

		// Skip bookmarks.
		if strings.Contains(dataset, "#") {
			continue
		}

		// Skip block devices.
		if strings.HasSuffix(dataset, ".block") {
			continue
		}

		// Skip top level.
		if !strings.Contains(dataset, "/") {
			continue
		}

		// We only care about containers, images and custom volumes.
		if !shared.StringInSlice(strings.SplitN(dataset, "/", 2)[0], []string{"containers", "images", "custom"}) {
			continue
		}

		// Apply mountpoint changes.
		oldMountPoint, err := d.getDatasetProperty(filepath.Join(d.config["zfs.pool_name"], dataset), "mountpoint")
		if err != nil {
			return err
		}
		newMountPoint := filepath.Join(shared.VarPath("storage-pools", d.name, dataset))

		if oldMountPoint != newMountPoint {
			err := d.setDatasetProperties(filepath.Join(d.config["zfs.pool_name"], dataset), fmt.Sprintf("mountpoint=%s", newMountPoint), "canmount=noauto")
			if err != nil {
				return err
			}
		}

		// Apply canmount changes.
		oldCanMount, err := d.getDatasetProperty(filepath.Join(d.config["zfs.pool_name"], dataset), "canmount")
		if err != nil {
			return err
		}

		if oldCanMount != "noauto" {
			err := d.setDatasetProperties(filepath.Join(d.config["zfs.pool_name"], dataset), "canmount=noauto")
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (d *zfs) patchStorageZFSVolMode() error {
	if len(zfsVersion) >= 3 && zfsVersion[0:3] == "0.6" {
		d.logger.Warn("Unable to set volmode on parent virtual-machines datasets due to ZFS being too old")
		return nil
	}

	// Set volmode=none on the parent virtual-machines directory
	err := d.setDatasetProperties(filepath.Join(d.config["zfs.pool_name"], "virtual-machines"), "volmode=none")
	if err != nil {
		return err
	}

	err = d.setDatasetProperties(filepath.Join(d.config["zfs.pool_name"], "deleted", "virtual-machines"), "volmode=none")
	if err != nil {
		return err
	}

	return nil
}
