package main

import (
	"context"
	"fmt"

	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/shared/entity"
)

// entityDeleter defines how to delete a specific entity type.
type entityDeleter interface {
	Delete(ctx context.Context, op *operations.Operation, s *state.State, ref entity.Reference) error
}

type instanceDeleter struct{}

// Delete deletes an instance.
func (d instanceDeleter) Delete(ctx context.Context, op *operations.Operation, s *state.State, ref entity.Reference) error {
	name := ref.Name()

	var opCreater operations.OperationCreator = func(s *state.State, args operations.OperationArgs) (*operations.Operation, error) {
		return operations.CreateUserOperationFromOperation(s, op, args)
	}

	instanceDeleteOp, err := doInstanceDelete(opCreater, s, ref.Name(), ref.ProjectName, true)
	if err != nil {
		return fmt.Errorf("Failed deleting instance %q: %w", name, err)
	}

	err = instanceDeleteOp.Start()
	if err != nil {
		return fmt.Errorf("Failed starting instance delete operation: %w", err)
	}

	err = instanceDeleteOp.Wait(ctx)
	if err != nil {
		return fmt.Errorf("Failed deleting instance %q: %w", name, err)
	}

	return nil
}

type imageDeleter struct{}

// Delete deletes an image.
func (d imageDeleter) Delete(ctx context.Context, imageDeleteOp *operations.Operation, s *state.State, ref entity.Reference) error {
	fingerprint := ref.Name()

	var imageID int
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		imageID, _, err = tx.GetImage(ctx, fingerprint, dbCluster.ImageFilter{Project: &ref.ProjectName})
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed getting image %q ID: %w", fingerprint, err)
	}

	var opCreater operations.OperationCreator = func(s *state.State, args operations.OperationArgs) (*operations.Operation, error) {
		return operations.CreateUserOperationFromOperation(s, imageDeleteOp, args)
	}

	imageDeleteOp, err = doImageDelete(ctx, opCreater, s, fingerprint, imageID, ref.ProjectName)
	if err != nil {
		return fmt.Errorf("Failed deleting image %q: %w", fingerprint, err)
	}

	err = imageDeleteOp.Start()
	if err != nil {
		return fmt.Errorf("Failed starting image delete operation: %w", err)
	}

	err = imageDeleteOp.Wait(ctx)
	if err != nil {
		return fmt.Errorf("Failed deleting image %q: %w", fingerprint, err)
	}

	return nil
}

type networkDeleter struct{}

// Delete deletes a network.
func (d networkDeleter) Delete(ctx context.Context, op *operations.Operation, s *state.State, ref entity.Reference) error {
	name := ref.Name()

	var projectConfig map[string]string
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		projectConfig, err = dbCluster.GetProjectConfig(ctx, tx.Tx(), ref.ProjectName)
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed getting project %q config: %w", ref.ProjectName, err)
	}

	err = doNetworkDelete(ctx, s, name, ref.ProjectName, projectConfig)
	if err != nil {
		return fmt.Errorf("Failed deleting network %q: %w", name, err)
	}

	return nil
}

type networkACLDeleter struct{}

// Delete deletes a network ACL.
func (d networkACLDeleter) Delete(ctx context.Context, op *operations.Operation, s *state.State, ref entity.Reference) error {
	name := ref.Name()

	err := doNetworkACLDelete(ctx, s, name, ref.ProjectName)
	if err != nil {
		return fmt.Errorf("Failed deleting network ACL %q: %w", name, err)
	}

	return nil
}

type networkZoneDeleter struct{}

// Delete deletes a network zone.
func (d networkZoneDeleter) Delete(ctx context.Context, op *operations.Operation, s *state.State, ref entity.Reference) error {
	name := ref.Name()

	err := doNetworkZoneDelete(ctx, s, name, ref.ProjectName)
	if err != nil {
		return fmt.Errorf("Failed deleting network zone %q: %w", name, err)
	}

	return nil
}

