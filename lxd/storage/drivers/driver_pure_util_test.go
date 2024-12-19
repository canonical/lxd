package drivers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_pure_serverName(t *testing.T) {
	// newTestVol creates a new Volume with the given UUID, VolumeType and ContentType.
	newTestVol := func(volName string, volType VolumeType, contentType ContentType, uuid string) Volume {
		config := map[string]string{
			"volatile.uuid": uuid,
		}

		return NewVolume(nil, "testpool", volType, contentType, volName, config, nil)
	}

	tests := []struct {
		Name        string
		Volume      Volume
		WantVolName string
		WantError   string
	}{
		{
			Name:      "Incorrect UUID length",
			Volume:    newTestVol("vol-err-1", VolumeTypeContainer, ContentTypeFS, "uuid"),
			WantError: "invalid UUID length: 4",
		},
		{
			Name:      "Invalid UUID format",
			Volume:    newTestVol("vol-err-2", VolumeTypeContainer, ContentTypeFS, "abcdefgh-1234-abcd-1234-abcdefgh"),
			WantError: "invalid UUID format",
		},
		{
			Name:        "Container FS",
			Volume:      newTestVol("c-fs", VolumeTypeContainer, ContentTypeFS, "a5289556-c903-409a-8aa0-4af18a46738d"),
			WantVolName: "c-a5289556c903409a8aa04af18a46738d",
		},
		{
			Name:        "VM FS",
			Volume:      newTestVol("vm-fs", VolumeTypeVM, ContentTypeFS, "a5289556-c903-409a-8aa0-4af18a46738d"),
			WantVolName: "v-a5289556c903409a8aa04af18a46738d",
		},
		{
			Name:        "VM Block",
			Volume:      newTestVol("vm-block", VolumeTypeVM, ContentTypeBlock, "a5289556-c903-409a-8aa0-4af18a46738d"),
			WantVolName: "v-a5289556c903409a8aa04af18a46738d-b",
		},
		{
			Name:        "Image FS",
			Volume:      newTestVol("img-fs", VolumeTypeImage, ContentTypeFS, "a5289556-c903-409a-8aa0-4af18a46738d"),
			WantVolName: "i-a5289556c903409a8aa04af18a46738d",
		},
		{
			Name:        "Image Block",
			Volume:      newTestVol("img-block", VolumeTypeImage, ContentTypeBlock, "a5289556-c903-409a-8aa0-4af18a46738d"),
			WantVolName: "i-a5289556c903409a8aa04af18a46738d-b",
		},
		{
			Name:        "Custom FS",
			Volume:      newTestVol("custom-fs", VolumeTypeCustom, ContentTypeFS, "a5289556-c903-409a-8aa0-4af18a46738d"),
			WantVolName: "u-a5289556c903409a8aa04af18a46738d",
		},
		{
			Name:        "Custom Block",
			Volume:      newTestVol("custom-block", VolumeTypeCustom, ContentTypeBlock, "a5289556-c903-409a-8aa0-4af18a46738d"),
			WantVolName: "u-a5289556c903409a8aa04af18a46738d-b",
		},
		{
			Name:        "Custom ISO",
			Volume:      newTestVol("custom-iso", VolumeTypeCustom, ContentTypeISO, "a5289556-c903-409a-8aa0-4af18a46738d"),
			WantVolName: "u-a5289556c903409a8aa04af18a46738d-i",
		},
		{
			Name:        "Snapshot Container FS",
			Volume:      newTestVol("c-fs/snap0", VolumeTypeContainer, ContentTypeFS, "fd87f109-767d-4f2f-ae18-66c34276f351"),
			WantVolName: "sc-fd87f109767d4f2fae1866c34276f351",
		},
		{
			Name:        "Snapshot VM FS",
			Volume:      newTestVol("vm-fs/snap0", VolumeTypeVM, ContentTypeFS, "fd87f109-767d-4f2f-ae18-66c34276f351"),
			WantVolName: "sv-fd87f109767d4f2fae1866c34276f351",
		},
		{
			Name:        "Snapshot VM Block",
			Volume:      newTestVol("vm-block/snap0", VolumeTypeVM, ContentTypeBlock, "fd87f109-767d-4f2f-ae18-66c34276f351"),
			WantVolName: "sv-fd87f109767d4f2fae1866c34276f351-b",
		},
		{
			Name:        "Snapshot Custom Block",
			Volume:      newTestVol("custom-block/snap0", VolumeTypeCustom, ContentTypeBlock, "fd87f109-767d-4f2f-ae18-66c34276f351"),
			WantVolName: "su-fd87f109767d4f2fae1866c34276f351-b",
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			d := &pure{}

			volName, err := d.getVolumeName(test.Volume)
			if err != nil {
				if test.WantError != "" {
					assert.ErrorContains(t, err, test.WantError)
				} else {
					t.Errorf("pure.getVolumeName() unexpected error: %v", err)
				}
			} else {
				if test.WantError != "" {
					t.Errorf("pure.getVolumeName() expected error %q, but got none", err)
				} else {
					assert.Equal(t, test.WantVolName, volName)
				}
			}
		})
	}
}
