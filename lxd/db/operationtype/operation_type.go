package operationtype

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// init disallows import of the package if any Type is not well-defined.
func init() {
	for t := Type(1); t < upperBound; t++ {
		if t.Description() == "" {
			panic(fmt.Sprintf("Operation type #%d does not have a description", t))
		}

		if t.EntityType() == "" {
			panic(fmt.Sprintf("Operation type #%d does not have an entity type", t))
		}
	}
}

// Type is a numeric code identifying the type of Operation.
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
	RemoveExpiredOIDCSessions
	ProfileUpdate
	VolumeUpdate
	VolumeDelete

	// upperBound is used only to enforce consistency in the package on init.
	// Make sure it's always the last item in this list.
	upperBound
)

// Validate returns an error if the given Type is not defined.
func Validate(operationTypeCode Type) error {
	if operationTypeCode > 0 && operationTypeCode < upperBound {
		return nil
	}

	return fmt.Errorf("Unknown operation type code %d", operationTypeCode)
}

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
	case ProfileUpdate:
		return "Updating profile"
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
	case VolumeUpdate:
		return "Updating storage volume"
	case VolumeDelete:
		return "Deleting storage volume"
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
	case RemoveExpiredOIDCSessions:
		return "Remove expired OIDC sessions"
	default:
		return "Executing operation"
	}
}

// EntityType returns the primary entity.Type that the Type operates on.
func (t Type) EntityType() entity.Type {
	switch t {
	case BackupCreate:
		return entity.TypeInstance
	case BackupRename:
		return entity.TypeInstance
	case BackupRestore:
		return entity.TypeInstance
	case BackupRemove:
		return entity.TypeInstance
	case ConsoleShow:
		return entity.TypeInstance
	case InstanceFreeze:
		return entity.TypeInstance
	case InstanceUnfreeze:
		return entity.TypeInstance
	case InstanceStart:
		return entity.TypeInstance
	case InstanceStop:
		return entity.TypeInstance
	case InstanceRestart:
		return entity.TypeInstance
	case CommandExec:
		return entity.TypeInstance
	case SnapshotCreate:
		return entity.TypeInstance
	case SnapshotRename:
		return entity.TypeInstance
	case SnapshotTransfer:
		return entity.TypeInstance
	case SnapshotUpdate:
		return entity.TypeInstance
	case SnapshotDelete:
		return entity.TypeInstance

	case InstanceCreate:
		return entity.TypeInstance
	case InstanceUpdate:
		return entity.TypeInstance
	case InstanceRename:
		return entity.TypeInstance
	case InstanceMigrate:
		return entity.TypeInstance
	case InstanceLiveMigrate:
		return entity.TypeInstance
	case InstanceDelete:
		return entity.TypeInstance
	case InstanceRebuild:
		return entity.TypeInstance
	case SnapshotRestore:
		return entity.TypeInstance

	case ImageDownload:
		return entity.TypeImage
	case ImageDelete:
		return entity.TypeImage
	case ImageToken:
		return entity.TypeImage
	case ImageRefresh:
		return entity.TypeImage
	case ImagesUpdate:
		return entity.TypeImage
	case ImagesSynchronize:
		return entity.TypeImage

	case CustomVolumeSnapshotsExpire:
		return entity.TypeStorageVolume
	case CustomVolumeBackupCreate:
		return entity.TypeStorageVolume
	case CustomVolumeBackupRemove:
		return entity.TypeStorageVolume
	case CustomVolumeBackupRename:
		return entity.TypeStorageVolume
	case CustomVolumeBackupRestore:
		return entity.TypeStorageVolume
	}

	return ""
}
