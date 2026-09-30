package drivers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
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

func Test_pureHost_matchesAnyQualifiedName(t *testing.T) {
	// A Fibre Channel host registers one WWPN per host bus adapter port on a single
	// Pure Storage host, so a match on any of them identifies the host.
	fcHost := pureHost{
		Name: "server01-scsi-fc",
		WWNs: []string{"21000024FF43B10C", "21000024FF43B10D"},
	}

	iscsiHost := pureHost{
		Name: "server01-iscsi",
		IQNs: []string{"iqn.2005-03.org.open-iscsi:abcdef123456"},
	}

	nvmeHost := pureHost{
		Name: "server01-nvme-tcp",
		NQNs: []string{"nqn.2014-08.org.nvmexpress:uuid:abcdef12-3456-7890-abcd-ef1234567890"},
	}

	tests := []struct {
		Name string
		Host pureHost
		Mode string
		QNs  []string
		Want bool
	}{
		{
			Name: "All local initiators registered",
			Host: fcHost,
			Mode: connectors.TypeSCSIFC,
			QNs:  []string{"21000024ff43b10c", "21000024ff43b10d"},
			Want: true,
		},
		{
			// Matters when a port is added after the host object was created.
			Name: "Only the second local initiator is registered",
			Host: fcHost,
			Mode: connectors.TypeSCSIFC,
			QNs:  []string{"21000024ff43b1ff", "21000024ff43b10d"},
			Want: true,
		},
		{
			// The reason enumeration order must not change host identity.
			Name: "Registration order does not matter",
			Host: fcHost,
			Mode: connectors.TypeSCSIFC,
			QNs:  []string{"21000024ff43b10d", "21000024ff43b10c"},
			Want: true,
		},
		{
			Name: "No local initiator is registered",
			Host: fcHost,
			Mode: connectors.TypeSCSIFC,
			QNs:  []string{"21000024ff43b1fe", "21000024ff43b1ff"},
			Want: false,
		},
		{
			Name: "Empty initiator list never matches",
			Host: fcHost,
			Mode: connectors.TypeSCSIFC,
			QNs:  []string{},
			Want: false,
		},
		{
			Name: "iSCSI IQN match",
			Host: iscsiHost,
			Mode: connectors.TypeISCSI,
			QNs:  []string{"iqn.2005-03.org.open-iscsi:abcdef123456"},
			Want: true,
		},
		{
			Name: "iSCSI IQN mismatch",
			Host: iscsiHost,
			Mode: connectors.TypeISCSI,
			QNs:  []string{"iqn.2005-03.org.open-iscsi:000000000000"},
			Want: false,
		},
		{
			Name: "NVMe/TCP NQN match",
			Host: nvmeHost,
			Mode: connectors.TypeNVMeTCP,
			QNs:  []string{"nqn.2014-08.org.nvmexpress:uuid:abcdef12-3456-7890-abcd-ef1234567890"},
			Want: true,
		},
		{
			Name: "NVMe/FC NQN match",
			Host: nvmeHost,
			Mode: connectors.TypeNVMeFC,
			QNs:  []string{"nqn.2014-08.org.nvmexpress:uuid:abcdef12-3456-7890-abcd-ef1234567890"},
			Want: true,
		},
		{
			Name: "NVMe NQN mismatch",
			Host: nvmeHost,
			Mode: connectors.TypeNVMeTCP,
			QNs:  []string{"nqn.2014-08.org.nvmexpress:uuid:00000000-0000-0000-0000-000000000000"},
			Want: false,
		},
		{
			Name: "Qualified name of another mode does not match",
			Host: iscsiHost,
			Mode: connectors.TypeNVMeTCP,
			QNs:  []string{"iqn.2005-03.org.open-iscsi:abcdef123456"},
			Want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			assert.Equal(t, test.Want, test.Host.matchesAnyQualifiedName(test.Mode, test.QNs))
		})
	}
}

