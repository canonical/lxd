//go:build linux && cgo && !agent

package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// ErrUnknownEntityID describes the unknown entity ID error.
var ErrUnknownEntityID = fmt.Errorf("Unknown entity ID")

// GetURIFromEntity returns the URI for the given entity type and entity ID.
func (c *Cluster) GetURIFromEntity(entityType entity.Type, entityID int) (*api.URL, error) {
	if entityType == "" {
		return nil, nil
	}

	err := entityType.Validate()
	if err != nil {
		return nil, fmt.Errorf("Failed to get URI from entity type and ID: %w", err)
	}

	var uri *api.URL

	switch entityType {
	case entity.TypeImage:
		var images []cluster.Image

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			images, err = cluster.GetImages(ctx, tx.tx)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("Failed to get images: %w", err)
		}

		for _, image := range images {
			if image.ID != entityID {
				continue
			}

			uri, err = entityType.URL(image.Project, "", image.Fingerprint)
			if err != nil {
				return nil, fmt.Errorf("Failed to get image URL: %w", err)
			}

			break
		}

		if uri == nil {
			return nil, ErrUnknownEntityID
		}

	case entity.TypeProfile:
		var profiles []cluster.Profile

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			profiles, err = cluster.GetProfiles(ctx, tx.Tx())
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("Failed to get profiles: %w", err)
		}

		for _, profile := range profiles {
			if profile.ID != entityID {
				continue
			}

			uri, err = entityType.URL(profile.Project, "", profile.Name)
			if err != nil {
				return nil, fmt.Errorf("Failed to get profile URL: %w", err)
			}

			break
		}

		if uri == nil {
			return nil, ErrUnknownEntityID
		}

	case entity.TypeProject:
		projects := make(map[int64]string)

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			projects, err = cluster.GetProjectIDsToNames(context.Background(), tx.tx)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("Failed to get project names and IDs: %w", err)
		}

		name, ok := projects[int64(entityID)]
		if !ok {
			return nil, ErrUnknownEntityID
		}

		uri, err = entityType.URL("", "", name)
		if err != nil {
			return nil, fmt.Errorf("Failed to get project URL: %w", err)
		}

	case entity.TypeCertificate:
		var certificates []cluster.Certificate

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			certificates, err = cluster.GetCertificates(context.Background(), tx.tx)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("Failed to get certificates: %w", err)
		}

		for _, cert := range certificates {
			if cert.ID != entityID {
				continue
			}

			uri, err = entityType.URL("", "", cert.Fingerprint)
			if err != nil {
				return nil, fmt.Errorf("Failed to get certificate URL: %w", err)
			}

			break
		}

		if uri == nil {
			return nil, ErrUnknownEntityID
		}

	case entity.TypeContainer:
		fallthrough
	case entity.TypeInstance:
		var instances []cluster.Instance

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			instances, err = cluster.GetInstances(ctx, tx.tx)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("Failed to get instances: %w", err)
		}

		for _, instance := range instances {
			if instance.ID != entityID {
				continue
			}

			uri, err = entityType.URL(instance.Project, "", instance.Name)
			if err != nil {
				return nil, fmt.Errorf("Failed to get instance URL: %w", err)
			}

			break
		}

		if uri == nil {
			return nil, ErrUnknownEntityID
		}

	case entity.TypeInstanceBackup:
		instanceBackup, err := c.GetInstanceBackupWithID(entityID)
		if err != nil {
			return nil, fmt.Errorf("Failed to get instance backup: %w", err)
		}

		var instances []cluster.Instance

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			instances, err = cluster.GetInstances(ctx, tx.tx)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("Failed to get instances: %w", err)
		}

		for _, instance := range instances {
			if instance.ID != instanceBackup.InstanceID {
				continue
			}

			uri, err = entityType.URL(instance.Project, "", instance.Name, instanceBackup.Name)
			if err != nil {
				return nil, fmt.Errorf("Failed to get instance backup URL: %w", err)
			}

			break
		}

		if uri == nil {
			return nil, ErrUnknownEntityID
		}

	case entity.TypeInstanceSnapshot:
		var snapshots []cluster.InstanceSnapshot

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			snapshots, err = cluster.GetInstanceSnapshots(ctx, tx.Tx())
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("Failed to get instance snapshots: %w", err)
		}

		for _, snapshot := range snapshots {
			if snapshot.ID != entityID {
				continue
			}

			uri, err = entityType.URL(snapshot.Project, "", snapshot.Instance, snapshot.Name)
			if err != nil {
				return nil, fmt.Errorf("Failed to get instance snapshot URL: %w", err)
			}

			break
		}

		if uri == nil {
			return nil, ErrUnknownEntityID
		}

	case entity.TypeNetwork:
		networkName, projectName, err := c.GetNetworkNameAndProjectWithID(entityID)
		if err != nil {
			return nil, fmt.Errorf("Failed to get network name and project name: %w", err)
		}

		uri, err = entityType.URL(projectName, "", networkName)
		if err != nil {
			return nil, fmt.Errorf("Failed to get network URL: %w", err)
		}

	case entity.TypeNetworkACL:
		networkACLName, projectName, err := c.GetNetworkACLNameAndProjectWithID(entityID)
		if err != nil {
			return nil, fmt.Errorf("Failed to get network ACL name and project name: %w", err)
		}

		uri, err = entityType.URL(projectName, "", networkACLName)
		if err != nil {
			return nil, fmt.Errorf("Failed to get network ACL URL: %w", err)
		}

	case entity.TypeNode:
		var nodeInfo NodeInfo

		err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			nodeInfo, err = tx.GetNodeWithID(ctx, entityID)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("Failed to get node information: %w", err)
		}

		uri, err = entityType.URL("", "", nodeInfo.Name)
		if err != nil {
			return nil, fmt.Errorf("Failed to get cluster member URL: %w", err)
		}

	case entity.TypeOperation:
		var op cluster.Operation

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			id := int64(entityID)
			filter := cluster.OperationFilter{ID: &id}
			ops, err := cluster.GetOperations(ctx, tx.tx, filter)
			if err != nil {
				return err
			}

			if len(ops) > 1 {
				return fmt.Errorf("More than one operation matches")
			}

			op = ops[0]
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("Failed to get operation: %w", err)
		}

		uri, err = entityType.URL("", "", op.UUID)
		if err != nil {
			return nil, fmt.Errorf("Failed to get operation URL: %w", err)
		}

	case entity.TypeStoragePool:
		_, pool, _, err := c.GetStoragePoolWithID(entityID)
		if err != nil {
			return nil, fmt.Errorf("Failed to get storage pool: %w", err)
		}

		uri, err = entityType.URL("", "", pool.Name)
		if err != nil {
			return nil, fmt.Errorf("Failed to get storage pool URL: %w", err)
		}

	case entity.TypeStorageVolume:
		var args StorageVolumeArgs

		err := c.Transaction(c.closingCtx, func(ctx context.Context, tx *ClusterTx) error {
			args, err = tx.GetStoragePoolVolumeWithID(ctx, entityID)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("Failed to get storage volume: %w", err)
		}

		uri, err = entityType.URL(args.ProjectName, "", args.PoolName, args.TypeName, args.Name)
		if err != nil {
			return nil, fmt.Errorf("Failed to get storage volume URL: %w", err)
		}

	case entity.TypeStorageVolumeBackup:
		backup, err := c.GetStoragePoolVolumeBackupWithID(entityID)
		if err != nil {
			return nil, fmt.Errorf("Failed to get volume backup: %w", err)
		}

		var volume StorageVolumeArgs

		err = c.Transaction(c.closingCtx, func(ctx context.Context, tx *ClusterTx) error {
			volume, err = tx.GetStoragePoolVolumeWithID(ctx, int(backup.VolumeID))
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("Failed to get storage volume: %w", err)
		}

		uri, err = entityType.URL(volume.ProjectName, "", volume.PoolName, volume.TypeName, volume.Name, backup.Name)
		if err != nil {
			return nil, fmt.Errorf("Failed to get storage volume backup URL: %w", err)
		}

	case entity.TypeStorageVolumeSnapshot:
		snapshot, err := c.GetStorageVolumeSnapshotWithID(entityID)
		if err != nil {
			return nil, fmt.Errorf("Failed to get volume snapshot: %w", err)
		}

		fields := strings.Split(snapshot.Name, "/")
		uri, err = entityType.URL(snapshot.ProjectName, "", snapshot.PoolName, snapshot.TypeName, fields[0], fields[1])
		if err != nil {
			return nil, fmt.Errorf("Failed to get storage volume snapshot URL: %w", err)
		}
	}

	return uri, nil
}
