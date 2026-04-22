package drivers

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testUUIDString = "a5289556-c903-409a-8aa0-4af18a46738d"

func Test_powerstore_encodeVolumeName(t *testing.T) {
	// newTestVol creates a new Volume with the given UUID, VolumeType and ContentType.
	newTestVol := func(volName string, volType VolumeType, contentType ContentType, volUUID string) Volume {
		config := map[string]string{
			"volatile.uuid": volUUID,
		}

		return NewVolume(nil, "testpool", volType, contentType, volName, config, nil)
	}

	d := &powerstore{}
	prefix := d.storagePoolScopePrefix("testpool")

	tests := []struct {
		name        string
		volume      Volume
		wantVolName string
		wantError   string
	}{
		{
			name:      "Missing UUID",
			volume:    newTestVol("vol-err", VolumeTypeContainer, ContentTypeFS, ""),
			wantError: "invalid UUID length: 0",
		},
		{
			name:      "Invalid UUID",
			volume:    newTestVol("vol-err", VolumeTypeContainer, ContentTypeFS, "not-a-valid-uuid"),
			wantError: `Failed parsing "volatile.uuid" from volume`,
		},
		{
			name:        "Container FS",
			volume:      newTestVol("c1", VolumeTypeContainer, ContentTypeFS, testUUIDString),
			wantVolName: prefix + "c_" + testUUIDString,
		},
		{
			name:        "VM FS",
			volume:      newTestVol("vm1", VolumeTypeVM, ContentTypeFS, testUUIDString),
			wantVolName: prefix + "v_" + testUUIDString,
		},
		{
			name:        "VM Block",
			volume:      newTestVol("vm1", VolumeTypeVM, ContentTypeBlock, testUUIDString),
			wantVolName: prefix + "v_" + testUUIDString + ".b",
		},
		{
			name:        "Image FS",
			volume:      newTestVol("img1", VolumeTypeImage, ContentTypeFS, testUUIDString),
			wantVolName: prefix + "i_" + testUUIDString,
		},
		{
			name:        "Image Block",
			volume:      newTestVol("img1", VolumeTypeImage, ContentTypeBlock, testUUIDString),
			wantVolName: prefix + "i_" + testUUIDString + ".b",
		},
		{
			name:        "Custom FS",
			volume:      newTestVol("custom1", VolumeTypeCustom, ContentTypeFS, testUUIDString),
			wantVolName: prefix + "u_" + testUUIDString,
		},
		{
			name:        "Custom Block",
			volume:      newTestVol("custom1", VolumeTypeCustom, ContentTypeBlock, testUUIDString),
			wantVolName: prefix + "u_" + testUUIDString + ".b",
		},
		{
			name:        "Custom ISO",
			volume:      newTestVol("custom1", VolumeTypeCustom, ContentTypeISO, testUUIDString),
			wantVolName: prefix + "u_" + testUUIDString + ".i",
		},
		{
			name:        "Snapshot clone VM FS",
			volume:      newTestVol(powerStoreMountableSnapshotPrefix+testUUIDString, VolumeTypeVM, ContentTypeFS, testUUIDString),
			wantVolName: prefix + "sv_" + testUUIDString,
		},
		{
			name:        "Snapshot clone VM Block",
			volume:      newTestVol(powerStoreMountableSnapshotPrefix+testUUIDString, VolumeTypeVM, ContentTypeBlock, testUUIDString),
			wantVolName: prefix + "sv_" + testUUIDString + ".b",
		},
		{
			name:        "Snapshot clone Custom FS",
			volume:      newTestVol(powerStoreMountableSnapshotPrefix+testUUIDString, VolumeTypeCustom, ContentTypeFS, testUUIDString),
			wantVolName: prefix + "su_" + testUUIDString,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			volName, err := d.encodeVolumeName(tc.volume)
			if tc.wantError != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tc.wantError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantVolName, volName)
			}
		})
	}
}

