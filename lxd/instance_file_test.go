package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/lxd/idmap"
)

// TestEffectiveOwnershipInRanges checks that a UID/GID is accepted when it falls within
// any range of its type and rejected (set to -1) only when it matches none.
func TestEffectiveOwnershipInRanges(t *testing.T) {
	tests := []struct {
		name        string
		ranges      []*idmap.IdRange
		uid         int64
		gid         int64
		expectedUID int64
		expectedGID int64
	}{
		{
			name: "both within single ranges",
			ranges: []*idmap.IdRange{
				{Isuid: true, Startid: 1000000, Endid: 1065535},
				{Isgid: true, Startid: 1000000, Endid: 1065535},
			},
			uid:         1000000,
			gid:         1000050,
			expectedUID: 1000000,
			expectedGID: 1000050,
		},
		{
			name: "uid outside single range",
			ranges: []*idmap.IdRange{
				{Isuid: true, Startid: 1000000, Endid: 1065535},
				{Isgid: true, Startid: 1000000, Endid: 1065535},
			},
			uid:         2000000,
			gid:         1000000,
			expectedUID: -1,
			expectedGID: 1000000,
		},
		{
			name: "uid within second of multiple non-contiguous ranges",
			ranges: []*idmap.IdRange{
				{Isuid: true, Startid: 1000000, Endid: 1000009},
				{Isuid: true, Startid: 2000000, Endid: 2000009},
				{Isgid: true, Startid: 1000000, Endid: 1000009},
			},
			uid:         2000005,
			gid:         1000005,
			expectedUID: 2000005,
			expectedGID: 1000005,
		},
		{
			name: "gid within none of multiple ranges",
			ranges: []*idmap.IdRange{
				{Isuid: true, Startid: 1000000, Endid: 1000009},
				{Isgid: true, Startid: 1000000, Endid: 1000009},
				{Isgid: true, Startid: 3000000, Endid: 3000009},
			},
			uid:         1000005,
			gid:         2000005,
			expectedUID: 1000005,
			expectedGID: -1,
		},
		{
			name: "combined uid and gid range",
			ranges: []*idmap.IdRange{
				{Isuid: true, Isgid: true, Startid: 1000000, Endid: 1065535},
			},
			uid:         1000000,
			gid:         1000000,
			expectedUID: 1000000,
			expectedGID: 1000000,
		},
		{
			name:        "no ranges leaves ids unchanged",
			ranges:      nil,
			uid:         1000000,
			gid:         1000000,
			expectedUID: 1000000,
			expectedGID: 1000000,
		},
		{
			name: "valid uid and invalid gid",
			ranges: []*idmap.IdRange{
				{Isuid: true, Startid: 1000000, Endid: 1000009},
				{Isgid: true, Startid: 1000000, Endid: 1000009},
			},
			uid:         1000005,
			gid:         5000000,
			expectedUID: 1000005,
			expectedGID: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uid, gid := effectiveOwnershipInRanges(tt.ranges, tt.uid, tt.gid)
			assert.Equal(t, tt.expectedUID, uid)
			assert.Equal(t, tt.expectedGID, gid)
		})
	}
}
