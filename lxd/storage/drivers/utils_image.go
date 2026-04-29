package drivers

import (
	"errors"

	"github.com/canonical/lxd/shared/ioprogress"
)

// imageVolumeConfigMatchesPoolDefault checks whether the instance volume's effective config
// is compatible with the pool's current defaults for image volumes. If the configs are
// incompatible, there is no point unpacking the image into a dedicated on-disk image
// volume because the driver would not be able to clone from it anyway.
func imageVolumeConfigMatchesPoolDefault(vol Volume) (bool, error) {
	// Configs are cloned because FillVolumeConfig mutates in place.
	instImgVol := NewVolume(vol.driver, vol.pool, VolumeTypeImage, vol.contentType, vol.name, maps.Clone(vol.config), maps.Clone(vol.poolConfig))
	err := vol.driver.FillVolumeConfig(instImgVol)
	if err != nil {
		return false, err
	}

	poolDefaultVol := NewVolume(vol.driver, vol.pool, VolumeTypeImage, vol.contentType, vol.name, make(map[string]string), maps.Clone(vol.poolConfig))
	err = vol.driver.FillVolumeConfig(poolDefaultVol)
	if err != nil {
		return false, err
	}

	return vol.driver.ImageVolumeConfigMatch(instImgVol, poolDefaultVol), nil
}

// CanUseOptimizedImage reports whether the driver supports optimized image volumes and
// the instance volume's config is compatible with the pool's image-volume defaults.
// Both conditions must hold: if the driver doesn't cache images at all, or if the
// instance's config would prevent cloning from a cached image, there's no benefit in
// maintaining a separate image volume.
func CanUseOptimizedImage(vol Volume) (bool, error) {
	// vol is passed by value so it is never nil, but its driver field may be unset.
	if vol.driver == nil {
		return false, errors.New("Volume has no associated driver")
	}

	if !vol.driver.Info().OptimizedImages {
		return false, nil
	}

	return imageVolumeConfigMatchesPoolDefault(vol)
}

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
