package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/shared/api"
)

func TestInstanceTypeToVolumeType(t *testing.T) {
	tests := []struct {
		instType instancetype.Type
		expected drivers.VolumeType
		err      bool
	}{
		{instancetype.Container, drivers.VolumeTypeContainer, false},
		{instancetype.VM, drivers.VolumeTypeVM, false},
		{instancetype.MicroVM, drivers.VolumeTypeMicroVM, false},
		{instancetype.Any, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.instType.String(), func(t *testing.T) {
			volType, err := InstanceTypeToVolumeType(tt.instType)
			if tt.err {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, volType)
			}
		})
	}
}

func TestVolumeTypeToAPIInstanceType(t *testing.T) {
	tests := []struct {
		volType  drivers.VolumeType
		expected api.InstanceType
		err      bool
	}{
		{drivers.VolumeTypeContainer, api.InstanceTypeContainer, false},
		{drivers.VolumeTypeVM, api.InstanceTypeVM, false},
		{drivers.VolumeTypeMicroVM, api.InstanceTypeMicroVM, false},
		{drivers.VolumeTypeCustom, api.InstanceTypeAny, true},
		{drivers.VolumeTypeImage, api.InstanceTypeAny, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.volType), func(t *testing.T) {
			apiType, err := VolumeTypeToAPIInstanceType(tt.volType)
			if tt.err {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, apiType)
			}
		})
	}
}

func TestVolumeTypeToDBType(t *testing.T) {
	tests := []struct {
		volType  drivers.VolumeType
		expected cluster.StoragePoolVolumeType
		err      bool
	}{
		{drivers.VolumeTypeContainer, cluster.StoragePoolVolumeTypeContainer, false},
		{drivers.VolumeTypeVM, cluster.StoragePoolVolumeTypeVM, false},
		{drivers.VolumeTypeMicroVM, cluster.StoragePoolVolumeTypeMicroVM, false},
		{drivers.VolumeTypeImage, cluster.StoragePoolVolumeTypeImage, false},
		{drivers.VolumeTypeCustom, cluster.StoragePoolVolumeTypeCustom, false},
		{"unknown", -1, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.volType), func(t *testing.T) {
			dbType, err := VolumeTypeToDBType(tt.volType)
			if tt.err {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, dbType)
			}
		})
	}
}

func TestVolumeDBTypeToType(t *testing.T) {
	tests := []struct {
		dbType   cluster.StoragePoolVolumeType
		expected drivers.VolumeType
	}{
		{cluster.StoragePoolVolumeTypeContainer, drivers.VolumeTypeContainer},
		{cluster.StoragePoolVolumeTypeVM, drivers.VolumeTypeVM},
		{cluster.StoragePoolVolumeTypeMicroVM, drivers.VolumeTypeMicroVM},
		{cluster.StoragePoolVolumeTypeImage, drivers.VolumeTypeImage},
		{cluster.StoragePoolVolumeTypeCustom, drivers.VolumeTypeCustom},
		{-1, drivers.VolumeTypeCustom},
	}

	for _, tt := range tests {
		t.Run(string(tt.expected), func(t *testing.T) {
			volType := VolumeDBTypeToType(tt.dbType)
			assert.Equal(t, tt.expected, volType)
		})
	}
}