type storageVolumeDeleter struct{}

// Delete deletes a storage volume.
func (d storageVolumeDeleter) Delete(ctx context.Context, op *operations.Operation, s *state.State, ref entity.Reference) error {
	parts := ref.GetPathArgs(3)
	poolName, volType, name := parts[0], parts[1], parts[2]

	// Only delete custom storage volumes. Instance and image volumes are deleted with their parent entity.
	if volType != dbCluster.StoragePoolVolumeTypeNameCustom {
		return nil
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return fmt.Errorf("Failed loading pool %q: %w", poolName, err)
	}

	volTypeCode, err := dbCluster.StoragePoolVolumeTypeFromName(volType)
	if err != nil {
		return err
	}

	var opCreater operations.OperationCreator = func(s *state.State, args operations.OperationArgs) (*operations.Operation, error) {
		return operations.CreateUserOperationFromOperation(s, op, args)
	}

	volumeDeleteOp, err := doStoragePoolVolumeDelete(ctx, opCreater, s, name, volTypeCode, pool, ref.ProjectName, ref.ProjectName)
	if err != nil {
		return fmt.Errorf("Failed deleting storage volume %q: %w", name, err)
	}

	err = volumeDeleteOp.Start()
	if err != nil {
		return fmt.Errorf("Failed starting storage volume delete operation: %w", err)
	}

	err = volumeDeleteOp.Wait(ctx)
	if err != nil {
		return fmt.Errorf("Failed deleting storage volume %q: %w", name, err)
	}

	return nil
}

type storageBucketDeleter struct{}

// Delete deletes a storage bucket.
func (d storageBucketDeleter) Delete(ctx context.Context, op *operations.Operation, s *state.State, ref entity.Reference) error {
	parts := ref.GetPathArgs(2)
	poolName, bucketName := parts[0], parts[1]

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return fmt.Errorf("Failed loading pool %q: %w", poolName, err)
	}

	err = doStorageBucketDelete(pool, ref.ProjectName, bucketName)
	if err != nil {
		return fmt.Errorf("Failed deleting storage bucket %q: %w", bucketName, err)
	}

	return nil
}

type profileDeleter struct{}

// Delete deletes a profile.
func (d profileDeleter) Delete(ctx context.Context, op *operations.Operation, s *state.State, ref entity.Reference) error {
	name := ref.Name()

	err := doProfileDelete(ctx, s, name, ref.ProjectName)
	if err != nil {
		return fmt.Errorf("Failed deleting profile %q: %w", name, err)
	}

	return nil
}

type placementGroupDeleter struct{}

// Delete deletes a placement group.
func (d placementGroupDeleter) Delete(ctx context.Context, op *operations.Operation, s *state.State, ref entity.Reference) error {
	name := ref.Name()

	err := doPlacementGroupDelete(ctx, s, name, ref.ProjectName)
	if err != nil {
		return fmt.Errorf("Failed deleting placement group %q: %w", name, err)
	}

	return nil
}

// getEntityDeleter returns a deleter implementation for the given entity type.
func getEntityDeleter(t entity.Type) (entityDeleter, error) {
	switch t {
	case entity.TypeInstance:
		return instanceDeleter{}, nil
	case entity.TypeImage:
		return imageDeleter{}, nil
	case entity.TypeNetwork:
		return networkDeleter{}, nil
	case entity.TypeNetworkACL:
		return networkACLDeleter{}, nil
	case entity.TypeNetworkZone:
		return networkZoneDeleter{}, nil
	case entity.TypeStorageVolume:
		return storageVolumeDeleter{}, nil
	case entity.TypeStorageBucket:
		return storageBucketDeleter{}, nil
	case entity.TypeProfile:
		return profileDeleter{}, nil
	case entity.TypePlacementGroup:
		return placementGroupDeleter{}, nil
	default:
		return nil, fmt.Errorf("Unsupported entity type %q", t)
	}
}