func Test_powerstore_decodeVolumeName(t *testing.T) {
	testUUID := uuid.MustParse(testUUIDString)

	d := &powerstore{}
	prefix := d.storagePoolScopePrefix("testpool")

	tests := []struct {
		name                  string
		encodedName           string
		wantVolType           VolumeType
		wantVolUUID           uuid.UUID
		wantContentType       ContentType
		wantMountableSnapshot bool
		wantError             string
	}{
		{
			name:        "Missing LXD prefix",
			encodedName: "noprefix",
			wantError:   "Missing LXD prefix",
		},
		{
			name:        "Missing pool name separator",
			encodedName: "lxd-nopoolsep",
			wantError:   "Invalid name format",
		},
		{
			name:        "Empty pool name",
			encodedName: "lxd--volname",
			wantError:   "Invalid name format",
		},
		{
			name:        "Empty volume name",
			encodedName: "lxd-pool-",
			wantError:   "Invalid name format",
		},
		{
			name:        "Invalid UUID in volume name",
			encodedName: prefix + "c_not-a-uuid",
			wantError:   "Failed decoding volume name",
		},
		{
			name:            "Container FS",
			encodedName:     prefix + "c_" + testUUIDString,
			wantVolType:     VolumeTypeContainer,
			wantVolUUID:     testUUID,
			wantContentType: "",
		},
		{
			name:            "VM FS",
			encodedName:     prefix + "v_" + testUUIDString,
			wantVolType:     VolumeTypeVM,
			wantVolUUID:     testUUID,
			wantContentType: "",
		},
		{
			name:            "VM Block",
			encodedName:     prefix + "v_" + testUUIDString + ".b",
			wantVolType:     VolumeTypeVM,
			wantVolUUID:     testUUID,
			wantContentType: ContentTypeBlock,
		},
		{
			name:            "Image FS",
			encodedName:     prefix + "i_" + testUUIDString,
			wantVolType:     VolumeTypeImage,
			wantVolUUID:     testUUID,
			wantContentType: "",
		},
		{
			name:            "Image Block",
			encodedName:     prefix + "i_" + testUUIDString + ".b",
			wantVolType:     VolumeTypeImage,
			wantVolUUID:     testUUID,
			wantContentType: ContentTypeBlock,
		},
		{
			name:            "Custom FS",
			encodedName:     prefix + "u_" + testUUIDString,
			wantVolType:     VolumeTypeCustom,
			wantVolUUID:     testUUID,
			wantContentType: "",
		},
		{
			name:            "Custom Block",
			encodedName:     prefix + "u_" + testUUIDString + ".b",
			wantVolType:     VolumeTypeCustom,
			wantVolUUID:     testUUID,
			wantContentType: ContentTypeBlock,
		},
		{
			name:            "Custom ISO",
			encodedName:     prefix + "u_" + testUUIDString + ".i",
			wantVolType:     VolumeTypeCustom,
			wantVolUUID:     testUUID,
			wantContentType: ContentTypeISO,
		},
		{
			name:                  "Snapshot clone VM FS",
			encodedName:           prefix + "sv_" + testUUIDString,
			wantVolType:           VolumeTypeVM,
			wantVolUUID:           testUUID,
			wantContentType:       "",
			wantMountableSnapshot: true,
		},
		{
			name:                  "Snapshot clone VM Block",
			encodedName:           prefix + "sv_" + testUUIDString + ".b",
			wantVolType:           VolumeTypeVM,
			wantVolUUID:           testUUID,
			wantContentType:       ContentTypeBlock,
			wantMountableSnapshot: true,
		},
		{
			name:                  "Snapshot clone Custom FS",
			encodedName:           prefix + "su_" + testUUIDString,
			wantVolType:           VolumeTypeCustom,
			wantVolUUID:           testUUID,
			wantContentType:       "",
			wantMountableSnapshot: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			volType, volUUID, contentType, isMountableSnapshot, err := d.decodeVolumeName(tc.encodedName)
			if tc.wantError != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tc.wantError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantVolType, volType)
				assert.Equal(t, tc.wantVolUUID, volUUID)
				assert.Equal(t, tc.wantContentType, contentType)
				assert.Equal(t, tc.wantMountableSnapshot, isMountableSnapshot)
			}
		})
	}
}

func Test_powerstore_encodeDecodeRoundTrip(t *testing.T) {
	testUUIDParsed := uuid.MustParse(testUUIDString)

	newTestVol := func(volName string, volType VolumeType, contentType ContentType) Volume {
		config := map[string]string{
			"volatile.uuid": testUUIDString,
		}

		return NewVolume(nil, "testpool", volType, contentType, volName, config, nil)
	}

	d := &powerstore{}

	tests := []struct {
		name            string
		volume          Volume
		wantVolType     VolumeType
		wantContentType ContentType
	}{
		{
			name:            "Container FS",
			volume:          newTestVol("c1", VolumeTypeContainer, ContentTypeFS),
			wantVolType:     VolumeTypeContainer,
			wantContentType: "",
		},
		{
			name:            "VM Block",
			volume:          newTestVol("vm1", VolumeTypeVM, ContentTypeBlock),
			wantVolType:     VolumeTypeVM,
			wantContentType: ContentTypeBlock,
		},
		{
			name:            "Custom ISO",
			volume:          newTestVol("custom1", VolumeTypeCustom, ContentTypeISO),
			wantVolType:     VolumeTypeCustom,
			wantContentType: ContentTypeISO,
		},
		{
			name:            "Image Block",
			volume:          newTestVol("img1", VolumeTypeImage, ContentTypeBlock),
			wantVolType:     VolumeTypeImage,
			wantContentType: ContentTypeBlock,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := d.encodeVolumeName(tc.volume)
			require.NoError(t, err)

			volType, volUUID, contentType, isMountableSnapshot, err := d.decodeVolumeName(encoded)
			require.NoError(t, err)
			assert.Equal(t, tc.wantVolType, volType)
			assert.Equal(t, testUUIDParsed, volUUID)
			assert.Equal(t, tc.wantContentType, contentType)
			assert.False(t, isMountableSnapshot)
		})
	}
}
