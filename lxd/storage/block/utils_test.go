package block

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_NormalizeWWN(t *testing.T) {
	tests := []struct {
		Name string
		WWN  string
		Want string
	}{
		{
			Name: "Uppercase without separators",
			WWN:  "10000000C9A1B2C3",
			Want: "10000000c9a1b2c3",
		},
		{
			Name: "Colon-separated byte format",
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
			Name: "New line",
			WWN:  "0x210034800D7035B3\n",
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
			assert.Equal(t, test.Want, NormalizeWWN(test.WWN))
		})
	}
}
