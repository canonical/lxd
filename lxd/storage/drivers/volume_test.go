package drivers

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test Volume_ConfigSizeFromSource.
func Test_Volume_ConfigSizeFromSource(t *testing.T) {
	nonBlockBackedDriver := dir{}
	blockBackedDriver := lvm{}

	tests := []struct {
		vol    Volume
		srcVol Volume
		err    error
		size   string
	}{
		{
			// Check the volume's size is used when empty non-image source volume used.
			vol:    Volume{driver: &nonBlockBackedDriver, volType: VolumeTypeContainer, config: map[string]string{"size": "1GB"}},
			srcVol: Volume{},
			err:    nil,
			size:   "1GB",
		},
		{
			// Check the volume's pool volume.size isn't used when empty non-image source volume used.
			vol:    Volume{driver: &nonBlockBackedDriver, volType: VolumeTypeContainer, poolConfig: map[string]string{"volume.size": "2GB"}},
			srcVol: Volume{},
			err:    nil,
			size:   "",
		},
		{
			// Check the volume's pool volume.size is used when volume size not specified and empty
			// image source volume used.
			vol:    Volume{driver: &nonBlockBackedDriver, volType: VolumeTypeContainer, poolConfig: map[string]string{"volume.size": "2GB"}},
			srcVol: Volume{volType: VolumeTypeImage},
			err:    nil,
			size:   "2GB",
		},
		{
			// Check the volume's default block disk size is used when volume is a block type and
			// neighter volume or pool volume size is specified and empty image source volume used.
			vol:    Volume{driver: &nonBlockBackedDriver, volType: VolumeTypeVM, contentType: ContentTypeBlock},
			srcVol: Volume{volType: VolumeTypeImage},
			err:    nil,
			size:   defaultBlockSize,
		},
		{
			// Check that the volume's smaller size than source image's rootfs size causes error.
			vol:    Volume{driver: &nonBlockBackedDriver, volType: VolumeTypeContainer, config: map[string]string{"size": "1GB"}},
			srcVol: Volume{volType: VolumeTypeImage, config: map[string]string{"volatile.rootfs.size": "15GB"}},
			err:    fmt.Errorf("Source image size (15000000000) exceeds specified volume size (1000005632)"),
			size:   "",
		},
		{
			// Check that the volume's larger size than source image's rootfs size overrides.
			vol:    Volume{driver: &nonBlockBackedDriver, volType: VolumeTypeContainer, config: map[string]string{"size": "20GB"}},
			srcVol: Volume{volType: VolumeTypeImage, config: map[string]string{"volatile.rootfs.size": "15GB"}},
			err:    nil,
			size:   "20GB",
		},
		{
			// Check returned size is empty when the container volume/pool doesn't specify a size and
			// the pool is not block backed and the volume is container & fs.
			vol:    Volume{driver: &nonBlockBackedDriver, volType: VolumeTypeContainer, contentType: ContentTypeFS, config: map[string]string{}},
			srcVol: Volume{volType: VolumeTypeImage, config: map[string]string{"volatile.rootfs.size": "15GB"}},
			err:    nil,
			size:   "",
		},
		{
			// Check returned size is empty when the container volume/pool doesn't specify a size and
			// the pool is not block backed and the volume is VM & block.
			vol:    Volume{driver: &nonBlockBackedDriver, volType: VolumeTypeVM, contentType: ContentTypeBlock, config: map[string]string{}},
			srcVol: Volume{volType: VolumeTypeImage, config: map[string]string{"volatile.rootfs.size": "15GB"}},
			err:    nil,
			size:   "15GB",
		},
		{
			// Check returned size is source size when the VM volume/pool doesn't specify a size and
			// the pool is block backed, and the source size is larger than default block disk size.
			vol:    Volume{driver: &blockBackedDriver, volType: VolumeTypeVM, config: map[string]string{}},
			srcVol: Volume{volType: VolumeTypeImage, config: map[string]string{"volatile.rootfs.size": "15GB"}},
			err:    nil,
			size:   "15GB",
		},
		{
			// Check returned size is default block disk size when the VM volume/pool doesn't specify a
			// size and the pool is block backed, and the source size is smaller than default block
			// disk size.
			vol:    Volume{driver: &blockBackedDriver, volType: VolumeTypeVM, config: map[string]string{}},
			srcVol: Volume{volType: VolumeTypeImage, config: map[string]string{"volatile.rootfs.size": "5GB"}},
			err:    nil,
			size:   defaultBlockSize,
		},
		{
			// Check volume's size is used when VM filesystem volume is supplied with image source.
			vol:    Volume{driver: &nonBlockBackedDriver, volType: VolumeTypeVM, contentType: ContentTypeFS, config: map[string]string{"size": "50MB"}},
			srcVol: Volume{volType: VolumeTypeImage},
			err:    nil,
			size:   "50MB",
		},
	}

	for _, test := range tests {
		size, err := test.vol.ConfigSizeFromSource(test.srcVol)
		assert.Equal(t, test.size, size)
		assert.Equal(t, test.err, err)
	}
}
