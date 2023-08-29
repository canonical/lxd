package operationtype

import (
	"github.com/canonical/lxd/lxd/auth"
)

// Type is a numeric code indentifying the type of an Operation.
type Type int64

// Possible values for Type
//
// WARNING: The type codes are stored in the database, so this list of
// definitions should be normally append-only. Any other change
// requires a database update.
const (
	Unknown Type = iota
	ClusterBootstrap
	ClusterJoin
	BackupCreate
	BackupRename
	BackupRestore
	BackupRemove
	ConsoleShow
	InstanceCreate
	InstanceUpdate
	InstanceRename
	InstanceMigrate
	InstanceLiveMigrate
	InstanceFreeze
	InstanceUnfreeze
	InstanceDelete
	InstanceStart
	InstanceStop
	InstanceRestart
	InstanceRebuild
	CommandExec
	SnapshotCreate
	SnapshotRename
	SnapshotRestore
	SnapshotTransfer
	SnapshotUpdate
	SnapshotDelete
	ImageDownload
	ImageDelete
	ImageToken
	ImageRefresh
	VolumeCopy
	VolumeCreate
	VolumeMigrate
	VolumeMove
	VolumeSnapshotCreate
	VolumeSnapshotDelete
	VolumeSnapshotUpdate
	ProjectRename
	ImagesExpire
	ImagesPruneLeftover
	ImagesUpdate
	ImagesSynchronize
	LogsExpire
	InstanceTypesUpdate
	BackupsExpire
	SnapshotsExpire
	CustomVolumeSnapshotsExpire
	CustomVolumeBackupCreate
	CustomVolumeBackupRemove
	CustomVolumeBackupRename
	CustomVolumeBackupRestore
	WarningsPruneResolved
	ClusterJoinToken
	VolumeSnapshotRename
	ClusterMemberEvacuate
	ClusterMemberRestore
	CertificateAddToken
	RemoveOrphanedOperations
	RenewServerCertificate
	RemoveExpiredTokens
	ClusterHeal
)

// Description return a human-readable description of the operation type.
func (t Type) Description() string {
	switch t {
	case ClusterBootstrap:
		return "Creating bootstrap node"
	case ClusterJoin:
		return "Joining cluster"
	case BackupCreate:
		return "Backing up instance"
	case BackupRename:
		return "Renaming instance backup"
	case BackupRestore:
		return "Restoring backup"
	case BackupRemove:
		return "Removing instance backup"
	case ConsoleShow:
		return "Showing console"
	case InstanceCreate:
		return "Creating instance"
	case InstanceUpdate:
		return "Updating instance"
	case InstanceRename:
		return "Renaming instance"
	case InstanceMigrate:
		return "Migrating instance"
	case InstanceLiveMigrate:
		return "Live-migrating instance"
	case InstanceFreeze:
		return "Freezing instance"
	case InstanceUnfreeze:
		return "Unfreezing instance"
	case InstanceDelete:
		return "Deleting instance"
	case InstanceStart:
		return "Starting instance"
	case InstanceStop:
		return "Stopping instance"
	case InstanceRestart:
		return "Restarting instance"
	case InstanceRebuild:
		return "Rebuilding instance"
	case CommandExec:
		return "Executing command"
	case SnapshotCreate:
		return "Snapshotting instance"
	case SnapshotRename:
		return "Renaming snapshot"
	case SnapshotRestore:
		return "Restoring snapshot"
	case SnapshotTransfer:
		return "Transferring snapshot"
	case SnapshotUpdate:
		return "Updating snapshot"
	case SnapshotDelete:
		return "Deleting snapshot"
	case ImageDownload:
		return "Downloading image"
	case ImageDelete:
		return "Deleting image"
	case ImageToken:
		return "Image download token"
	case ImageRefresh:
		return "Refreshing image"
	case VolumeCopy:
		return "Copying storage volume"
	case VolumeCreate:
		return "Creating storage volume"
	case VolumeMigrate:
		return "Migrating storage volume"
	case VolumeMove:
		return "Moving storage volume"
	case VolumeSnapshotCreate:
		return "Creating storage volume snapshot"
	case VolumeSnapshotDelete:
		return "Deleting storage volume snapshot"
	case VolumeSnapshotUpdate:
		return "Updating storage volume snapshot"
	case VolumeSnapshotRename:
		return "Renaming storage volume snapshot"
	case ProjectRename:
		return "Renaming project"
	case ImagesExpire:
		return "Cleaning up expired images"
	case ImagesPruneLeftover:
		return "Pruning leftover image files"
	case ImagesUpdate:
		return "Updating images"
	case ImagesSynchronize:
		return "Synchronizing images"
	case LogsExpire:
		return "Expiring log files"
	case InstanceTypesUpdate:
		return "Updating instance types"
	case BackupsExpire:
		return "Cleaning up expired backups"
	case SnapshotsExpire:
		return "Cleaning up expired instance snapshots"
	case CustomVolumeSnapshotsExpire:
		return "Cleaning up expired volume snapshots"
	case CustomVolumeBackupCreate:
		return "Creating custom volume backup"
	case CustomVolumeBackupRemove:
		return "Deleting custom volume backup"
	case CustomVolumeBackupRename:
		return "Renaming custom volume backup"
	case CustomVolumeBackupRestore:
		return "Restoring custom volume backup"
	case WarningsPruneResolved:
		return "Pruning resolved warnings"
	case ClusterMemberEvacuate:
		return "Evacuating cluster member"
	case ClusterMemberRestore:
		return "Restoring cluster member"
	case RemoveOrphanedOperations:
		return "Remove orphaned operations"
	case RenewServerCertificate:
		return "Renewing server certificate"
	case RemoveExpiredTokens:
		return "Remove expired tokens"
	case ClusterHeal:
		return "Healing cluster"
	default:
		return "Executing operation"
	}
}

