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

func Test_formatFCWWN(t *testing.T) {
	tests := []struct {
		name string
		wwn  string
		want string
	}{
		{
			name: "Colon-separated WWN is converted into 0x prefixed form",
			wwn:  "58:cc:f0:90:cb:20:03:46",
			want: "0x58ccf090cb200346",
		},
		{
			name: "Uppercase WWN is normalized to lowercase",
			wwn:  "58:CC:F0:90:CB:20:03:46",
			want: "0x58ccf090cb200346",
		},
		{
			name: "WWN that is already in the expected form is returned unchanged",
			wwn:  "0x58ccf090cb200346",
			want: "0x58ccf090cb200346",
		},
		{
			name: "Plain hex WWN without separators is prefixed with 0x",
			wwn:  "58ccf090cb200346",
			want: "0x58ccf090cb200346",
		},
		{
			name: "WWN with surrounding whitespace is normalized",
			wwn:  "  58:cc:f0:90:cb:20:03:46  ",
			want: "0x58ccf090cb200346",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, formatFCWWN(test.wwn))
		})
	}
}
