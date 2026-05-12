package drivers

import (
	"errors"
	"maps"

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

// ensureImageVolume materialises the given image volume on disk for drivers
// that keep one cached image volume per fingerprint. If the volume already
// exists, the helper enforces the single-variant invariant: the on-disk
// config must match pool defaults, otherwise ErrImageVariantNotSupported is
// returned so the caller can slow-unpack a per-instance volume instead of
// replacing a shared image.
func ensureImageVolume(imgVol Volume, filler *VolumeFiller, progressReporter ioprogress.ProgressReporter) error {
	if imgVol.driver == nil {
		return errors.New("Volume has no associated driver")
	}

	exists, err := imgVol.driver.HasVolume(imgVol)
	if err != nil {
		return err
	}

	if exists {
		poolDefault := NewVolume(imgVol.driver, imgVol.pool, imgVol.volType, imgVol.contentType, imgVol.name, map[string]string{}, imgVol.poolConfig)
		err = imgVol.driver.FillVolumeConfig(poolDefault)
		if err != nil {
			return err
		}

		if !imgVol.driver.ImageVolumeConfigMatch(poolDefault, imgVol) {
			return ErrImageVariantNotSupported
		}

		return nil
	}

	return imgVol.driver.CreateVolume(imgVol, filler, progressReporter)
}
