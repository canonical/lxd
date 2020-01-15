package drivers

import (
	"path/filepath"
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