func Test_pureHost_missingQualifiedNames(t *testing.T) {
	// A host object that was not created with the full set of initiators keeps matching
	// on the one it carries while missing the rest.
	host := pureHost{
		Name: "server01-scsi-fc",
		WWNs: []string{"21000024FF43B10C"},
	}

	tests := []struct {
		Name string
		Mode string
		QNs  []string
		Want []string
	}{
		{
			// An adapter added after the host object was created.
			Name: "Unregistered initiator is reported",
			Mode: connectors.TypeSCSIFC,
			QNs:  []string{"21000024ff43b10c", "21000024ff43b10d"},
			Want: []string{"21000024ff43b10d"},
		},
		{
			// A registered initiator must never be reported, or every mapping would
			// patch the host again.
			Name: "Registered initiator is not reported",
			Mode: connectors.TypeSCSIFC,
			QNs:  []string{"21000024ff43b10c"},
			Want: nil,
		},
		{
			// The array reports WWNs in uppercase and the connector in lowercase, so a
			// comparison that is not normalized would report every initiator as missing.
			Name: "Case does not make an initiator look missing",
			Mode: connectors.TypeSCSIFC,
			QNs:  []string{"21:00:00:24:FF:43:B1:0C"},
			Want: nil,
		},
		{
			Name: "All initiators unregistered",
			Mode: connectors.TypeSCSIFC,
			QNs:  []string{"21000024ff43b1fe", "21000024ff43b1ff"},
			Want: []string{"21000024ff43b1fe", "21000024ff43b1ff"},
		},
		{
			Name: "Empty initiator list reports nothing",
			Mode: connectors.TypeSCSIFC,
			QNs:  []string{},
			Want: nil,
		},
		{
			// A host carries one protocol, so an iSCSI IQN is missing from a Fibre
			// Channel host. The mode is what keeps the two from being compared.
			Name: "Other transports are reported against their own field",
			Mode: connectors.TypeISCSI,
			QNs:  []string{"iqn.2005-03.org.open-iscsi:abcdef123456"},
			Want: []string{"iqn.2005-03.org.open-iscsi:abcdef123456"},
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			assert.Equal(t, test.Want, host.missingQualifiedNames(test.Mode, test.QNs))
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

func Test_nvmeFCTargets(t *testing.T) {
	const subsystemNQN = "nqn.2010-06.com.purestorage:flasharray.1234"

	tests := []struct {
		Name      string
		Ports     []purePort
		WantNQN   string
		WantAddrs []string
	}{
		{
			// An NVMe/FC port reports both an NQN and a WWN. On Pure Storage the target
			// node name equals the port WWN, so both halves of the address match.
			Name: "NVMe/FC port yields nn/pn address from a single WWN",
			Ports: []purePort{
				{Name: "CT0.FC1", WWN: "52:4A:93:71:56:B8:6F:01", NQN: subsystemNQN},
			},
			WantNQN:   subsystemNQN,
			WantAddrs: []string{"nn-0x524a937156b86f01:pn-0x524a937156b86f01"},
		},
		{
			// The inverse of the SCSI/FC filter: a WWN with no NQN is a SCSI/FC port.
			Name: "SCSI/FC port is excluded",
			Ports: []purePort{
				{Name: "CT0.FC0", WWN: "52:4A:93:71:56:B8:6F:00"},
			},
			WantNQN:   "",
			WantAddrs: []string{},
		},
		{
			Name: "NVMe/TCP and iSCSI ports are excluded",
			Ports: []purePort{
				{Name: "CT0.ETH0", IQN: "iqn.2010-06.com.purestorage:flasharray.1234"},
				{Name: "CT0.ETH1", NQN: subsystemNQN},
			},
			WantNQN:   "",
			WantAddrs: []string{},
		},
		{
			Name: "Mixed ports keep only the NVMe/FC ones",
			Ports: []purePort{
				{Name: "CT0.FC0", WWN: "52:4A:93:71:56:B8:6F:00"},
				{Name: "CT0.FC1", WWN: "52:4A:93:71:56:B8:6F:01", NQN: subsystemNQN},
				{Name: "CT1.FC1", WWN: "52:4A:93:71:56:B8:6F:11", NQN: subsystemNQN},
				{Name: "CT0.ETH0", IQN: "iqn.2010-06.com.purestorage:flasharray.1234"},
			},
			WantNQN: subsystemNQN,
			WantAddrs: []string{
				"nn-0x524a937156b86f01:pn-0x524a937156b86f01",
				"nn-0x524a937156b86f11:pn-0x524a937156b86f11",
			},
		},
		{
			Name: "Duplicate ports are reported once",
			Ports: []purePort{
				{Name: "CT0.FC1", WWN: "52:4A:93:71:56:B8:6F:01", NQN: subsystemNQN},
				{Name: "CT0.FC1", WWN: "0x524a937156b86f01", NQN: subsystemNQN},
			},
			WantNQN:   subsystemNQN,
			WantAddrs: []string{"nn-0x524a937156b86f01:pn-0x524a937156b86f01"},
		},
		{
			// A Pure Storage array exposes one subsystem, so this should not occur. If it
			// ever does, the addresses returned must still all serve the returned NQN -
			// otherwise the connector would be given an address that cannot reach the
			// subsystem it was told to connect to.
			Name: "Ports of another subsystem are excluded",
			Ports: []purePort{
				{Name: "CT0.FC1", WWN: "52:4A:93:71:56:B8:6F:01", NQN: subsystemNQN},
				{Name: "CT1.FC1", WWN: "52:4A:93:71:56:B8:6F:11", NQN: "nqn.2010-06.com.purestorage:flasharray.5678"},
			},
			WantNQN:   subsystemNQN,
			WantAddrs: []string{"nn-0x524a937156b86f01:pn-0x524a937156b86f01"},
		},
		{
			Name:      "No ports at all",
			Ports:     []purePort{},
			WantNQN:   "",
			WantAddrs: []string{},
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			nqn, addrs := nvmeFCTargets(test.Ports)
			assert.Equal(t, test.WantNQN, nqn)
			assert.Equal(t, test.WantAddrs, addrs)
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
			// Pure Storage assigns LUNs from 1 to 4095, so a response without a
			// "lun" unmarshals to 0, a value the array never assigns. That is why
			// connectHostToVolume rejects a non-positive LUN in scsi/fc mode.
			Name:    "Connection without a LUN, as reported for the NVMe modes",
			Body:    `{"items":[{"host":{"name":"server01-nvme-fc"},"volume":{"name":"pool::vol"}}]}`,
			WantLUN: 0,
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

func Test_pureError_Error(t *testing.T) {
	newErr := func(messages ...string) *pureError {
		perr := &pureError{}
		for _, message := range messages {
			perr.Errors = append(perr.Errors, struct {
				Context string `json:"context"`
				Message string `json:"message"`
			}{Message: message})
		}

		return perr
	}

	tests := []struct {
		Name string
		Err  *pureError
		Want string
	}{
		{
			Name: "Nil error",
			Err:  nil,
			Want: "",
		},
		{
			Name: "No errors reported",
			Err:  newErr(),
			Want: "",
		},
		{
			Name: "Trailing dot is removed",
			Err:  newErr("Volume does not exist."),
			Want: "Volume does not exist",
		},
		{
			Name: "Message without a trailing dot is kept as is",
			Err:  newErr("Volume does not exist"),
			Want: "Volume does not exist",
		},
		{
			Name: "Only the first message is reported",
			Err:  newErr("First failure.", "Second failure."),
			Want: "First failure",
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			assert.Equal(t, test.Want, test.Err.Error())
		})
	}
}

func Test_isPureErrorOf(t *testing.T) {
	newErr := func(statusCode int, messages ...string) *pureError {
		perr := &pureError{statusCode: statusCode}
		for _, message := range messages {
			perr.Errors = append(perr.Errors, struct {
				Context string `json:"context"`
				Message string `json:"message"`
			}{Message: message})
		}

		return perr
	}

	tests := []struct {
		Name       string
		Err        error
		StatusCode int
		Substrings []string
		Want       bool
	}{
		{
			Name:       "Nil error",
			Err:        nil,
			StatusCode: http.StatusBadRequest,
			Want:       false,
		},
		{
			Name:       "Error of another type",
			Err:        errors.New("Not found"),
			StatusCode: http.StatusBadRequest,
			Substrings: []string{"Not found"},
			Want:       false,
		},
		{
			Name:       "Wrapped Pure Storage error",
			Err:        fmt.Errorf("Failed getting volume: %w", newErr(http.StatusBadRequest, "Volume not found.")),
			StatusCode: http.StatusBadRequest,
			Substrings: []string{"not found"},
			Want:       true,
		},
		{
			Name:       "Wrapped Pure Storage error with another status code",
			Err:        fmt.Errorf("Failed getting volume: %w", newErr(http.StatusUnauthorized, "Volume not found.")),
			StatusCode: http.StatusBadRequest,
			Substrings: []string{"not found"},
			Want:       false,
		},
		{
			Name:       "Wrapped error of another type",
			Err:        fmt.Errorf("Failed getting volume: %w", errors.New("Not found")),
			StatusCode: http.StatusBadRequest,
			Substrings: []string{"Not found"},
			Want:       false,
		},
		{
			Name:       "Status code match without substrings",
			Err:        newErr(http.StatusBadRequest, "Anything"),
			StatusCode: http.StatusBadRequest,
			Want:       true,
		},
		{
			Name:       "Status code mismatch",
			Err:        newErr(http.StatusUnauthorized, "Not found"),
			StatusCode: http.StatusBadRequest,
			Substrings: []string{"Not found"},
			Want:       false,
		},
		{
			Name:       "Substring match ignores case",
			Err:        newErr(http.StatusBadRequest, "Volume DOES NOT EXIST."),
			StatusCode: http.StatusBadRequest,
			Substrings: []string{"does not exist"},
			Want:       true,
		},
		{
			Name:       "Any of the substrings is enough",
			Err:        newErr(http.StatusBadRequest, "Host not found"),
			StatusCode: http.StatusBadRequest,
			Substrings: []string{"does not exist", "not found"},
			Want:       true,
		},
		{
			Name:       "Any of the messages is enough",
			Err:        newErr(http.StatusBadRequest, "Unrelated failure", "Host not found"),
			StatusCode: http.StatusBadRequest,
			Substrings: []string{"not found"},
			Want:       true,
		},
		{
			Name:       "No substring matches",
			Err:        newErr(http.StatusBadRequest, "Unrelated failure"),
			StatusCode: http.StatusBadRequest,
			Substrings: []string{"not found", "does not exist"},
			Want:       false,
		},
		{
			Name:       "Substrings given but no messages reported",
			Err:        newErr(http.StatusBadRequest),
			StatusCode: http.StatusBadRequest,
			Substrings: []string{"not found"},
			Want:       false,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			assert.Equal(t, test.Want, isPureErrorOf(test.Err, test.StatusCode, test.Substrings...))
		})
	}
}

func Test_isPureErrorNotFound(t *testing.T) {
	newErr := func(statusCode int, message string) *pureError {
		perr := &pureError{statusCode: statusCode}
		perr.Errors = append(perr.Errors, struct {
			Context string `json:"context"`
			Message string `json:"message"`
		}{Message: message})

		return perr
	}

	tests := []struct {
		Name string
		Err  error
		Want bool
	}{
		{
			Name: "Not found",
			Err:  newErr(http.StatusBadRequest, "Volume not found."),
			Want: true,
		},
		{
			Name: "Does not exist",
			Err:  newErr(http.StatusBadRequest, "Host does not exist."),
			Want: true,
		},
		{
			Name: "No such volume or snapshot",
			Err:  newErr(http.StatusBadRequest, "No such volume or snapshot: pool::vol."),
			Want: true,
		},
		{
			// A missing resource is reported as a bad request, never as HTTP 404.
			Name: "Not found message with another status code",
			Err:  newErr(http.StatusNotFound, "Volume not found."),
			Want: false,
		},
		{
			Name: "Bad request for another reason",
			Err:  newErr(http.StatusBadRequest, "Volume already exists."),
			Want: false,
		},
		{
			Name: "Error of another type",
			Err:  errors.New("Volume not found"),
			Want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			assert.Equal(t, test.Want, isPureErrorNotFound(test.Err))
		})
	}
}

func Test_pure_getUUIDFromVolumeName(t *testing.T) {
	tests := []struct {
		Name      string
		VolName   string
		Want      string
		WantError string
	}{
		{
			Name:    "UUID without hyphens",
			VolName: "a5289556c903409a8aa04af18a46738d",
			Want:    "a5289556-c903-409a-8aa0-4af18a46738d",
		},
		{
			Name:    "Uppercase UUID",
			VolName: "A5289556C903409A8AA04AF18A46738D",
			Want:    "a5289556-c903-409a-8aa0-4af18a46738d",
		},
		{
			Name:      "Empty name",
			VolName:   "",
			WantError: `Failed parsing UUID from volume name "": `,
		},
		{
			Name:      "Name that is too short",
			VolName:   "a5289556c903409a",
			WantError: `Failed parsing UUID from volume name "a5289556c903409a": `,
		},
		{
			Name:      "Non-hexadecimal characters",
			VolName:   "g5289556c903409a8aa04af18a46738d",
			WantError: `Failed parsing UUID from volume name "g5289556c903409a8aa04af18a46738d": `,
		},
		{
			// The caller is expected to strip the type prefix and content type suffix.
			Name:      "Name with type prefix",
			VolName:   "c-a5289556c903409a8aa04af18a46738d",
			WantError: `Failed parsing UUID from volume name "c-a5289556c903409a8aa04af18a46738d": `,
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			d := &pure{}

			volUUID, err := d.getUUIDFromVolumeName(test.VolName)
			if test.WantError != "" {
				assert.ErrorContains(t, err, test.WantError)
				assert.Equal(t, uuid.Nil, volUUID)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.Want, volUUID.String())
		})
	}
}

func Test_pure_getVolumeName_roundTrip(t *testing.T) {
	const volUUID = "a5289556-c903-409a-8aa0-4af18a46738d"

	d := &pure{}

	vol := NewVolume(nil, "testpool", VolumeTypeCustom, ContentTypeFS, "custom-fs", map[string]string{"volatile.uuid": volUUID}, nil)

	volName, err := d.getVolumeName(vol)
	require.NoError(t, err)

	// Strip the type prefix that getVolumeName prepends.
	parsedUUID, err := d.getUUIDFromVolumeName(strings.TrimPrefix(volName, pureVolTypePrefixes[VolumeTypeCustom]+"-"))
	require.NoError(t, err)
	assert.Equal(t, volUUID, parsedUUID.String())
}
