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
	PruneExpiredDurableOperations
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
	case PruneExpiredDurableOperations:
		return "Pruning expired durable operations"
	case RenewServerCertificate:
		return "Renewing server certificate"
	case RemoveExpiredTokens:
		return "Remove expired tokens"
	case ClusterHeal:
		return "Healing cluster"
	case ClusterJoinToken:
		return "Cluster join token"
	case CertificateAddToken:
		return "Certificate add token"
	case RemoveExpiredOIDCSessions:
		return "Removing expired OIDC sessions"

	// It should never be possible to reach the default clause.
	// See the init function.
	default:
		return ""
	}
}

// EntityType returns the primary entity.Type that the Type operates on.
func (t Type) EntityType() entity.Type {
	switch t {
	// Server level operations and background tasks.
	case ClusterBootstrap, ClusterJoin, CustomVolumeSnapshotsExpire, ImagesExpire, ImagesPruneLeftover,
		ImagesSynchronize, RemoveExpiredOIDCSessions, RemoveExpiredTokens, RemoveOrphanedOperations,
		WarningsPruneResolved, ClusterMemberEvacuate, ClusterMemberRestore, LogsExpire, InstanceTypesUpdate,
		BackupsExpire, SnapshotsExpire, ClusterJoinToken, CertificateAddToken, RenewServerCertificate,
		ClusterHeal, ImagesUpdate, PruneExpiredDurableOperations:
		return entity.TypeServer

	// Project level operations.
	// If creating a resource, then the parent project is the primary entity
	// (the entity being created is not yet referenceable).
	case VolumeCreate, ProjectRename, InstanceCreate, ImageDownload:
		return entity.TypeProject

	// Volume operations.
	case VolumeSnapshotRename, VolumeSnapshotUpdate, VolumeSnapshotDelete, VolumeMigrate,
		VolumeMove, VolumeSnapshotCreate, CustomVolumeBackupCreate, VolumeCopy, VolumeUpdate, VolumeDelete:
		return entity.TypeStorageVolume

	// Instance operations.
	case BackupCreate, ConsoleShow, InstanceFreeze, InstanceUpdate, InstanceUnfreeze,
		InstanceStart, InstanceStop, InstanceRestart, InstanceRename, InstanceMigrate, InstanceLiveMigrate,
		InstanceDelete, InstanceRebuild, SnapshotRestore, CommandExec, SnapshotCreate:
		return entity.TypeInstance

	// Instance backup operations.
	case BackupRename, BackupRemove, BackupRestore:
		return entity.TypeInstanceBackup

	// Instance snapshot operations.
	case SnapshotRename, SnapshotTransfer, SnapshotUpdate, SnapshotDelete:
		return entity.TypeInstanceSnapshot

	// Image operations.
	case ImageDelete, ImageRefresh, ImageToken:
		return entity.TypeImage

	// Volume backup operations.
	case CustomVolumeBackupRemove, CustomVolumeBackupRename, CustomVolumeBackupRestore:
		return entity.TypeStorageVolumeBackup

	// Profile operations.
	case ProfileUpdate:
		return entity.TypeProfile

	// It should never be possible to reach the default clause.
	// See the init function.
	default:
		return ""
	}
}

// ConflictAction returns the action to take if a conflicting operation is already running.
type ConflictAction int

const (
	// ConflictActionNone means operation has no conflicts, all operations of this type can run concurrently.
	ConflictActionNone ConflictAction = iota
	// ConflictActionFail asks to resolve conflicts by failing to create a new operation if a conflicting operation is already running.
	ConflictActionFail
	// ConflictActionWait asks to resolve conflicts by waiting for the conflicting operation to complete before starting a new operation.
	// TODO not implemented yet.
	ConflictActionWait
)

// ConflictAction returns the action to take if a conflicting operation is already running.
func (t Type) ConflictAction() ConflictAction {
	return ConflictActionNone
}
