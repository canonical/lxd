package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHasBitField(t *testing.T) {
	field := []uint32{1 << 3, 0, 1 << 28}

	assert.True(t, hasBitField(field, 3))
	assert.False(t, hasBitField(field, 4))
	assert.False(t, hasBitField(field, 35))
	assert.True(t, hasBitField(field, 92))

	// Bits past the end of the field are reported as unset rather than panicking.
	assert.False(t, hasBitField(field, 96))
	assert.False(t, hasBitField(field, 124))
	assert.False(t, hasBitField(nil, 0))
}

func TestEthtoolModes(t *testing.T) {
	names := map[string]bool{}

	for i, mode := range ethtoolModes {
		if i > 0 {
			assert.Greater(t, mode.bit, ethtoolModes[i-1].bit, "Link modes must be sorted by bit without duplicates")
		}

		assert.NotEmpty(t, mode.name)
		assert.False(t, names[mode.name], "Duplicate link mode name %q", mode.name)
		names[mode.name] = true
	}
}

func TestEthtoolModesKernelMapping(t *testing.T) {
	modes := map[uint]string{}
	for _, mode := range ethtoolModes {
		modes[mode.bit] = mode.name
	}

	// First and last link mode of each range added after bit 48, as defined by the kernel's
	// enum ethtool_link_mode_bit_indices.
	expected := []ethtoolMode{
		{52, "50000baseKR/Full"},
		{56, "50000baseDR/Full"},
		{57, "100000baseKR2/Full"},
		{61, "100000baseDR2/Full"},
		{62, "200000baseKR4/Full"},
		{66, "200000baseCR4/Full"},
		{67, "100baseT1/Full"},
		{68, "1000baseT1/Full"},
		{69, "400000baseKR8/Full"},
		{73, "400000baseCR8/Full"},
		{75, "100000baseKR/Full"},
		{79, "100000baseDR/Full"},
		{80, "200000baseKR2/Full"},
		{84, "200000baseCR2/Full"},
		{85, "400000baseKR4/Full"},
		{89, "400000baseCR4/Full"},
		{90, "100baseFX/Half"},
		{92, "10baseT1L/Full"},
		{93, "800000baseCR8/Full"},
		{98, "800000baseVR8/Full"},
		{99, "10baseT1S/Full"},
		{102, "10baseT1BRR/Full"},
		{103, "200000baseCR/Full"},
		{108, "200000baseVR/Full"},
		{109, "400000baseCR2/Full"},
		{114, "400000baseVR2/Full"},
		{115, "800000baseCR4/Full"},
		{120, "800000baseVR4/Full"},
		{121, "1600000baseCR8/Full"},
		{124, "1600000baseDR8_2/Full"},
	}

	for _, mode := range expected {
		assert.Equal(t, mode.name, modes[mode.bit], "Unexpected link mode for bit %d", mode.bit)
	}

	// The FEC bits don't describe link modes.
	for _, bit := range []uint{49, 50, 51, 74} {
		assert.NotContains(t, modes, bit)
	}
}
