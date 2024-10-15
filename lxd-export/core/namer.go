package core

import (
	"strings"
)

var (
	// Instance prefixes
	profilePrefix  = "profile_"
	instancePrefix = "instance_"
	// Networking prefixes
	networkZonePrefix       = "network-zone_"
	networkZoneRecordPrefix = "network-zone-record_"
	networkPeerPrefix       = "network-peer_"
	networkACLPrefix        = "network-acl_"
	networkForwardPrefix    = "network-forward_"
	networkLBPrefix         = "network-lb_"
	networkPrefix           = "network_"
	// Storage prefixes
	storagePoolPrefix           = "storage-pool_"
	storageVolumePrefix         = "storage-volume_"
	storageVolumeSnapshotPrefix = "storage-volume-snapshot_"
	storageBucketPrefix         = "storage-bucket_"
	storageBucketKeyPrefix      = "storage-bucket-key_"
	// Project prefixes
	projectPrefix = "project_"
)

var prefixToImportance = map[string]int{
	projectPrefix:               0,
	storagePoolPrefix:           1,
	storageVolumePrefix:         2,
	storageVolumeSnapshotPrefix: 3,
	storageBucketPrefix:         4,
	storageBucketKeyPrefix:      5,
	networkZonePrefix:           6,
	networkZoneRecordPrefix:     7,
	networkPrefix:               8,
	networkForwardPrefix:        9,
	networkLBPrefix:             10,
	networkPeerPrefix:           11,
	networkACLPrefix:            12,
	profilePrefix:               13,
	instancePrefix:              14,
}

func humanIDEncode(prefix string, parts ...string) string {
	encodedParts := make([]string, len(parts))
	for i, part := range parts {
		encodedParts[i] = strings.Replace(part, "_", "--", -1)
	}

	return prefix + strings.Join(encodedParts, "_")
}

func humanIDDecode(humanID string) (prefix string, parts []string) {
	decodedParts := strings.Split(humanID, "_")
	prefix = decodedParts[0]
	for _, part := range decodedParts[1:] {
		parts = append(parts, strings.Replace(part, "--", "_", -1))
	}

	return prefix + "_", parts
}

func generateProfileHumanID(project string, name string) string {
	return humanIDEncode(profilePrefix, project, name)
}

func generateInstanceHumanID(project string, name string) string {
	return humanIDEncode(instancePrefix, project, name)
}

func generateNetworkZoneHumanID(name string) string {
	return humanIDEncode(networkZonePrefix, name)
}

func generateNetworkZoneRecordHumanID(zoneName string, name string) string {
	return humanIDEncode(networkZoneRecordPrefix, zoneName, name)
}

func generateNetworkPeerHumanID(srcProject string, srcNetworkName string, peerName string) string {
	return humanIDEncode(networkPeerPrefix, srcProject, srcNetworkName, peerName)
}

func generateNetworkACLHumanID(project string, aclName string) string {
	return humanIDEncode(networkACLPrefix, project, aclName)
}

func generateNetworkForwardHumanID(project string, networkName string, dataID string) string {
	return humanIDEncode(networkForwardPrefix, project, networkName, dataID)
}

func generateNetworkLBHumanID(project string, networkName string, dataID string) string {
	return humanIDEncode(networkLBPrefix, project, networkName, dataID)
}

func generateNetworkHumanID(project, name string) string {
	return humanIDEncode(networkPrefix, project, name)
}

func generateProjectHumanID(name string) string {
	return humanIDEncode(projectPrefix, name)
}

func generateStoragePoolHumanID(poolName string) string {
	return humanIDEncode(storagePoolPrefix, poolName)
}

func generateStorageVolumeHumanID(project string, poolName string, volumeName string, volumeLocation string) string {
	return humanIDEncode(storageVolumePrefix, project, poolName, volumeName, volumeLocation)
}

func generateStorageVolumeSnapshotHumanID(project string, driver string, poolName string, volumeName string, snapshotName string) string {
	return humanIDEncode(storageVolumeSnapshotPrefix, project, driver, poolName, volumeName, snapshotName)
}

func generateStorageBucketHumanID(project string, driver string, poolName string, bucketName string) string {
	return humanIDEncode(storageBucketPrefix, project, driver, poolName, bucketName)
}

func generateStorageBucketKeyHumanID(project string, driver string, poolName string, bucketName string, keyName string) string {
	return humanIDEncode(storageBucketKeyPrefix, project, driver, poolName, bucketName, keyName)
}
