package db

// OperationType is a numeric code indentifying the type of an Operation.
type OperationType int64

// Possible values for OperationType
//
// WARNING: The type codes are stored in the database, so this list of
//          definitions should be normally append-only. Any other change
//          requires a database update.
const (
	OperationUnknown OperationType = iota
	OperationClusterBootstrap
	OperationClusterJoin
	OperationBackupCreate
	OperationBackupRename
	OperationBackupRestore
	OperationBackupRemove
	OperationConsoleShow
	OperationContainerCreate
	OperationContainerUpdate
	OperationContainerRename
	OperationContainerMigrate
	OperationContainerLiveMigrate
	OperationContainerFreeze
	OperationContainerUnfreeze
	OperationContainerDelete
	OperationContainerStart
	OperationContainerStop
	OperationContainerRestart
	OperationCommandExec
	OperationSnapshotCreate
	OperationSnapshotRename
	OperationSnapshotRestore
	OperationSnapshotTransfer
	OperationSnapshotUpdate
	OperationSnapshotDelete
	OperationImageDownload
	OperationImageDelete
	OperationImageToken
	OperationImageRefresh
	OperationVolumeCopy
	OperationVolumeCreate
	OperationVolumeMigrate
	OperationVolumeMove
	OperationVolumeSnapshotCreate
	OperationVolumeSnapshotDelete
	OperationVolumeSnapshotUpdate
	OperationProjectRename
	OperationImagesExpire
	OperationImagesPruneLeftover
	OperationImagesUpdate
	OperationImagesSynchronize
	OperationLogsExpire
	OperationInstanceTypesUpdate
	OperationBackupsExpire
	OperationSnapshotsExpire
)

// Description return a human-readable description of the operation type.
func (t OperationType) Description() string {
	switch t {
	case OperationClusterBootstrap:
		return "Creating bootstrap node"
	case OperationClusterJoin:
		return "Joining cluster"
	case OperationBackupCreate:
		return "Backing up container"
	case OperationBackupRename:
		return "Renaming container backup"
	case OperationBackupRestore:
		return "Restoring backup"
	case OperationBackupRemove:
		return "Removing container backup"
	case OperationConsoleShow:
		return "Showing console"
	case OperationContainerCreate:
		return "Creating container"
	case OperationContainerUpdate:
		return "Updating container"
	case OperationContainerRename:
		return "Renaming container"
	case OperationContainerMigrate:
		return "Migrating container"
	case OperationContainerLiveMigrate:
		return "Live-migrating container"
	case OperationContainerFreeze:
		return "Freezing container"
	case OperationContainerUnfreeze:
		return "Unfreezing container"
	case OperationContainerDelete:
		return "Deleting container"
	case OperationContainerStart:
		return "Starting container"
	case OperationContainerStop:
		return "Stopping container"
	case OperationContainerRestart:
		return "Restarting container"
	case OperationCommandExec:
		return "Executing command"
	case OperationSnapshotCreate:
		return "Snapshotting container"
	case OperationSnapshotRename:
		return "Renaming snapshot"
	case OperationSnapshotRestore:
		return "Restoring snapshot"
	case OperationSnapshotTransfer:
		return "Transferring snapshot"
	case OperationSnapshotUpdate:
		return "Updating snapshot"
	case OperationSnapshotDelete:
		return "Deleting snapshot"
	case OperationImageDownload:
		return "Downloading image"
	case OperationImageDelete:
		return "Deleting image"
	case OperationImageToken:
		return "Image download token"
	case OperationImageRefresh:
		return "Refreshing image"
	case OperationVolumeCopy:
		return "Copying storage volume"
	case OperationVolumeCreate:
		return "Creating storage volume"
	case OperationVolumeMigrate:
		return "Migrating storage volume"
	case OperationVolumeMove:
		return "Moving storage volume"
	case OperationVolumeSnapshotCreate:
		return "Creating storage volume snapshot"
	case OperationVolumeSnapshotDelete:
		return "Deleting storage volume snapshot"
	case OperationVolumeSnapshotUpdate:
		return "Updating storage volume snapshot"
	case OperationProjectRename:
		return "Renaming project"
	case OperationImagesExpire:
		return "Cleaning up expired images"
	case OperationImagesPruneLeftover:
		return "Pruning leftover image files"
	case OperationImagesUpdate:
		return "Updating images"
	case OperationImagesSynchronize:
		return "Synchronizing images"
	case OperationLogsExpire:
		return "Expiring log files"
	case OperationInstanceTypesUpdate:
		return "Updating instance types"
	case OperationBackupsExpire:
		return "Cleaning up expired backups"
	case OperationSnapshotsExpire:
		return "Cleaning up expired snapshots"
	default:
		return "Executing operation"
	}
}

// Permission returns the needed RBAC permission to cancel the oepration
func (t OperationType) Permission() string {
	switch t {
	case OperationBackupCreate:
		return "operate-containers"
	case OperationBackupRename:
		return "operate-containers"
	case OperationBackupRestore:
		return "operate-containers"
	case OperationBackupRemove:
		return "operate-containers"
	case OperationConsoleShow:
		return "operate-containers"
	case OperationContainerFreeze:
		return "operate-containers"
	case OperationContainerUnfreeze:
		return "operate-containers"
	case OperationContainerStart:
		return "operate-containers"
	case OperationContainerStop:
		return "operate-containers"
	case OperationContainerRestart:
		return "operate-containers"
	case OperationCommandExec:
		return "operate-containers"
	case OperationSnapshotCreate:
		return "operate-containers"
	case OperationSnapshotRename:
		return "operate-containers"
	case OperationSnapshotTransfer:
		return "operate-containers"
	case OperationSnapshotUpdate:
		return "operate-containers"
	case OperationSnapshotDelete:
		return "operate-containers"

	case OperationContainerCreate:
		return "manage-containers"
	case OperationContainerUpdate:
		return "manage-containers"
	case OperationContainerRename:
		return "manage-containers"
	case OperationContainerMigrate:
		return "manage-containers"
	case OperationContainerLiveMigrate:
		return "manage-containers"
	case OperationContainerDelete:
		return "manage-containers"
	case OperationSnapshotRestore:
		return "manage-containers"

	case OperationImageDownload:
		return "manage-images"
	case OperationImageDelete:
		return "manage-images"
	case OperationImageToken:
		return "manage-images"
	case OperationImageRefresh:
		return "manage-images"
	case OperationImagesUpdate:
		return "manage-images"
	case OperationImagesSynchronize:
		return "manage-images"
	}

	return ""
}
