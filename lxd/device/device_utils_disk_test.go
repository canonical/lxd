package device

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/lxd/idmap"
)

func TestDiskVMVirtiofsdResolveIDMaps(t *testing.T) {
	t.Run("Use supplied idmaps", func(t *testing.T) {
		expected := []idmap.IdmapEntry{
			{
				Hostid:   1000,
				Isuid:    true,
				Nsid:     0,
				Maprange: 1,
			},
			{
				Hostid:   1000,
				Isgid:    true,
				Nsid:     0,
				Maprange: 1,
			},
		}

		currentIDMapSetFunc := func() (*idmap.IdmapSet, error) {
			return nil, errors.New("should not be called")
		}

		actual, err := diskVMVirtiofsdResolveIDMaps(expected, currentIDMapSetFunc)
		assert.NoError(t, err)
		assert.Equal(t, expected, actual)
	})

	t.Run("Fallback to current idmap set", func(t *testing.T) {
		// The current idmap set maps current-namespace IDs (Nsid) to parent IDs (Hostid), here a
		// non-identity mapping (0 -> 100000). The resolved maps must be an identity map over the
		// current Nsid range (Hostid = Nsid) so nested LXD can start virtiofsd correctly.
		currentIDMapSet := &idmap.IdmapSet{Idmap: []idmap.IdmapEntry{
			{
				Hostid:   100000,
				Isuid:    true,
				Nsid:     0,
				Maprange: 65536,
			},
			{
				Hostid:   100000,
				Isgid:    true,
				Nsid:     0,
				Maprange: 65536,
			},
		}}

		expected := []idmap.IdmapEntry{
			{
				Hostid:   0,
				Isuid:    true,
				Nsid:     0,
				Maprange: 65536,
			},
			{
				Hostid:   0,
				Isgid:    true,
				Nsid:     0,
				Maprange: 65536,
			},
		}

		currentIDMapSetFunc := func() (*idmap.IdmapSet, error) {
			return currentIDMapSet, nil
		}

		actual, err := diskVMVirtiofsdResolveIDMaps(nil, currentIDMapSetFunc)
		assert.NoError(t, err)
		assert.Equal(t, expected, actual)
	})

	t.Run("Fail if getting current idmap set", func(t *testing.T) {
		currentIDMapSetFunc := func() (*idmap.IdmapSet, error) {
			return nil, errors.New("boom")
		}

		actual, err := diskVMVirtiofsdResolveIDMaps(nil, currentIDMapSetFunc)
		assert.Nil(t, actual)
		assert.EqualError(t, err, "Failed getting current idmap set: boom")
	})

	t.Run("Fail if current idmap set empty", func(t *testing.T) {
		currentIDMapSetFunc := func() (*idmap.IdmapSet, error) {
			return &idmap.IdmapSet{}, nil
		}

		actual, err := diskVMVirtiofsdResolveIDMaps(nil, currentIDMapSetFunc)
		assert.Nil(t, actual)
		assert.EqualError(t, err, "Current idmap set cannot be empty")
	})

	t.Run("Fail if current idmap set has no gid map", func(t *testing.T) {
		currentIDMapSetFunc := func() (*idmap.IdmapSet, error) {
			return &idmap.IdmapSet{Idmap: []idmap.IdmapEntry{
				{
					Hostid:   100000,
					Isuid:    true,
					Nsid:     0,
					Maprange: 65536,
				},
			}}, nil
		}

		actual, err := diskVMVirtiofsdResolveIDMaps(nil, currentIDMapSetFunc)
		assert.Nil(t, actual)
		assert.EqualError(t, err, "Current idmap set must contain both UID and GID mappings")
	})

	t.Run("Fail if current idmap set function is nil", func(t *testing.T) {
		actual, err := diskVMVirtiofsdResolveIDMaps(nil, nil)
		assert.Nil(t, actual)
		assert.EqualError(t, err, "Current idmap set function is nil")
	})
}

func TestDiskAddRootUserNSEntry(t *testing.T) {
	// Check adds a combined uid/gid root entry to an empty list.
	var idmaps []idmap.IdmapEntry
	idmaps = diskAddRootUserNSEntry(idmaps, 65534)
	expected := []idmap.IdmapEntry{
		{
			Isuid:    true,
			Isgid:    true,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
	}

	assert.Equal(t, expected, idmaps)

	// Check doesn't add another one if an existing combined entry exists.
	idmaps = diskAddRootUserNSEntry(idmaps, 65534)
	assert.Equal(t, expected, idmaps)

	// Check adds a root gid entry if root uid entry already exists.
	idmaps = []idmap.IdmapEntry{
		{
			Isuid:    true,
			Isgid:    false,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
	}

	idmaps = diskAddRootUserNSEntry(idmaps, 65534)
	expected = []idmap.IdmapEntry{
		{
			Isuid:    true,
			Isgid:    false,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
		{
			Isuid:    false,
			Isgid:    true,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
	}

	assert.Equal(t, expected, idmaps)

	// Check adds a root uid entry if root gid entry already exists.
	idmaps = []idmap.IdmapEntry{
		{
			Isuid:    false,
			Isgid:    true,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
	}

	idmaps = diskAddRootUserNSEntry(idmaps, 65534)
	expected = []idmap.IdmapEntry{
		{
			Isuid:    false,
			Isgid:    true,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
		{
			Isuid:    true,
			Isgid:    false,
			Hostid:   65534,
			Maprange: 1,
			Nsid:     0,
		},
	}

	assert.Equal(t, expected, idmaps)
}
