package zone

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/api"
)

// TestValidateEntries exercises validateEntries with a variety of valid and
// invalid inputs.  validateEntries has no dependency on zone state, so a
// zero-value zone is sufficient.
func TestValidateEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		entries     []api.NetworkZoneRecordEntry
		expectError bool
	}{
		{
			name: "TTL zero defaults to 300 and is accepted",
			entries: []api.NetworkZoneRecordEntry{
				{Type: "A", TTL: 0, Value: "192.0.2.1"},
			},
		},
		{
			name: "Normal TTL is accepted",
			entries: []api.NetworkZoneRecordEntry{
				{Type: "A", TTL: 300, Value: "192.0.2.1"},
			},
		},
		{
			name: "Maximum valid TTL (math.MaxUint32) is accepted",
			entries: []api.NetworkZoneRecordEntry{
				{Type: "A", TTL: math.MaxUint32, Value: "192.0.2.1"},
			},
		},
		{
			name: "TTL one above MaxUint32 is rejected",
			entries: []api.NetworkZoneRecordEntry{
				{Type: "A", TTL: math.MaxUint32 + 1, Value: "192.0.2.1"},
			},
			expectError: true,
		},
		{
			// RFC 3597 generic format: TYPE<NNN> \# <rdlength-decimal> <rdata-hex>.
			// TYPE1 is the A record (IANA type 1); the rdata for 192.0.2.1 is the
			// four octets c0 00 02 01.  Using the generic form exercises the same
			// validation path as any other unknown-at-compile-time type number.
			name: "RFC 3597 TYPE<NNN> generic identifier is accepted",
			entries: []api.NetworkZoneRecordEntry{
				{Type: "TYPE1", TTL: 300, Value: `\# 4 c0000201`},
			},
		},
		{
			name: "Multiple valid record types are accepted",
			entries: []api.NetworkZoneRecordEntry{
				{Type: "A", TTL: 300, Value: "192.0.2.1"},
				{Type: "AAAA", TTL: 300, Value: "2001:db8::1"},
				// TXT values must include surrounding double-quotes (zone-file syntax).
				{Type: "TXT", TTL: 300, Value: `"v=spf1 -all"`},
				{Type: "MX", TTL: 300, Value: "10 mail.example.net."},
				{Type: "CNAME", TTL: 300, Value: "www.example.net."},
				// CAA tag value must be double-quoted.
				{Type: "CAA", TTL: 300, Value: `0 issue "letsencrypt.org"`},
				{Type: "SRV", TTL: 300, Value: "10 20 5060 sip.example.net."},
			},
		},
		{
			name: "Unknown record type is rejected",
			entries: []api.NetworkZoneRecordEntry{
				{Type: "FOOBAR", TTL: 300, Value: "somevalue"},
			},
			expectError: true,
		},
		{
			name: "Invalid value for a valid type is rejected",
			entries: []api.NetworkZoneRecordEntry{
				{Type: "A", TTL: 300, Value: "not-an-ip-address"},
			},
			expectError: true,
		},
		{
			name: "Duplicate entries are rejected",
			entries: []api.NetworkZoneRecordEntry{
				{Type: "A", TTL: 300, Value: "192.0.2.1"},
				{Type: "A", TTL: 600, Value: "192.0.2.1"},
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := &zone{}
			err := d.validateEntries(api.NetworkZoneRecordPut{Entries: tc.entries})
			if tc.expectError {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
