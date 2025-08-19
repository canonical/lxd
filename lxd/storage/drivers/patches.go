package drivers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/canonical/lxd/shared/api"
)

// PatchUpdatePowerFlexSnapshotPrefix patches the given snapshot volumes by adding the dedicated snapshot prefix.
// On the driver level we don't know if a snapshot on the storage array is an actual snapshot of a volume in LXD
// or a copied volume which was created by taking a snapshot (powerflex.snapshot_copy=true).
// Therefore this function is exported so it can be invoked from outside with the knowledge of which are the
// snapshots that require patching.
func PatchUpdatePowerFlexSnapshotPrefix(d Driver, snapVols []Volume) error {
	// Ensure this patch functions is only ever invoked for PowerFlex backed pools.
	powerFlexDriver, ok := d.(*powerflex)
	if !ok {
		return fmt.Errorf("Driver %T is not of type powerflex", d)
	}

	for _, snapVol := range snapVols {
		// Get the snapshot volumes patched (new) name including the prefix.
		newSnapVolName, err := powerFlexDriver.getVolumeName(snapVol)
		if err != nil {
			return err
		}

		// Check for a break of protocol.
		// For all the volumes passed into this patch function their name has to include the snapshot prefix.
		// If this is not the case, an invalid volume got passed.
		if !strings.HasPrefix(newSnapVolName, powerFlexSnapshotPrefix) {
			return fmt.Errorf("Actual volume snapshot %q in pool %q doesn't have the snapshot prefix", newSnapVolName, snapVol.Pool())
		}

		// We can easily derive the name of the existing snapshot volume by removing the prefix.
		oldSnapVolName := strings.TrimPrefix(newSnapVolName, powerFlexSnapshotPrefix)

		// After having derived the old name, we can get the actual snapshot volume's ID on storage.
		volumeID, err := powerFlexDriver.client().getVolumeID(oldSnapVolName)
		if err != nil {
			// If the volume doesn't anymore exist under its old name (without prefix),
			// it has to exist under its new name.
			// This might be required in scenarios where we want to reapply the patch due to other errors.
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				// Check if the volume is present under the new name already.
				// If not this can be considered an error.
				_, err := powerFlexDriver.client().getVolumeID(newSnapVolName)
				if err == nil {
					continue
				}

				return fmt.Errorf("Volume snapshot %q in pool %q cannot be found using its old or new name", snapVol.Name(), snapVol.Pool())
			}

			return err
		}

		// Using the snapshot volume's ID we can set the new patched name including the prefix.
		err = powerFlexDriver.client().renameVolume(volumeID, newSnapVolName)
		if err != nil {
			return err
		}
	}

	return nil
}
