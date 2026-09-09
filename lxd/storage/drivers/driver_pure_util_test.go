package drivers

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/storage/connectors"
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

func Test_pureNormalizeWWN(t *testing.T) {
	tests := []struct {
		Name string
		WWN  string
		Want string
	}{
		{
			Name: "Uppercase without separators, as reported by Pure Storage hosts",
			WWN:  "10000000C9A1B2C3",
			Want: "10000000c9a1b2c3",
		},
		{
			Name: "Colon-separated byte format, as reported by Pure Storage ports",
			WWN:  "21:00:34:80:0d:70:35:b3",
			Want: "210034800d7035b3",
		},
		{
			Name: "Linux sysfs format with 0x prefix",
			WWN:  "0x210034800d7035b3",
			Want: "210034800d7035b3",
		},
		{
			Name: "Surrounding whitespace",
			WWN:  "  0x210034800D7035B3  ",
			Want: "210034800d7035b3",
		},
		{
			Name: "Plain hex without prefix or separators",
			WWN:  "210034800d7035b3",
			Want: "210034800d7035b3",
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			assert.Equal(t, test.Want, pureNormalizeWWN(test.WWN))
		})
	}
}

func Test_pureHost_matchesQualifiedName(t *testing.T) {
	host := pureHost{
		Name: "server01-scsi-fc",
		IQNs: []string{"iqn.2005-03.org.open-iscsi:abcdef123456"},
		NQNs: []string{"nqn.2014-08.org.nvmexpress:uuid:abcdef12-3456-7890-abcd-ef1234567890"},
		WWNs: []string{"10000000C9A1B2C3", "10000000C9A1B2C4"},
	}

	tests := []struct {
		Name string
		Mode string
		QN   string
		Want bool
	}{
		{
			Name: "iSCSI IQN match",
			Mode: connectors.TypeISCSI,
			QN:   "iqn.2005-03.org.open-iscsi:abcdef123456",
			Want: true,
		},
		{
			Name: "iSCSI IQN mismatch",
			Mode: connectors.TypeISCSI,
			QN:   "iqn.2005-03.org.open-iscsi:000000000000",
			Want: false,
		},
		{
			Name: "NVMe/TCP NQN match",
			Mode: connectors.TypeNVMeTCP,
			QN:   "nqn.2014-08.org.nvmexpress:uuid:abcdef12-3456-7890-abcd-ef1234567890",
			Want: true,
		},
		{
			// The SCSI/FC connector reports the local initiator WWPN in lowercase,
			// whereas Pure Storage reports host WWNs in uppercase.
			Name: "SCSI/FC WWN match despite differing case",
			Mode: connectors.TypeSCSIFC,
			QN:   "10000000c9a1b2c3",
			Want: true,
		},
		{
			Name: "SCSI/FC WWN match on second WWN",
			Mode: connectors.TypeSCSIFC,
			QN:   "10000000c9a1b2c4",
			Want: true,
		},
		{
			Name: "SCSI/FC WWN match in colon-separated format",
			Mode: connectors.TypeSCSIFC,
			QN:   "10:00:00:00:c9:a1:b2:c3",
			Want: true,
		},
		{
			Name: "SCSI/FC WWN mismatch",
			Mode: connectors.TypeSCSIFC,
			QN:   "10000000c9a1b2ff",
			Want: false,
		},
		{
			Name: "Qualified name of another mode does not match",
			Mode: connectors.TypeSCSIFC,
			QN:   "iqn.2005-03.org.open-iscsi:abcdef123456",
			Want: false,
		},
		{
			Name: "Unsupported mode never matches",
			Mode: "unsupported",
			QN:   "10000000c9a1b2c3",
			Want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			assert.Equal(t, test.Want, host.matchesQualifiedName(test.Mode, test.QN))
		})
	}
}

