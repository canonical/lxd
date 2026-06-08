package connectors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_normalizeWWPN(t *testing.T) {
	tests := []struct {
		name string
		wwpn string
		want string
	}{
		{
			name: "Linux sysfs format with 0x prefix",
			wwpn: "0x210034800d7035b3",
			want: "210034800d7035b3",
		},
		{
			name: "Colon-separated byte format",
			wwpn: "21:00:34:80:0d:70:35:b3",
			want: "210034800d7035b3",
		},
		{
			name: "Uppercase WWPN",
			wwpn: "0x210034800D7035B3",
			want: "210034800d7035b3",
		},
		{
			name: "Surrounding whitespace",
			wwpn: "  0x210034800d7035b3  ",
			want: "210034800d7035b3",
		},
		{
			name: "Plain hex without prefix or separators",
			wwpn: "210034800d7035b3",
			want: "210034800d7035b3",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, normalizeWWPN(test.wwpn))
		})
	}
}
