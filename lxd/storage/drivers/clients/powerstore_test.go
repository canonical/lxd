package clients

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/lxd/storage/connectors"
)

func Test_formatQN(t *testing.T) {
	tests := []struct {
		name          string
		connectorType string
		qn            string
		want          string
	}{
		{
			name:          "Non-FC connector is returned unchanged",
			connectorType: connectors.TypeISCSI,
			qn:            "iqn.1993-08.org.debian:01:abcdef123456",
			want:          "iqn.1993-08.org.debian:01:abcdef123456",
		},
		{
			name:          "FC WWPN with 0x prefix is reformatted to colon-separated bytes",
			connectorType: connectors.TypeSCSIFC,
			qn:            "0x210034800d7035b3",
			want:          "21:00:34:80:0d:70:35:b3",
		},
		{
			name:          "FC WWPN already colon-separated is normalized to lowercase",
			connectorType: connectors.TypeSCSIFC,
			qn:            "21:00:34:80:0D:70:35:B3",
			want:          "21:00:34:80:0d:70:35:b3",
		},
		{
			name:          "FC WWPN plain hex without separators is reformatted",
			connectorType: connectors.TypeSCSIFC,
			qn:            "210034800d7035b3",
			want:          "21:00:34:80:0d:70:35:b3",
		},
		{
			name:          "FC WWPN with surrounding whitespace is reformatted",
			connectorType: connectors.TypeSCSIFC,
			qn:            "  0x210034800d7035b3  ",
			want:          "21:00:34:80:0d:70:35:b3",
		},
		{
			name:          "FC WWPN with unexpected length is returned unchanged",
			connectorType: connectors.TypeSCSIFC,
			qn:            "0x210034800d7035",
			want:          "0x210034800d7035",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, formatQN(test.connectorType, test.qn))
		})
	}
}

func Test_PowerStoreHost_MissingQualifiedNames(t *testing.T) {
	// A host object that was not created with the full set of initiators keeps matching
	// on the one it carries while missing the rest.
	host := PowerStoreHost{
		Name: "server01-scsi-fc",
		Initiators: []*PowerStoreHostInitiator{
			{PortName: "21:00:00:24:ff:43:b1:0c", PortType: "FC"},
		},
	}

	tests := []struct {
		name          string
		connectorType string
		qns           []string
		want          []string
	}{
		{
			// An adapter added after the host object was created.
			name:          "Unregistered initiator is reported",
			connectorType: connectors.TypeSCSIFC,
			qns:           []string{"21000024ff43b10c", "21000024ff43b10d"},
			want:          []string{"21000024ff43b10d"},
		},
		{
			// A registered initiator must never be reported, or every mapping would
			// patch the host again.
			name:          "Registered initiator is not reported",
			connectorType: connectors.TypeSCSIFC,
			qns:           []string{"21000024ff43b10c"},
			want:          nil,
		},
		{
			name:          "Port name is normalizied and registered initiator is not reported",
			connectorType: connectors.TypeSCSIFC,
			qns:           []string{"0x21000024FF43B10C"},
			want:          nil,
		},
		{
			name:          "All initiators unregistered and reported",
			connectorType: connectors.TypeSCSIFC,
			qns:           []string{"21000024ff43b1fe", "21000024ff43b1ff"},
			want:          []string{"21000024ff43b1fe", "21000024ff43b1ff"},
		},
		{
			name:          "Empty initiator list reports nothing",
			connectorType: connectors.TypeSCSIFC,
			qns:           []string{},
			want:          nil,
		},
		{
			name:          "Other transports are reported against their own port type",
			connectorType: connectors.TypeISCSI,
			qns:           []string{"iqn.2005-03.org.open-iscsi:abcdef123456"},
			want:          []string{"iqn.2005-03.org.open-iscsi:abcdef123456"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			missing, err := host.MissingQualifiedNames(test.connectorType, test.qns)
			assert.NoError(t, err)
			assert.Equal(t, test.want, missing)
		})
	}
}
