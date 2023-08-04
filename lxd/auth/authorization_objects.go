package auth

import (
	"fmt"
)

func GroupObject(groupName string) string {
	return fmt.Sprintf("%s:%s", ObjectTypeGroup, groupName)
}

func ImageObject(projectName string, imageFingerprint string) string {
	return fmt.Sprintf("%s:%s_%s", ObjectTypeImage, projectName, imageFingerprint)
}

func InstanceObject(projectName string, instanceName string) string {
	return fmt.Sprintf("%s:%s_%s", ObjectTypeInstance, projectName, instanceName)
}

func NetworkObject(projectName string, networkName string) string {
	return fmt.Sprintf("%s:%s_%s", ObjectTypeNetwork, projectName, networkName)
}

func NetworkACLObject(projectName string, networkACLName string) string {
	return fmt.Sprintf("%s:%s_%s", ObjectTypeNetwork, projectName, networkACLName)
}

func NetworkZoneObject(projectName string, networkZoneName string) string {
	return fmt.Sprintf("%s:%s_%s", ObjectTypeNetwork, projectName, networkZoneName)
}

func ProfileObject(projectName string, profileName string) string {
	return fmt.Sprintf("%s:%s_%s", ObjectTypeNetwork, projectName, profileName)
}

func ProjectObject(projectName string) string {
	return fmt.Sprintf("%s:%s", ObjectTypeProject, projectName)
}

func StorageBucketObject(projectName string, poolName string, bucketName string) string {
	return fmt.Sprintf("%s:%s_%s/%s", ObjectTypeStorageBucket, projectName, poolName, bucketName)
}

func StorageVolumeObject(projectName string, poolName string, volumeName string, volumeType string) string {
	return fmt.Sprintf("%s:%s_%s/%s/%s", ObjectTypeStorageVolume, projectName, poolName, volumeType, volumeName)
}

func UserObject(userName string) string {
	return fmt.Sprintf("%s:%s", ObjectTypeUser, userName)
}
