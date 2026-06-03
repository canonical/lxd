package drivers

import (
	"errors"

	"github.com/canonical/lxd/shared/ioprogress"
)

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