// Permission returns the needed permission to cancel the operation.
func (t Type) Permission() auth.Relation {
	switch t {
	case BackupCreate:
		return auth.RelationInstanceOperator
	case BackupRename:
		return auth.RelationInstanceOperator
	case BackupRestore:
		return auth.RelationInstanceOperator
	case BackupRemove:
		return auth.RelationInstanceOperator
	case ConsoleShow:
		return auth.RelationInstanceOperator
	case InstanceFreeze:
		return auth.RelationInstanceOperator
	case InstanceUnfreeze:
		return auth.RelationInstanceOperator
	case InstanceStart:
		return auth.RelationInstanceOperator
	case InstanceStop:
		return auth.RelationInstanceOperator
	case InstanceRestart:
		return auth.RelationInstanceOperator
	case CommandExec:
		return auth.RelationInstanceOperator
	case SnapshotCreate:
		return auth.RelationInstanceOperator
	case SnapshotRename:
		return auth.RelationInstanceOperator
	case SnapshotTransfer:
		return auth.RelationInstanceOperator
	case SnapshotUpdate:
		return auth.RelationInstanceOperator
	case SnapshotDelete:
		return auth.RelationInstanceOperator

	case InstanceCreate:
		return auth.RelationInstanceManager
	case InstanceUpdate:
		return auth.RelationInstanceManager
	case InstanceRename:
		return auth.RelationInstanceManager
	case InstanceMigrate:
		return auth.RelationInstanceManager
	case InstanceLiveMigrate:
		return auth.RelationInstanceManager
	case InstanceDelete:
		return auth.RelationInstanceManager
	case InstanceRebuild:
		return auth.RelationInstanceOperator
	case SnapshotRestore:
		return auth.RelationInstanceManager

	case ImageDownload:
		return auth.RelationImageManager
	case ImageDelete:
		return auth.RelationImageManager
	case ImageToken:
		return auth.RelationImageManager
	case ImageRefresh:
		return auth.RelationImageManager
	case ImagesUpdate:
		return auth.RelationImageManager
	case ImagesSynchronize:
		return auth.RelationImageManager

	case CustomVolumeSnapshotsExpire:
		return auth.RelationStorageVolumeManager
	case CustomVolumeBackupCreate:
		return auth.RelationStorageVolumeManager
	case CustomVolumeBackupRemove:
		return auth.RelationStorageVolumeManager
	case CustomVolumeBackupRename:
		return auth.RelationStorageVolumeManager
	case CustomVolumeBackupRestore:
		return auth.RelationStorageVolumeManager
	}

	return ""
}
