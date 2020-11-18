// +build linux,cgo,!agent

package db

import "github.com/grant-he/lxd/shared/api"

func (c *Cluster) GetStoragePoolVolume(project string, volumeName string, volumeType int, poolID, nodeID int64) (int64, *api.StorageVolume, error) {
	return c.storagePoolVolumeGetType(project, volumeName, volumeType, poolID, nodeID)
}
