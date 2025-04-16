package storage

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// InstancePath returns the directory of an instance or snapshot.
func InstancePath(instanceType instancetype.Type, projectName, instanceName string, isSnapshot bool) string {
	fullName := project.Instance(projectName, instanceName)
	if instanceType == instancetype.VM {
		if isSnapshot {
			return shared.VarPath("virtual-machines-snapshots", fullName)
		}

		return shared.VarPath("virtual-machines", fullName)
	}

	if isSnapshot {
		return shared.VarPath("snapshots", fullName)
	}

	return shared.VarPath("containers", fullName)
}

// CreateContainerMountpoint creates the provided container mountpoint and symlink.
func CreateContainerMountpoint(mountPoint string, mountPointSymlink string, privileged bool) error {
	mntPointSymlinkExist := shared.PathExists(mountPointSymlink)
	mntPointSymlinkTargetExist := shared.PathExists(mountPoint)

	var err error
	if !mntPointSymlinkTargetExist {
		err = os.MkdirAll(mountPoint, 0711)
		if err != nil {
			return err
		}
	}

	err = os.Chmod(mountPoint, 0100)
	if err != nil {
		return err
	}

	if !mntPointSymlinkExist {
		err := os.Symlink(mountPoint, mountPointSymlink)
		if err != nil {
			return err
		}
	}

	return nil
}

// CreateSnapshotMountpoint creates the provided container snapshot mountpoint
// and symlink.
func CreateSnapshotMountpoint(snapshotMountpoint string, snapshotsSymlinkTarget string, snapshotsSymlink string) error {
	snapshotMntPointExists := shared.PathExists(snapshotMountpoint)
	mntPointSymlinkExist := shared.PathExists(snapshotsSymlink)

	if !snapshotMntPointExists {
		err := os.MkdirAll(snapshotMountpoint, 0711)
		if err != nil {
			return err
		}
	}

	if !mntPointSymlinkExist {
		err := os.Symlink(snapshotsSymlinkTarget, snapshotsSymlink)
		if err != nil {
			return err
		}
	}

	return nil
}

// UsedBy returns list of API resources using storage pool. Accepts firstOnly argument to indicate that only the
// first resource using network should be returned. This can help to quickly check if the storage pool is in use.
// If memberSpecific is true, then the search is restricted to volumes that belong to this member or belong to
// all members. The ignoreVolumeType argument can be used to exclude certain volume type(s) from the list.
func UsedBy(ctx context.Context, s *state.State, pool Pool, firstOnly bool, memberSpecific bool, ignoreVolumeType ...string) ([]string, error) {
	var err error
	var usedBy []string

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Get all the volumes using the storage pool.
		poolID := pool.ID() // Create local variable to get the pointer.
		volumes, err := tx.GetStorageVolumes(ctx, memberSpecific, db.StorageVolumeFilter{PoolID: &poolID})
		if err != nil {
			return fmt.Errorf("Failed loading storage volumes: %w", err)
		}

		for _, vol := range volumes {
			var u *api.URL

			if shared.ValueInSlice(vol.Type, ignoreVolumeType) {
				continue
			}

			// Generate URL for volume based on types that map to other entities.
			switch vol.Type {
			case cluster.StoragePoolVolumeTypeNameContainer, cluster.StoragePoolVolumeTypeNameVM:
				volName, snapName, isSnap := api.GetParentAndSnapshotName(vol.Name)
				if isSnap {
					u = api.NewURL().Path(version.APIVersion, "instances", volName, "snapshots", snapName).Project(vol.Project)
				} else {
					u = api.NewURL().Path(version.APIVersion, "instances", volName).Project(vol.Project)
				}

				usedBy = append(usedBy, u.String())
			case cluster.StoragePoolVolumeTypeNameImage:
				imgProjectNames, err := tx.GetProjectsUsingImage(ctx, vol.Name)
				if err != nil {
					return fmt.Errorf("Failed loading projects using image %q: %w", vol.Name, err)
				}

				if len(imgProjectNames) > 0 {
					for _, imgProjectName := range imgProjectNames {
						u = api.NewURL().Path(version.APIVersion, "images", vol.Name).Project(imgProjectName).Target(vol.Location)
						usedBy = append(usedBy, u.String())
					}
				} else {
					// Handle orphaned image volumes that are not associated to an image.
					u = vol.URL(version.APIVersion)
					usedBy = append(usedBy, u.String())
				}

			default:
				u = vol.URL(version.APIVersion)
				usedBy = append(usedBy, u.String())
			}

			if firstOnly {
				return nil
			}
		}

		// Get all buckets using the storage pool.
		filters := []db.StorageBucketFilter{{
			PoolID: &poolID,
		}}

		buckets, err := tx.GetStoragePoolBuckets(ctx, memberSpecific, filters...)
		if err != nil {
			return fmt.Errorf("Failed loading storage buckets: %w", err)
		}

		for _, bucket := range buckets {
			u := bucket.URL(version.APIVersion, pool.Name(), bucket.Project)
			usedBy = append(usedBy, u.String())

			if firstOnly {
				return nil
			}
		}

		// Get all the profiles using the storage pool.
		profiles, err := cluster.GetProfiles(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading profiles: %w", err)
		}

		// Get all the profile devices.
		profileDevices, err := cluster.GetDevices(ctx, tx.Tx(), "profile")
		if err != nil {
			return fmt.Errorf("Failed loading profile devices: %w", err)
		}

		for _, profile := range profiles {
			for _, device := range profileDevices[profile.ID] {
				if device.Type != cluster.TypeDisk {
					continue
				}

				if device.Config["pool"] != pool.Name() {
					continue
				}

				u := api.NewURL().Path(version.APIVersion, "profiles", profile.Name).Project(profile.Project)
				usedBy = append(usedBy, u.String())

				if firstOnly {
					return nil
				}

				break
			}
		}

		return err
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(usedBy)

	return usedBy, nil
}