func Test_pureDiskSuffix(t *testing.T) {
	// A Pure Storage volume serial number is always 24 characters long.
	const serial = "8726B5033AF2433D00014196"

	tests := []struct {
		Name      string
		Mode      string
		Serial    string
		Want      string
		WantError string
	}{
		{
			// iSCSI addresses the volume as a SCSI device, whose device identifier
			// is the volume serial number.
			Name:   "iSCSI uses the serial number verbatim",
			Mode:   connectors.TypeISCSI,
			Serial: serial,
			Want:   serial,
		},
		{
			// Fibre Channel is a SCSI transport and therefore behaves as iSCSI does.
			Name:   "SCSI/FC uses the serial number verbatim",
			Mode:   connectors.TypeSCSIFC,
			Serial: serial,
			Want:   serial,
		},
		{
			// NVMe embeds the Pure Storage OUI in the middle of the serial number.
			Name:   "NVMe/TCP embeds the OUI in the device identifier",
			Mode:   connectors.TypeNVMeTCP,
			Serial: serial,
			Want:   "008726B5033AF24324a9373D00014196",
		},
		{
			Name:      "Serial number that is too short",
			Mode:      connectors.TypeSCSIFC,
			Serial:    "8726B5033AF243",
			WantError: `Unexpected length of serial number "8726B5033AF243" (14)`,
		},
		{
			Name:      "Empty serial number",
			Mode:      connectors.TypeISCSI,
			Serial:    "",
			WantError: `Unexpected length of serial number "" (0)`,
		},
		{
			Name:      "Unsupported mode",
			Mode:      "unsupported",
			Serial:    serial,
			WantError: `Unsupported Pure Storage mode "unsupported"`,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			diskSuffix, err := pureDiskSuffix(test.Mode, test.Serial)
			if test.WantError != "" {
				assert.EqualError(t, err, test.WantError)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.Want, diskSuffix)
		})
	}
}

func Test_fcTargetWWNs(t *testing.T) {
	tests := []struct {
		Name  string
		Ports []purePort
		Want  []string
	}{
		{
			Name: "Fibre Channel port is selected",
			Ports: []purePort{
				{Name: "CT0.FC0", WWN: "52:4A:93:71:56:B8:6F:00"},
			},
			Want: []string{"524a937156b86f00"},
		},
		{
			// A port that reports an NQN alongside its WWN serves NVMe/FC and must
			// never be handed to the SCSI/FC connector.
			Name: "NVMe/FC port is excluded",
			Ports: []purePort{
				{Name: "CT0.FC1", WWN: "52:4A:93:71:56:B8:6F:01", NQN: "nqn.2010-06.com.purestorage:flasharray.1234"},
			},
			Want: []string{},
		},
		{
			Name: "iSCSI and NVMe/TCP ports are excluded",
			Ports: []purePort{
				{Name: "CT0.ETH0", IQN: "iqn.2010-06.com.purestorage:flasharray.1234"},
				{Name: "CT0.ETH1", NQN: "nqn.2010-06.com.purestorage:flasharray.1234"},
			},
			Want: []string{},
		},
		{
			Name: "Mixed ports keep only the SCSI/FC ones",
			Ports: []purePort{
				{Name: "CT0.FC0", WWN: "52:4A:93:71:56:B8:6F:00"},
				{Name: "CT0.FC1", WWN: "52:4A:93:71:56:B8:6F:01", NQN: "nqn.2010-06.com.purestorage:flasharray.1234"},
				{Name: "CT1.FC0", WWN: "52:4A:93:71:56:B8:6F:10"},
				{Name: "CT0.ETH0", IQN: "iqn.2010-06.com.purestorage:flasharray.1234"},
			},
			Want: []string{"524a937156b86f00", "524a937156b86f10"},
		},
		{
			Name: "Duplicate WWNs are reported once",
			Ports: []purePort{
				{Name: "CT0.FC0", WWN: "52:4A:93:71:56:B8:6F:00"},
				{Name: "CT0.FC0", WWN: "0x524a937156b86f00"},
			},
			Want: []string{"524a937156b86f00"},
		},
		{
			Name:  "No ports at all",
			Ports: []purePort{},
			Want:  []string{},
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			assert.Equal(t, test.Want, fcTargetWWNs(test.Ports))
		})
	}
}

func Test_pureConnection_unmarshal(t *testing.T) {
	// Responses as returned by the Pure Storage "connections" endpoint. The LUN is
	// required by the SCSI/FC connector to scope the SCSI bus rescan.
	tests := []struct {
		Name    string
		Body    string
		WantLUN int
		WantLen int
	}{
		{
			Name:    "Connection with an assigned LUN",
			Body:    `{"items":[{"lun":1,"host":{"name":"server01-scsi-fc"},"volume":{"name":"pool::vol"}}]}`,
			WantLUN: 1,
			WantLen: 1,
		},
		{
			Name:    "Connection with a high LUN",
			Body:    `{"items":[{"lun":4095}]}`,
			WantLUN: 4095,
			WantLen: 1,
		},
		{
			Name:    "Response without items",
			Body:    `{"items":[]}`,
			WantLen: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			var resp pureResponse[pureConnection]

			err := json.Unmarshal([]byte(test.Body), &resp)
			require.NoError(t, err)
			require.Len(t, resp.Items, test.WantLen)

			if test.WantLen > 0 {
				assert.Equal(t, test.WantLUN, resp.Items[0].LUN)
			}
		})
	}
}
