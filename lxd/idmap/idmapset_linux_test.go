//go:build linux && cgo

package idmap

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_ToLxcString(t *testing.T) {
	tests := []struct {
		name     string
		idmapSet IdmapSet
		expected []string
	}{
		{
			name:     "empty idmap set",
			idmapSet: IdmapSet{},
			expected: []string{},
		},
		{
			name: "single uid entry",
			idmapSet: IdmapSet{
				Idmap: []IdmapEntry{
					{Isuid: true, Hostid: 1000, Nsid: 0, Maprange: 1000},
				},
			},
			expected: []string{"u 0 1000 1000"},
		},
		{
			name: "single gid entry",
			idmapSet: IdmapSet{
				Idmap: []IdmapEntry{
					{Isgid: true, Hostid: 1000, Nsid: 0, Maprange: 1000},
				},
			},
			expected: []string{"g 0 1000 1000"},
		},
		{
			name: "single both entry",
			idmapSet: IdmapSet{
				Idmap: []IdmapEntry{
					{Isuid: true, Isgid: true, Hostid: 1000, Nsid: 0, Maprange: 1000},
				},
			},
			expected: []string{
				"u 0 1000 1000",
				"g 0 1000 1000",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.idmapSet.ToLxcString()
			if !slices.Equal(result, tt.expected) {
				t.Errorf("ToLxcString() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func Test_IsBetween(t *testing.T) {
	tests := []struct {
		name     string
		value    int64
		low      int64
		high     int64
		expected bool
	}{
		{"within range", 5, 1, 10, true},
		{"range start", 1, 1, 10, true},
		{"range end", 9, 1, 10, true},
		{"below range", 0, 1, 10, false},
		{"above range", 10, 1, 10, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isBetween(tt.value, tt.low, tt.high)
			if result != tt.expected {
				t.Errorf("isBetween(%d, %d, %d) = %v; want %v", tt.value, tt.low, tt.high, result, tt.expected)
			}
		})
	}
}

func TestIdmapSetAddSafe_split(t *testing.T) {
	orig := IdmapSet{Idmap: []IdmapEntry{{Isuid: true, Hostid: 1000, Nsid: 0, Maprange: 1000}}}

	err := orig.AddSafe(IdmapEntry{Isuid: true, Hostid: 500, Nsid: 500, Maprange: 10})
	if err != nil {
		t.Error(err)
		return
	}

	if orig.Idmap[0].Hostid != 1000 || orig.Idmap[0].Nsid != 0 || orig.Idmap[0].Maprange != 500 {
		t.Error(fmt.Errorf("bad range: %v", orig.Idmap[0]))
		return
	}

	if orig.Idmap[1].Hostid != 500 || orig.Idmap[1].Nsid != 500 || orig.Idmap[1].Maprange != 10 {
		t.Error(fmt.Errorf("bad range: %v", orig.Idmap[1]))
		return
	}

	if orig.Idmap[2].Hostid != 1510 || orig.Idmap[2].Nsid != 510 || orig.Idmap[2].Maprange != 490 {
		t.Error(fmt.Errorf("bad range: %v", orig.Idmap[2]))
		return
	}

	if len(orig.Idmap) != 3 {
		t.Error("too many idmap entries")
		return
	}
}

func TestIdmapSetAddSafe_lower(t *testing.T) {
	orig := IdmapSet{Idmap: []IdmapEntry{{Isuid: true, Hostid: 1000, Nsid: 0, Maprange: 1000}}}

	err := orig.AddSafe(IdmapEntry{Isuid: true, Hostid: 500, Nsid: 0, Maprange: 10})
	if err != nil {
		t.Error(err)
		return
	}

	if orig.Idmap[0].Hostid != 500 || orig.Idmap[0].Nsid != 0 || orig.Idmap[0].Maprange != 10 {
		t.Error(fmt.Errorf("bad range: %v", orig.Idmap[0]))
		return
	}

	if orig.Idmap[1].Hostid != 1010 || orig.Idmap[1].Nsid != 10 || orig.Idmap[1].Maprange != 990 {
		t.Error(fmt.Errorf("bad range: %v", orig.Idmap[1]))
		return
	}

	if len(orig.Idmap) != 2 {
		t.Error("too many idmap entries")
		return
	}
}

func TestIdmapSetAddSafe_upper(t *testing.T) {
	orig := IdmapSet{Idmap: []IdmapEntry{{Isuid: true, Hostid: 1000, Nsid: 0, Maprange: 1000}}}

	err := orig.AddSafe(IdmapEntry{Isuid: true, Hostid: 500, Nsid: 995, Maprange: 10})
	if err != nil {
		t.Error(err)
		return
	}

	if orig.Idmap[0].Hostid != 1000 || orig.Idmap[0].Nsid != 0 || orig.Idmap[0].Maprange != 995 {
		t.Error(fmt.Errorf("bad range: %v", orig.Idmap[0]))
		return
	}

	if orig.Idmap[1].Hostid != 500 || orig.Idmap[1].Nsid != 995 || orig.Idmap[1].Maprange != 10 {
		t.Error(fmt.Errorf("bad range: %v", orig.Idmap[1]))
		return
	}

	if len(orig.Idmap) != 2 {
		t.Error("too many idmap entries")
		return
	}
}

func TestIdmapSetIntersects(t *testing.T) {
	orig := IdmapSet{Idmap: []IdmapEntry{{Isuid: true, Hostid: 165536, Nsid: 0, Maprange: 65536}}}

	if !orig.Intersects(IdmapEntry{Isuid: true, Hostid: 231071, Nsid: 0, Maprange: 65536}) {
		t.Error("ranges don't intersect")
		return
	}

	if !orig.Intersects(IdmapEntry{Isuid: true, Hostid: 231072, Nsid: 0, Maprange: 65536}) {
		t.Error("ranges don't intersect")
		return
	}

	if !orig.Intersects(IdmapEntry{Isuid: true, Hostid: 231072, Nsid: 65535, Maprange: 65536}) {
		t.Error("ranges don't intersect")
		return
	}

	if orig.Intersects(IdmapEntry{Isuid: true, Hostid: 231072, Nsid: 65536, Maprange: 65536}) {
		t.Error("ranges intersect")
		return
	}
}

func TestIdmapHostIDMapRange(t *testing.T) {
	// Check empty entry is not covered.
	idmap := IdmapEntry{}
	assert.False(t, idmap.HostIDsCoveredBy(nil, nil))

	// Check nil allowed lists are not covered.
	idmap = IdmapEntry{Isuid: true, Hostid: 1000, Maprange: 1}
	assert.False(t, idmap.HostIDsCoveredBy(nil, nil))

	// Check that UID/GID specific host IDs are covered by equivalent UID/GID specific host ID rule.
	uidOnlyEntry := IdmapEntry{Isuid: true, Hostid: 1000, Maprange: 1}
	gidOnlyEntry := IdmapEntry{Isgid: true, Hostid: 1000, Maprange: 1}

	allowedUIDMaps := []IdmapEntry{
		{Isuid: true, Hostid: 1000, Maprange: 1},
	}

	allowedGIDMaps := []IdmapEntry{
		{Isgid: true, Hostid: 1000, Maprange: 1},
	}

	assert.True(t, uidOnlyEntry.HostIDsCoveredBy(allowedUIDMaps, nil))
	assert.False(t, uidOnlyEntry.HostIDsCoveredBy(nil, allowedUIDMaps))
	assert.True(t, uidOnlyEntry.HostIDsCoveredBy(allowedUIDMaps, allowedUIDMaps))

	assert.False(t, uidOnlyEntry.HostIDsCoveredBy(allowedGIDMaps, nil))
	assert.False(t, uidOnlyEntry.HostIDsCoveredBy(nil, allowedGIDMaps))
	assert.False(t, uidOnlyEntry.HostIDsCoveredBy(allowedGIDMaps, allowedGIDMaps))

	assert.False(t, gidOnlyEntry.HostIDsCoveredBy(allowedGIDMaps, nil))
	assert.True(t, gidOnlyEntry.HostIDsCoveredBy(nil, allowedGIDMaps))
	assert.True(t, gidOnlyEntry.HostIDsCoveredBy(allowedGIDMaps, allowedGIDMaps))

	assert.False(t, gidOnlyEntry.HostIDsCoveredBy(allowedUIDMaps, nil))
	assert.False(t, gidOnlyEntry.HostIDsCoveredBy(nil, allowedUIDMaps))
	assert.False(t, gidOnlyEntry.HostIDsCoveredBy(allowedUIDMaps, allowedUIDMaps))

	// Check ranges are correctly blocked when not covered by single ID allow list.
	uidOnlyRangeEntry := IdmapEntry{Isuid: true, Hostid: 1000, Maprange: 2}
	gidOnlyRangeEntry := IdmapEntry{Isgid: true, Hostid: 1000, Maprange: 2}

	assert.False(t, uidOnlyRangeEntry.HostIDsCoveredBy(allowedUIDMaps, nil))
	assert.False(t, uidOnlyRangeEntry.HostIDsCoveredBy(nil, allowedUIDMaps))
	assert.False(t, uidOnlyRangeEntry.HostIDsCoveredBy(allowedUIDMaps, allowedUIDMaps))

	assert.False(t, gidOnlyRangeEntry.HostIDsCoveredBy(allowedGIDMaps, nil))
	assert.False(t, gidOnlyRangeEntry.HostIDsCoveredBy(nil, allowedGIDMaps))
	assert.False(t, gidOnlyRangeEntry.HostIDsCoveredBy(allowedGIDMaps, allowedGIDMaps))

	// Check ranges are allowed when fully covered.
	allowedUIDMaps = []IdmapEntry{
		{Isuid: true, Hostid: 1000, Maprange: 2},
	}

	allowedGIDMaps = []IdmapEntry{
		{Isgid: true, Hostid: 1000, Maprange: 2},
	}

	assert.True(t, uidOnlyRangeEntry.HostIDsCoveredBy(allowedUIDMaps, nil))
	assert.False(t, uidOnlyRangeEntry.HostIDsCoveredBy(nil, allowedUIDMaps))
	assert.True(t, uidOnlyRangeEntry.HostIDsCoveredBy(allowedUIDMaps, allowedUIDMaps))

	assert.False(t, gidOnlyRangeEntry.HostIDsCoveredBy(allowedGIDMaps, nil))
	assert.True(t, gidOnlyRangeEntry.HostIDsCoveredBy(nil, allowedGIDMaps))
	assert.True(t, gidOnlyRangeEntry.HostIDsCoveredBy(allowedGIDMaps, allowedGIDMaps))

	// Check ranges for combined allowed ID maps are correctly validated.
	allowedCombinedMaps := []IdmapEntry{
		{Isuid: true, Isgid: true, Hostid: 1000, Maprange: 2},
	}

	assert.True(t, uidOnlyRangeEntry.HostIDsCoveredBy(allowedCombinedMaps, nil))
	assert.False(t, uidOnlyRangeEntry.HostIDsCoveredBy(nil, allowedCombinedMaps))
	assert.True(t, uidOnlyRangeEntry.HostIDsCoveredBy(allowedCombinedMaps, allowedCombinedMaps))

	assert.False(t, gidOnlyRangeEntry.HostIDsCoveredBy(allowedCombinedMaps, nil))
	assert.True(t, gidOnlyRangeEntry.HostIDsCoveredBy(nil, allowedCombinedMaps))
	assert.True(t, gidOnlyRangeEntry.HostIDsCoveredBy(allowedCombinedMaps, allowedCombinedMaps))

	combinedEntry := IdmapEntry{Isuid: true, Isgid: true, Hostid: 1000, Maprange: 1}

	assert.False(t, combinedEntry.HostIDsCoveredBy(allowedCombinedMaps, nil))
	assert.False(t, combinedEntry.HostIDsCoveredBy(nil, allowedCombinedMaps))
	assert.True(t, combinedEntry.HostIDsCoveredBy(allowedCombinedMaps, allowedCombinedMaps))

	assert.False(t, combinedEntry.HostIDsCoveredBy(allowedCombinedMaps, nil))
	assert.False(t, combinedEntry.HostIDsCoveredBy(nil, allowedCombinedMaps))
	assert.True(t, combinedEntry.HostIDsCoveredBy(allowedCombinedMaps, allowedCombinedMaps))
}

func Test_getFromShadow(t *testing.T) {
	tests := []struct {
		name     string
		username string
		content  string
		expected [][]int64
		wantErr  bool
	}{
		{
			name:     "valid entry",
			username: "root",
			content:  "root:1000000:1000000000\n",
			expected: [][]int64{{1000000, 1000000000}},
		},
		{
			name:     "valid entries",
			username: "foo",
			content:  "foo:0:1000\nfoo:1001:5\n",
			expected: [][]int64{{0, 1000}, {1001, 5}},
		},
		{
			name:     "valid entry for foo",
			username: "foo",
			content:  "foo:0:1000\nbar:1001:5\n",
			expected: [][]int64{{0, 1000}},
		},
		{
			name:     "valid entry for bar",
			username: "bar",
			content:  "foo:0:1000\nbar:1001:5\n",
			expected: [][]int64{{1001, 5}},
		},
		{
			name:     "empty file",
			username: "foo",
			content:  "",
			wantErr:  true,
		},
		{
			name:     "invalid format",
			username: "foo",
			content:  "0:1000\n",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file with test content
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "subXid")

			err := os.WriteFile(tmpFile, []byte(tt.content), 0644)
			if err != nil {
				t.Fatal(err)
			}

			result, err := getFromShadow(tmpFile, tt.username)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("got %v, want %v", result, tt.expected)
			}
		})
	}
}

func Test_getFromProc(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected [][]int64
		wantErr  bool
	}{
		{
			name:     "valid entry",
			content:  "         0          0 4294967295\n",
			expected: [][]int64{{0, 0, 4294967295}},
		},
		{
			name:     "comments are ignored",
			content:  "# not supposed to be here but harmless\n         0          0 4294967295\n",
			expected: [][]int64{{0, 0, 4294967295}},
		},
		{
			name:     "valid entries",
			content:  "0 1000 1\n1 1001 5",
			expected: [][]int64{{0, 1000, 1}, {1, 1001, 5}},
		},
		{
			name:    "empty file",
			content: "",
			wantErr: true,
		},
		{
			name:    "invalid format",
			content: "0 1000",
			wantErr: true,
		},
		{
			name:     "skip invalid entries",
			content:  "invalid 1000 1\n0 1000 1",
			expected: [][]int64{{0, 1000, 1}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file with test content
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "Xid_map")

			err := os.WriteFile(tmpFile, []byte(tt.content), 0644)
			if err != nil {
				t.Fatal(err)
			}

			// Test the function
			result, err := getFromProc(tmpFile)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("got %v, want %v", result, tt.expected)
			}
		})
	}
}
