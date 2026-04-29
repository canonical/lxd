package drivers

import (
	"errors"

	"github.com/canonical/lxd/shared/ioprogress"
)

// createVolumeFromImage creates a new volume from an image. If imgVol is nil (no cached
// image volume is available), it falls back to unpacking the image directly into a new
// volume via the filler. Otherwise it verifies that the cached image volume can serve the
// target volume's effective config and clones from it, falling back to a direct unpack
// when the configs are incompatible or the image volume cannot be shrunk to the requested
// size.
func createVolumeFromImage(vol Volume, imgVol *Volume, filler *VolumeFiller, progressReporter ioprogress.ProgressReporter) error {
	// vol is passed by value so it is never nil, but its driver field may be unset.
	if vol.driver == nil {
		return errors.New("Volume has no associated driver")
	}

	if imgVol == nil {
		return vol.driver.CreateVolume(vol, filler, progressReporter)
	}

	// Drivers without per-config image variants cache a single image volume keyed on
	// pool defaults; if the target volume's effective config (pool defaults plus
	// initial.* overrides) needs a different filesystem or block-backing mode, the
	// cached image cannot serve it and the image is unpacked directly instead.
	if !vol.driver.ImageVolumeConfigMatch(*imgVol, vol) {
		return vol.driver.CreateVolume(vol, filler, progressReporter)
	}

	// Derive the volume size to use for the new volume when copying from the image volume.
	// Where possible (if the image volume has a volatile.rootfs.size property), it checks
	// that the image volume isn't larger than the volume's "size" and the pool's
	// "volume.size" setting.
	newVolSize, err := vol.ConfigSizeFromSource(*imgVol)
	if err != nil {
		return err
	}

	vol.SetConfigSize(newVolSize)

	// Clone the new volume from the cached image volume.
	err = vol.driver.CreateVolumeFromCopy(NewVolumeCopy(vol), NewVolumeCopy(*imgVol), false, progressReporter)
	if err != nil {
		if !errors.Is(err, ErrCannotBeShrunk) {
			return err
		}

		// The cached image volume is larger than the requested new volume size and cannot
		// be shrunk. Fall back to unpacking the image directly into a new volume. This is
		// slower but allows creating volumes smaller than the pool's volume settings.
		return vol.driver.CreateVolume(vol, filler, progressReporter)
	}

	return nil
}
