package nodes

import (
	"fmt"

	"github.com/canonical/lxd/shared/api"
	"github.com/r3labs/diff/v3"
)

var (
	StoragePoolPrefix           = "storage-pool_"
	StorageVolumePrefix         = "storage-volume_"
	StorageVolumeSnapshotPrefix = "storage-volume-snapshot_"
	StorageBucketPrefix         = "storage-bucket_"
	StorageBucketKeyPrefix      = "storage-bucket-key_"
)

type StoragePoolNode struct {
	baseNode

	Driver string
	Name   string
}

func (sp *StoragePoolNode) Diff(other any) (diff.Changelog, error) {
	return nil, nil
}

func GenerateStoragePoolHumanID(poolName string) string {
	return fmt.Sprintf("%s%s", StoragePoolPrefix, poolName)
}

func NewStoragePoolNode(poolName string, data *api.StoragePool, id int64) *StoragePoolNode {
	data.UsedBy = nil
	return &StoragePoolNode{
		baseNode: baseNode{
			data:    data,
			id:      id,
			humanID: GenerateStoragePoolHumanID(poolName),
		},
		Name:   poolName,
		Driver: data.Driver,
	}
}

type StorageVolumeNode struct {
	baseNode

	Project string
	Name    string
}

func (sv *StorageVolumeNode) Diff(other any) (diff.Changelog, error) {
	return nil, nil
}

func (sv *StorageVolumeNode) Renamable() bool {
	return true
}

func GenerateStorageVolumeHumanID(project string, poolName string, volumeName string, volumeLocation string) string {
	return fmt.Sprintf("%s%s_%s_%s_%s", StorageVolumePrefix, project, poolName, volumeName, volumeLocation)
}

func NewStorageVolumeNode(project string, poolName string, volumeName string, volumeLocation string, volume api.StorageVolume, id int64) *StorageVolumeNode {
	return &StorageVolumeNode{
		baseNode: baseNode{
			data:    volume,
			id:      id,
			humanID: GenerateStorageVolumeHumanID(project, poolName, volumeName, volumeLocation),
		},
		Project: project,
		Name:    volumeName,
	}
}

type StorageVolumeSnapshotNode struct {
	baseNode

	Project string
	Name    string
}

func (svs *StorageVolumeSnapshotNode) Diff(other any) (diff.Changelog, error) {
	return nil, nil
}

func (svs *StorageVolumeSnapshotNode) Renamable() bool {
	return true
}

func GenerateStorageVolumeSnapshotHumanID(project string, driver string, poolName string, volumeName string, snapshotName string) string {
	return fmt.Sprintf("%s%s_%s_%s_%s_%s", StorageVolumeSnapshotPrefix, project, driver, poolName, volumeName, snapshotName)
}

func NewStorageVolumeSnapshotNode(project string, driver string, poolName string, volumeName string, snapshotName string, snapshot api.StorageVolumeSnapshot, id int64) *StorageVolumeSnapshotNode {
	return &StorageVolumeSnapshotNode{
		baseNode: baseNode{
			data:    snapshot,
			id:      id,
			humanID: GenerateStorageVolumeSnapshotHumanID(project, driver, poolName, volumeName, snapshotName),
		},
		Project: project,
		Name:    snapshotName,
	}
}

type StorageBucketNode struct {
	baseNode

	Project string
	Name    string
}

func (sb *StorageBucketNode) Diff(other any) (diff.Changelog, error) {
	return nil, nil
}

func GenerateStorageBucketHumanID(project string, driver string, poolName string, bucketName string) string {
	return fmt.Sprintf("%s%s_%s_%s_%s", StorageBucketPrefix, project, driver, poolName, bucketName)
}

func NewStorageBucketNode(project string, driver string, poolName string, bucketName string, bucket api.StorageBucket, id int64) *StorageBucketNode {
	return &StorageBucketNode{
		baseNode: baseNode{
			data:    bucket,
			id:      id,
			humanID: GenerateStorageBucketHumanID(project, driver, poolName, bucketName),
		},
		Project: project,
		Name:    bucketName,
	}
}

type StorageBucketKeyNode struct {
	baseNode

	Project    string
	BucketName string
	Name       string
}

func (sbk *StorageBucketKeyNode) Diff(other any) (diff.Changelog, error) {
	return nil, nil
}

func GenerateStorageBucketKeyHumanID(project string, driver string, poolName string, bucketName string, keyName string) string {
	return fmt.Sprintf("%s%s_%s_%s_%s_%s", StorageBucketKeyPrefix, project, driver, poolName, bucketName, keyName)
}

func NewStorageBucketKeyNode(project string, driver string, poolName string, bucketName string, keyName string, key api.StorageBucketKey, id int64) *StorageBucketKeyNode {
	return &StorageBucketKeyNode{
		baseNode: baseNode{
			data:    key,
			id:      id,
			humanID: GenerateStorageBucketKeyHumanID(project, driver, poolName, bucketName, keyName),
		},
		Project:    project,
		BucketName: bucketName,
		Name:       keyName,
	}
}
