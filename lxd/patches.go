package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

/* Patches are one-time actions that are sometimes needed to update
   existing container configuration or move things around on the
   filesystem.

   Those patches are applied at startup time after the database schema
   has been fully updated. Patches can therefore assume a working database.

   At the time the patches are applied, the containers aren't started
   yet and the daemon isn't listening to requests.

   DO NOT use this mechanism for database update. Schema updates must be
   done through the separate schema update mechanism.


   Only append to the patches list, never remove entries and never re-order them.
*/

var patches = []patch{
	{name: "invalid_profile_names", run: patchInvalidProfileNames},
	{name: "leftover_profile_config", run: patchLeftoverProfileConfig},
	{name: "network_permissions", run: patchNetworkPermissions},
	{name: "storage_api", run: patchStorageApi},
	{name: "storage_api_v1", run: patchStorageApiV1},
	{name: "storage_api_dir_cleanup", run: patchStorageApiDirCleanup},
}

type patch struct {
	name string
	run  func(name string, d *Daemon) error
}

func (p *patch) apply(d *Daemon) error {
	shared.LogDebugf("Applying patch: %s", p.name)

	err := p.run(p.name, d)
	if err != nil {
		return err
	}

	err = dbPatchesMarkApplied(d.db, p.name)
	if err != nil {
		return err
	}

	return nil
}

func patchesApplyAll(d *Daemon) error {
	appliedPatches, err := dbPatches(d.db)
	if err != nil {
		return err
	}

	for _, patch := range patches {
		if shared.StringInSlice(patch.name, appliedPatches) {
			continue
		}

		err := patch.apply(d)
		if err != nil {
			return err
		}
	}

	return nil
}

// Patches begin here
func patchLeftoverProfileConfig(name string, d *Daemon) error {
	stmt := `
DELETE FROM profiles_config WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices WHERE profile_id NOT IN (SELECT id FROM profiles);
DELETE FROM profiles_devices_config WHERE profile_device_id NOT IN (SELECT id FROM profiles_devices);
`

	_, err := d.db.Exec(stmt)
	if err != nil {
		return err
	}

	return nil
}

func patchInvalidProfileNames(name string, d *Daemon) error {
	profiles, err := dbProfiles(d.db)
	if err != nil {
		return err
	}

	for _, profile := range profiles {
		if strings.Contains(profile, "/") || shared.StringInSlice(profile, []string{".", ".."}) {
			shared.LogInfo("Removing unreachable profile (invalid name)", log.Ctx{"name": profile})
			err := dbProfileDelete(d.db, profile)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func patchNetworkPermissions(name string, d *Daemon) error {
	// Get the list of networks
	networks, err := dbNetworks(d.db)
	if err != nil {
		return err
	}

	// Fix the permissions
	err = os.Chmod(shared.VarPath("networks"), 0711)
	if err != nil {
		return err
	}

	for _, network := range networks {
		if !shared.PathExists(shared.VarPath("networks", network)) {
			continue
		}

		err = os.Chmod(shared.VarPath("networks", network), 0711)
		if err != nil {
			return err
		}

		if shared.PathExists(shared.VarPath("networks", network, "dnsmasq.hosts")) {
			err = os.Chmod(shared.VarPath("networks", network, "dnsmasq.hosts"), 0644)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func patchStorageApi(name string, d *Daemon) error {
	lvmVgName := daemonConfig["storage.lvm_vg_name"].Get()
	zfsPoolName := daemonConfig["storage.zfs_pool_name"].Get()
	defaultPoolName := "default"
	preStorageApiStorageType := storageTypeDir

	if lvmVgName != "" {
		preStorageApiStorageType = storageTypeLvm
		defaultPoolName = lvmVgName
	} else if zfsPoolName != "" {
		preStorageApiStorageType = storageTypeZfs
		defaultPoolName = zfsPoolName
	} else if d.BackingFs == "btrfs" {
		preStorageApiStorageType = storageTypeBtrfs
	} else {
		// Dir storage pool.
	}

	defaultStorageTypeName, err := storageTypeToString(preStorageApiStorageType)
	if err != nil {
		return err
	}

	// In case we detect that an lvm name or a zfs name exists it makes
	// sense to create a storage pool in the database, independent of
	// whether anything currently exists on that pool. We can still probably
	// safely assume that the user at least once used that pool.
	// However, when we detect {dir, btrfs}, we can't rely on that guess
	// since the daemon doesn't record any name for the pool anywhere.  So
	// in the {dir, btrfs} case we check whether anything exists on the
	// pool, if not, then we don't create a default pool. The user will then
	// be forced to run lxd init again and can start from a pristine state.
	// Check if this LXD instace currently has any containers, snapshots, or
	// images configured. If so, we create a default storage pool in the
	// database. Otherwise, the user will have to run LXD init.
	cRegular, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return err
	}

	// Get list of existing snapshots.
	cSnapshots, err := dbContainersList(d.db, cTypeSnapshot)
	if err != nil {
		return err
	}

	// Get list of existing public images.
	imgPublic, err := dbImagesGet(d.db, true)
	if err != nil {
		return err
	}

	// Get list of existing private images.
	imgPrivate, err := dbImagesGet(d.db, false)
	if err != nil {
		return err
	}

	// Nothing exists on the pool so we're not creating a default one,
	// thereby forcing the user to run lxd init.
	if len(cRegular) == 0 && len(cSnapshots) == 0 && len(imgPublic) == 0 && len(imgPrivate) == 0 {
		return nil
	}

	// If any of these are actually called, there's no way back.
	poolName := defaultPoolName
	switch preStorageApiStorageType {
	case storageTypeBtrfs:
		err = upgradeFromStorageTypeBtrfs(name, d, defaultPoolName, defaultStorageTypeName, cRegular, cSnapshots, imgPublic, imgPrivate)
	case storageTypeDir:
		err = upgradeFromStorageTypeDir(name, d, defaultPoolName, defaultStorageTypeName, cRegular, cSnapshots, imgPublic, imgPrivate)
	case storageTypeLvm:
		err = upgradeFromStorageTypeLvm(name, d, defaultPoolName, defaultStorageTypeName, cRegular, cSnapshots, imgPublic, imgPrivate)
	case storageTypeZfs:
		// The user is using a zfs dataset. This case needs to be
		// handled with care:

		// - The pool name that is used in the storage backends needs
		//   to be set to a sane name that doesn't contain a slash "/".
		//   This is what this snippet is for.
		// - The full dataset name <pool_name>/<volume_name> needs to be
		//   set as the source value.
		if strings.Contains(defaultPoolName, "/") {
			poolName = "default"
		}
		err = upgradeFromStorageTypeZfs(name, d, defaultPoolName, defaultStorageTypeName, cRegular, []string{}, imgPublic, imgPrivate)
	default: // Shouldn't happen.
		return fmt.Errorf("Invalid storage type. Upgrading not possible.")
	}
	if err != nil {
		return err
	}

	// The new storage api enforces that the default storage pool on which
	// containers are created is set in the default profile. If it isn't
	// set, then LXD will refuse to create a container until either an
	// appropriate device including a pool is added to the default profile
	// or the user explicitly passes the pool the container's storage volume
	// is supposed to be created on.
	allcontainers := append(cRegular, cSnapshots...)
	err = updatePoolPropertyForAllObjects(d, poolName, allcontainers)
	if err != nil {
		return err
	}

	// Unset deprecated storage keys.
	daemonConfig["storage.lvm_fstype"].Set(d, "")
	daemonConfig["storage.lvm_mount_options"].Set(d, "")
	daemonConfig["storage.lvm_thinpool_name"].Set(d, "")
	daemonConfig["storage.lvm_vg_name"].Set(d, "")
	daemonConfig["storage.lvm_volume_size"].Set(d, "")
	daemonConfig["storage.zfs_pool_name"].Set(d, "")
	daemonConfig["storage.zfs_remove_snapshots"].Set(d, "")
	daemonConfig["storage.zfs_use_refquota"].Set(d, "")

	return d.SetupStorageDriver()
}

func upgradeFromStorageTypeBtrfs(name string, d *Daemon, defaultPoolName string, defaultStorageTypeName string, cRegular []string, cSnapshots []string, imgPublic []string, imgPrivate []string) error {
	poolConfig := map[string]string{}
	poolSubvolumePath := getStoragePoolMountPoint(defaultPoolName)
	poolConfig["source"] = poolSubvolumePath

	poolID := int64(-1)
	_, err := dbStoragePools(d.db)
	if err == nil { // Already exist valid storage pools.
		// Get the pool ID as we need it for storage volume creation.
		// (Use a tmp variable as Go's scoping is freaking me out.)
		tmp, err := dbStoragePoolGetID(d.db, defaultPoolName)
		if err != nil {
			shared.LogErrorf("Failed to query database: %s.", err)
			return err
		}
		poolID = tmp
	} else if err == NoSuchObjectError { // Likely a pristine upgrade.
		tmp, err := dbStoragePoolCreate(d.db, defaultPoolName, defaultStorageTypeName, poolConfig)
		if err != nil {
			return err
		}
		poolID = tmp

		s, err := storagePoolInit(d, defaultPoolName)
		if err != nil {
			return err
		}

		err = s.StoragePoolCreate()
		if err != nil {
			return err
		}
	} else { // Shouldn't happen.
		shared.LogErrorf("Failed to query database: %s.", err)
		return err
	}

	// Create storage volumes in the database.
	volumeConfig := map[string]string{}

	if len(cRegular) > 0 {
		// ${LXD_DIR}/storage-pools/<name>
		containersSubvolumePath := getContainerMountPoint(defaultPoolName, "")
		if !shared.PathExists(containersSubvolumePath) {
			err := os.MkdirAll(containersSubvolumePath, 0711)
			if err != nil {
				return err
			}
		}
	}

	for _, ct := range cRegular {
		// Create new db entry in the storage volumes table for the
		// container.
		_, err := dbStoragePoolVolumeGetTypeID(d.db, ct, storagePoolVolumeTypeContainer, poolID)
		if err == nil {
			shared.LogWarnf("Storage volumes database already contains an entry for the container.")
		} else if err == NoSuchObjectError {
			// Insert storage volumes for containers into the database.
			_, err := dbStoragePoolVolumeCreate(d.db, ct, storagePoolVolumeTypeContainer, poolID, volumeConfig)
			if err != nil {
				shared.LogErrorf("Could not insert a storage volume for container \"%s\".", ct)
				return err
			}
		} else {
			shared.LogErrorf("Failed to query database: %s", err)
			return err
		}

		// Rename the btrfs subvolume and making it a
		// subvolume of the subvolume of the storage pool:
		// mv ${LXD_DIR}/containers/<container_name> ${LXD_DIR}/storage-pools/<pool>/<container_name>
		oldContainerMntPoint := shared.VarPath("containers", ct)
		newContainerMntPoint := getContainerMountPoint(defaultPoolName, ct)
		if shared.PathExists(oldContainerMntPoint) {
			err = os.Rename(oldContainerMntPoint, newContainerMntPoint)
			if err != nil {
				return err
			}
		}

		// Create a symlink to the mountpoint of the container:
		// ${LXD_DIR}/containers/<container_name> ->
		// ${LXD_DIR}/storage-pools/<pool>/containers/<container_name>
		doesntMatter := false
		err = createContainerMountpoint(newContainerMntPoint, oldContainerMntPoint, doesntMatter)
		if err != nil {
			return err
		}

		// Check if we need to account for snapshots for this container.
		ctSnapshots, err := dbContainerGetSnapshots(d.db, ct)
		if err != nil {
			return err
		}

		if len(ctSnapshots) > 0 {
			// Create the snapshots directory in
			// the new storage pool:
			// ${LXD_DIR}/storage-pools/<pool>/snapshots
			newSnapshotsMntPoint := getSnapshotMountPoint(defaultPoolName, ct)
			if !shared.PathExists(newSnapshotsMntPoint) {
				err := os.MkdirAll(newSnapshotsMntPoint, 0700)
				if err != nil {
					return err
				}
			}
		}

		for _, cs := range ctSnapshots {
			// Insert storage volumes for snapshots into the
			// database. Note that snapshots have already been moved
			// and symlinked above. So no need to do any work here.
			_, err := dbStoragePoolVolumeGetTypeID(d.db, cs, storagePoolVolumeTypeContainer, poolID)
			if err == nil {
				shared.LogWarnf("Storage volumes database already contains an entry for the container.")
				// For btrfs we need to assume that the btrfs
				// execs below succeeded.
				continue
			} else if err == NoSuchObjectError {
				// Insert storage volumes for containers into the database.
				_, err := dbStoragePoolVolumeCreate(d.db, cs, storagePoolVolumeTypeContainer, poolID, volumeConfig)
				if err != nil {
					shared.LogErrorf("Could not insert a storage volume for snapshot \"%s\".", cs)
					return err
				}
			} else {
				shared.LogErrorf("Failed to query database: %s", err)
				return err
			}

			// We need to create a new snapshot since we can't move
			// readonly snapshots.
			oldSnapshotMntPoint := shared.VarPath("snapshots", cs)
			newSnapshotMntPoint := getSnapshotMountPoint(defaultPoolName, cs)
			err = exec.Command(
				"btrfs",
				"subvolume",
				"snapshot",
				"-r",
				oldSnapshotMntPoint,
				newSnapshotMntPoint).Run()
			if err != nil {
				return err
			}

			// Delete the old subvolume.
			err = exec.Command(
				"btrfs",
				"subvolume",
				"delete",
				oldSnapshotMntPoint,
			).Run()
			if err != nil {
				return err
			}
		}

		if len(ctSnapshots) > 0 {
			// Create a new symlink from the snapshots directory of
			// the container to the snapshots directory on the
			// storage pool:
			// ${LXD_DIR}/snapshots/<container_name> -> ${LXD_DIR}/storage-pools/<pool>/snapshots/<container_name>
			snapshotsPath := shared.VarPath("snapshots", ct)
			newSnapshotMntPoint := getSnapshotMountPoint(defaultPoolName, ct)
			os.Remove(snapshotsPath)
			if !shared.PathExists(snapshotsPath) {
				err := os.Symlink(newSnapshotMntPoint, snapshotsPath)
				if err != nil {
					return err
				}
			}
		}

	}

	// Insert storage volumes for images into the database. Images don't
	// move. The tarballs remain in their original location.
	images := append(imgPublic, imgPrivate...)
	for _, img := range images {
		_, err := dbStoragePoolVolumeGetTypeID(d.db, img, storagePoolVolumeTypeImage, poolID)
		if err == nil {
			shared.LogWarnf("Storage volumes database already contains an entry for the container.")
		} else if err == NoSuchObjectError {
			// Insert storage volumes for containers into the database.
			_, err := dbStoragePoolVolumeCreate(d.db, img, storagePoolVolumeTypeImage, poolID, volumeConfig)
			if err != nil {
				shared.LogWarnf("Could not insert a storage volume for image \"%s\".", img)
				return err
			}
		} else {
			shared.LogErrorf("Failed to query database: %s", err)
			return err
		}

		imagesMntPoint := getImageMountPoint(defaultPoolName, "")
		if !shared.PathExists(imagesMntPoint) {
			err := os.MkdirAll(imagesMntPoint, 0700)
			if err != nil {
				return err
			}
		}

		oldImageMntPoint := shared.VarPath("images", img+".btrfs")
		newImageMntPoint := getImageMountPoint(defaultPoolName, img)
		if shared.PathExists(oldImageMntPoint) {
			err := os.Rename(oldImageMntPoint, newImageMntPoint)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func upgradeFromStorageTypeDir(name string, d *Daemon, defaultPoolName string, defaultStorageTypeName string, cRegular []string, cSnapshots []string, imgPublic []string, imgPrivate []string) error {
	poolConfig := map[string]string{}
	poolConfig["source"] = shared.VarPath("storage-pools", defaultPoolName)

	poolID := int64(-1)
	_, err := dbStoragePools(d.db)
	if err == nil { // Already exist valid storage pools.
		// Get the pool ID as we need it for storage volume creation.
		// (Use a tmp variable as Go's scoping is freaking me out.)
		tmp, err := dbStoragePoolGetID(d.db, defaultPoolName)
		if err != nil {
			shared.LogErrorf("Failed to query database: %s.", err)
			return err
		}
		poolID = tmp
	} else if err == NoSuchObjectError { // Likely a pristine upgrade.
		tmp, err := dbStoragePoolCreate(d.db, defaultPoolName, defaultStorageTypeName, poolConfig)
		if err != nil {
			return err
		}
		poolID = tmp

		s, err := storagePoolInit(d, defaultPoolName)
		if err != nil {
			return err
		}

		err = s.StoragePoolCreate()
		if err != nil {
			return err
		}
	} else { // Shouldn't happen.
		shared.LogErrorf("Failed to query database: %s.", err)
		return err
	}

	// Create storage volumes in the database.
	volumeConfig := map[string]string{}
	// Insert storage volumes for containers into the database.
	for _, ct := range cRegular {
		_, err := dbStoragePoolVolumeGetTypeID(d.db, ct, storagePoolVolumeTypeContainer, poolID)
		if err == nil {
			shared.LogWarnf("Storage volumes database already contains an entry for the container.")
		} else if err == NoSuchObjectError {
			// Insert storage volumes for containers into the database.
			_, err := dbStoragePoolVolumeCreate(d.db, ct, storagePoolVolumeTypeContainer, poolID, volumeConfig)
			if err != nil {
				shared.LogErrorf("Could not insert a storage volume for container \"%s\".", ct)
				return err
			}
		} else {
			shared.LogErrorf("Failed to query database: %s", err)
			return err
		}

		// Create the new path where containers will be located on the
		// new storage api.
		containersMntPoint := getContainerMountPoint(defaultPoolName, "")
		if !shared.PathExists(containersMntPoint) {
			err := os.MkdirAll(containersMntPoint, 0711)
			if err != nil {
				return err
			}
		}

		// Simply rename the container when they are directories.
		oldContainerMntPoint := shared.VarPath("containers", ct)
		newContainerMntPoint := getContainerMountPoint(defaultPoolName, ct)
		if shared.PathExists(oldContainerMntPoint) {
			err := os.Rename(oldContainerMntPoint, newContainerMntPoint)
			if err != nil {
				return err
			}
		}

		doesntMatter := false
		err = createContainerMountpoint(newContainerMntPoint, oldContainerMntPoint, doesntMatter)
		if err != nil {
			return err
		}

		// Check if we need to account for snapshots for this container.
		oldSnapshotMntPoint := shared.VarPath("snapshots", ct)
		if !shared.PathExists(oldSnapshotMntPoint) {
			continue
		}

		// If the snapshots directory for that container is empty,
		// remove it.
		isEmpty, err := shared.PathIsEmpty(oldSnapshotMntPoint)
		if isEmpty {
			os.Remove(oldSnapshotMntPoint)
			continue
		}

		// Create the new path where snapshots will be located on the
		// new storage api.
		snapshotsMntPoint := shared.VarPath("storage-pools", defaultPoolName, "snapshots")
		if !shared.PathExists(snapshotsMntPoint) {
			err := os.MkdirAll(snapshotsMntPoint, 0711)
			if err != nil {
				return err
			}
		}

		// Now simply rename the snapshots directory as well.
		newSnapshotMntPoint := getSnapshotMountPoint(defaultPoolName, ct)
		if shared.PathExists(oldSnapshotMntPoint) {
			err := os.Rename(oldSnapshotMntPoint, newSnapshotMntPoint)
			if err != nil {
				return err
			}
		}

		// Create a symlink for this container.  snapshots.
		err = createSnapshotMountpoint(newSnapshotMntPoint, newSnapshotMntPoint, oldSnapshotMntPoint)
		if err != nil {
			return err
		}
	}

	// Insert storage volumes for snapshots into the database. Note that
	// snapshots have already been moved and symlinked above. So no need to
	// do any work here.
	for _, cs := range cSnapshots {
		// Insert storage volumes for snapshots into the
		// database. Note that snapshots have already been moved
		// and symlinked above. So no need to do any work here.
		_, err := dbStoragePoolVolumeGetTypeID(d.db, cs, storagePoolVolumeTypeContainer, poolID)
		if err == nil {
			shared.LogWarnf("Storage volumes database already contains an entry for the container.")
		} else if err == NoSuchObjectError {
			// Insert storage volumes for containers into the database.
			_, err := dbStoragePoolVolumeCreate(d.db, cs, storagePoolVolumeTypeContainer, poolID, volumeConfig)
			if err != nil {
				shared.LogErrorf("Could not insert a storage volume for snapshot \"%s\".", cs)
				return err
			}
		} else {
			shared.LogErrorf("Failed to query database: %s", err)
			return err
		}
	}

	// Insert storage volumes for images into the database. Images don't
	// move. The tarballs remain in their original location.
	images := append(imgPublic, imgPrivate...)
	for _, img := range images {
		_, err := dbStoragePoolVolumeGetTypeID(d.db, img, storagePoolVolumeTypeImage, poolID)
		if err == nil {
			shared.LogWarnf("Storage volumes database already contains an entry for the container.")
		} else if err == NoSuchObjectError {
			// Insert storage volumes for containers into the database.
			_, err := dbStoragePoolVolumeCreate(d.db, img, storagePoolVolumeTypeImage, poolID, volumeConfig)
			if err != nil {
				shared.LogWarnf("Could not insert a storage volume for image \"%s\".", img)
				return err
			}
		} else {
			shared.LogErrorf("Failed to query database: %s", err)
			return err
		}
	}

	return nil
}

func upgradeFromStorageTypeLvm(name string, d *Daemon, defaultPoolName string, defaultStorageTypeName string, cRegular []string, cSnapshots []string, imgPublic []string, imgPrivate []string) error {
	poolConfig := map[string]string{}
	poolConfig["source"] = defaultPoolName
	poolConfig["volume.lvm.thinpool_name"] = daemonConfig["storage.lvm_thinpool_name"].Get()
	poolConfig["volume.block.filesystem"] = daemonConfig["storage.lvm_fstype"].Get()
	poolConfig["volume.block.mount_options"] = daemonConfig["storage.lvm_mount_options"].Get()

	// Get size of the volume group.
	output, err := tryExec("vgs", "--nosuffix", "--units", "g", "--noheadings", "-o", "size", defaultPoolName)
	if err != nil {
		return err
	}
	tmp := string(output)
	tmp = strings.TrimSpace(tmp)
	szFloat, err := strconv.ParseFloat(tmp, 32)
	if err != nil {
		return err
	}
	szInt64 := shared.Round(szFloat)
	poolConfig["size"] = fmt.Sprintf("%dGB", szInt64)

	err = storagePoolValidateConfig(defaultPoolName, "lvm", poolConfig)
	if err != nil {
		return err
	}

	// Peek into the storage pool database to see whether any storage pools
	// are already configured. If so, we can assume that a partial upgrade
	// has been performed and can skip the next steps.
	poolID := int64(-1)
	pools, err := dbStoragePools(d.db)
	if err == nil { // Already exist valid storage pools.
		// Check if the storage pool already has a db entry.
		if shared.StringInSlice(defaultPoolName, pools) {
			shared.LogWarnf("Database already contains a valid entry for the storage pool: %s.", defaultPoolName)
		}

		// Get the pool ID as we need it for storage volume creation.
		// (Use a tmp variable as Go's scoping is freaking me out.)
		tmp, err := dbStoragePoolGetID(d.db, defaultPoolName)
		if err != nil {
			shared.LogErrorf("Failed to query database: %s.", err)
			return err
		}
		poolID = tmp
	} else if err == NoSuchObjectError { // Likely a pristine upgrade.
		tmp, err := dbStoragePoolCreate(d.db, defaultPoolName, defaultStorageTypeName, poolConfig)
		if err != nil {
			return err
		}
		poolID = tmp
	} else { // Shouldn't happen.
		shared.LogErrorf("Failed to query database: %s.", err)
		return err
	}

	poolMntPoint := getStoragePoolMountPoint(defaultPoolName)
	if !shared.PathExists(poolMntPoint) {
		err = os.MkdirAll(poolMntPoint, 0711)
		if err != nil {
			shared.LogWarnf("Failed to create pool mountpoint: %s", poolMntPoint)
		}
	}

	// Create storage volumes in the database.
	volumeConfig := map[string]string{}

	if len(cRegular) > 0 {
		newContainersMntPoint := getContainerMountPoint(defaultPoolName, "")
		if !shared.PathExists(newContainersMntPoint) {
			err = os.MkdirAll(newContainersMntPoint, 0711)
			if err != nil {
				shared.LogWarnf("Failed to create containers mountpoint: %s", newContainersMntPoint)
			}
		}
	}

	// Insert storage volumes for containers into the database.
	for _, ct := range cRegular {
		_, err := dbStoragePoolVolumeGetTypeID(d.db, ct, storagePoolVolumeTypeContainer, poolID)
		if err == nil {
			shared.LogWarnf("Storage volumes database already contains an entry for the container.")
		} else if err == NoSuchObjectError {
			// Insert storage volumes for containers into the database.
			_, err := dbStoragePoolVolumeCreate(d.db, ct, storagePoolVolumeTypeContainer, poolID, volumeConfig)
			if err != nil {
				shared.LogErrorf("Could not insert a storage volume for container \"%s\".", ct)
				return err
			}
		} else {
			shared.LogErrorf("Failed to query database: %s", err)
			return err
		}

		// Unmount the logical volume.
		oldContainerMntPoint := shared.VarPath("containers", ct)
		if shared.IsMountPoint(oldContainerMntPoint) {
			err := tryUnmount(oldContainerMntPoint, 0)
			if err != nil {
				return err
			}
		}

		// Create the new path where containers will be located on the
		// new storage api. We do os.Rename() here to preserve
		// permissions and ownership.
		newContainerMntPoint := getContainerMountPoint(defaultPoolName, ct)
		if !shared.PathExists(newContainerMntPoint) {
			err = os.Rename(oldContainerMntPoint, newContainerMntPoint)
			if err != nil {
				return err
			}
		}

		if shared.PathExists(oldContainerMntPoint + ".lv") {
			err := os.Remove(oldContainerMntPoint + ".lv")
			if err != nil {
				return err
			}
		}

		// Rename the logical volume device.
		ctLvName := containerNameToLVName(ct)
		newContainerLvName := fmt.Sprintf("%s_%s", storagePoolVolumeApiEndpointContainers, ctLvName)
		_, err = tryExec("lvrename", defaultPoolName, ctLvName, newContainerLvName)
		if err != nil {
			return err
		}

		// Create the new container mountpoint.
		doesntMatter := false
		err = createContainerMountpoint(newContainerMntPoint, oldContainerMntPoint, doesntMatter)
		if err != nil {
			return err
		}

		lvFsType := poolConfig["volume.block.filesystem"]
		mountOptions := poolConfig["volume.block.mount_options"]
		containerLvDevPath := fmt.Sprintf("/dev/%s/%s_%s", defaultPoolName, storagePoolVolumeApiEndpointContainers, ctLvName)
		err = tryMount(containerLvDevPath, newContainerMntPoint, lvFsType, 0, mountOptions)
		if err != nil {
			return err
		}

		// Check if we need to account for snapshots for this container.
		ctSnapshots, err := dbContainerGetSnapshots(d.db, ct)
		if err != nil {
			return err
		}

		for _, cs := range ctSnapshots {
			// Insert storage volumes for snapshots into the
			// database. Note that snapshots have already been moved
			// and symlinked above. So no need to do any work here.
			_, err := dbStoragePoolVolumeGetTypeID(d.db, cs, storagePoolVolumeTypeContainer, poolID)
			if err == nil {
				shared.LogWarnf("Storage volumes database already contains an entry for the container.")
			} else if err == NoSuchObjectError {
				// Insert storage volumes for containers into the database.
				_, err := dbStoragePoolVolumeCreate(d.db, cs, storagePoolVolumeTypeContainer, poolID, volumeConfig)
				if err != nil {
					shared.LogErrorf("Could not insert a storage volume for snapshot \"%s\".", cs)
					return err
				}
			} else {
				shared.LogErrorf("Failed to query database: %s", err)
				return err
			}

			// Create the snapshots directory in the new storage
			// pool:
			// ${LXD_DIR}/storage-pools/<pool>/snapshots
			newSnapshotMntPoint := getSnapshotMountPoint(defaultPoolName, cs)
			if !shared.PathExists(newSnapshotMntPoint) {
				err := os.MkdirAll(newSnapshotMntPoint, 0700)
				if err != nil {
					return err
				}
			}

			// Unmount the logical volume.
			oldSnapshotMntPoint := shared.VarPath("snapshots", cs)
			if shared.IsMountPoint(oldSnapshotMntPoint) {
				err := tryUnmount(oldSnapshotMntPoint, 0)
				if err != nil {
					return err
				}
			}

			// Rename the snapshot mountpoint to preserve acl's and
			// so on.
			if shared.PathExists(oldSnapshotMntPoint) {
				err := os.Rename(oldSnapshotMntPoint, newSnapshotMntPoint)
				if err != nil {
					return err
				}
			}

			os.Remove(oldSnapshotMntPoint + ".lv")

			// Make sure we use a valid lv name.
			csLvName := containerNameToLVName(cs)
			newSnapshotLvName := fmt.Sprintf("%s_%s", storagePoolVolumeApiEndpointContainers, csLvName)
			_, err = tryExec("lvrename", defaultPoolName, csLvName, newSnapshotLvName)
			if err != nil {
				return err
			}

		}

		if len(ctSnapshots) > 0 {
			// Create a new symlink from the snapshots directory of
			// the container to the snapshots directory on the
			// storage pool:
			// ${LXD_DIR}/snapshots/<container_name> -> ${LXD_DIR}/storage-pools/<pool>/snapshots/<container_name>
			snapshotsPath := shared.VarPath("snapshots", ct)
			newSnapshotsPath := getSnapshotMountPoint(defaultPoolName, ct)
			if shared.PathExists(snapshotsPath) {
				err := os.Remove(snapshotsPath)
				if err != nil {
					return err
				}
			}
			if !shared.PathExists(snapshotsPath) {
				err = os.Symlink(newSnapshotsPath, snapshotsPath)
				if err != nil {
					return err
				}
			}
		}

	}

	images := append(imgPublic, imgPrivate...)
	if len(images) > 0 {
		imagesMntPoint := getImageMountPoint(defaultPoolName, "")
		err := os.MkdirAll(imagesMntPoint, 0700)
		if err != nil {
			return err
		}
	}

	for _, img := range images {
		_, err := dbStoragePoolVolumeGetTypeID(d.db, img, storagePoolVolumeTypeImage, poolID)
		if err == nil {
			shared.LogWarnf("Storage volumes database already contains an entry for the container.")
		} else if err == NoSuchObjectError {
			// Insert storage volumes for containers into the database.
			_, err := dbStoragePoolVolumeCreate(d.db, img, storagePoolVolumeTypeImage, poolID, volumeConfig)
			if err != nil {
				shared.LogWarnf("Could not insert a storage volume for image \"%s\".", img)
				return err
			}
		} else {
			shared.LogErrorf("Failed to query database: %s", err)
			return err
		}

		// Unmount the logical volume.
		oldImageMntPoint := shared.VarPath("images", img+".lv")
		if shared.IsMountPoint(oldImageMntPoint) {
			err := tryUnmount(oldImageMntPoint, 0)
			if err != nil {
				return err
			}
		}

		if shared.PathExists(oldImageMntPoint) {
			err := os.Remove(oldImageMntPoint)
			if err != nil {
				return err
			}
		}

		newImageMntPoint := getImageMountPoint(defaultPoolName, img)
		if !shared.PathExists(newImageMntPoint) {
			err := os.MkdirAll(newImageMntPoint, 0700)
			if err != nil {
				return err
			}
		}

		// Rename the logical volume device.
		newImageLvName := fmt.Sprintf("%s_%s", storagePoolVolumeApiEndpointImages, img)
		_, err = tryExec("lvrename", defaultPoolName, img, newImageLvName)
		if err != nil {
			return err
		}
	}

	return nil
}

func upgradeFromStorageTypeZfs(name string, d *Daemon, defaultPoolName string, defaultStorageTypeName string, cRegular []string, cSnapshots []string, imgPublic []string, imgPrivate []string) error {
	poolConfig := map[string]string{}
	oldLoopFilePath := shared.VarPath("zfs.img")
	poolName := defaultPoolName

	// Peek into the storage pool database to see whether any storage pools
	// are already configured. If so, we can assume that a partial upgrade
	// has been performed and can skip the next steps. Otherwise we might
	// run into problems. For example, the "zfs.img" file might have already
	// been moved into ${LXD_DIR}/disks and we might therefore falsely
	// conclude that we're using an existing storage pool.
	poolID := int64(-1)
	pools, err := dbStoragePools(d.db)
	if err == nil { // Already exist valid storage pools.
		if strings.Contains(defaultPoolName, "/") {
			poolName = "default"
		}

		// Check if the storage pool already has a db entry.
		if shared.StringInSlice(poolName, pools) {
			shared.LogWarnf("Database already contains a valid entry for the storage pool: %s.", poolName)
		}

		// Get the pool ID as we need it for storage volume creation.
		// (Use a tmp variable as Go's scoping is freaking me out.)
		tmp, err := dbStoragePoolGetID(d.db, poolName)
		if err != nil {
			shared.LogErrorf("Failed to query database: %s.", err)
			return err
		}
		poolID = tmp
	} else if err == NoSuchObjectError { // Likely a pristine upgrade.
		if shared.PathExists(oldLoopFilePath) {
			// This is a loop file pool.
			poolConfig["source"] = shared.VarPath("disks", defaultPoolName+".img")
			err := os.Rename(oldLoopFilePath, poolConfig["source"])
			if err != nil {
				return err
			}
		} else {
			if strings.Contains(defaultPoolName, "/") {
				poolName = "default"
			}
			// This is a block device pool.
			poolConfig["source"] = defaultPoolName
		}

		if poolName == defaultPoolName {
			output, err := exec.Command("zpool", "get", "size", "-p", "-H", defaultPoolName).CombinedOutput()
			if err == nil {
				lidx := strings.LastIndex(string(output), "\t")
				fidx := strings.LastIndex(string(output)[:lidx-1], "\t")
				poolConfig["size"] = string(output)[fidx+1 : lidx]
			}
		}

		// (Use a tmp variable as Go's scoping is freaking me out.)
		tmp, err := dbStoragePoolCreate(d.db, poolName, defaultStorageTypeName, poolConfig)
		if err != nil {
			shared.LogWarnf("Storage pool already exists in the database. Proceeding...")
		}
		poolID = tmp
	} else { // Shouldn't happen.
		shared.LogErrorf("Failed to query database: %s.", err)
		return err
	}

	// Create storage volumes in the database.
	volumeConfig := map[string]string{}

	if len(cRegular) > 0 {
		containersSubvolumePath := getContainerMountPoint(poolName, "")
		if !shared.PathExists(containersSubvolumePath) {
			err := os.MkdirAll(containersSubvolumePath, 0711)
			if err != nil {
				shared.LogWarnf("Failed to create path: %s.", containersSubvolumePath)
			}
		}
	}

	failedUpgradeEntities := []string{}
	for _, ct := range cRegular {
		_, err := dbStoragePoolVolumeGetTypeID(d.db, ct, storagePoolVolumeTypeContainer, poolID)
		if err == nil {
			shared.LogWarnf("Storage volumes database already contains an entry for the container.")
		} else if err == NoSuchObjectError {
			// Insert storage volumes for containers into the database.
			_, err := dbStoragePoolVolumeCreate(d.db, ct, storagePoolVolumeTypeContainer, poolID, volumeConfig)
			if err != nil {
				shared.LogErrorf("Could not insert a storage volume for container \"%s\".", ct)
				return err
			}
		} else {
			shared.LogErrorf("Failed to query database: %s", err)
			return err
		}

		// Unmount the container zfs doesn't really seem to care if we
		// do this.
		ctDataset := fmt.Sprintf("%s/containers/%s", defaultPoolName, ct)
		oldContainerMntPoint := shared.VarPath("containers", ct)
		if shared.IsMountPoint(oldContainerMntPoint) {
			_, err := tryExec("zfs", "unmount", "-f", ctDataset)
			if err != nil {
				shared.LogWarnf("Failed to unmount ZFS filesystem via zfs unmount. Trying lazy umount (MNT_DETACH)...")
				err := tryUnmount(oldContainerMntPoint, syscall.MNT_DETACH)
				if err != nil {
					failedUpgradeEntities = append(failedUpgradeEntities, fmt.Sprintf("containers/%s: Failed to umount zfs filesystem.", ct))
					continue
				}
			}
		}

		os.Remove(oldContainerMntPoint)

		os.Remove(oldContainerMntPoint + ".zfs")

		// Changing the mountpoint property should have actually created
		// the path but in case it somehow didn't let's do it ourselves.
		doesntMatter := false
		newContainerMntPoint := getContainerMountPoint(poolName, ct)
		err = createContainerMountpoint(newContainerMntPoint, oldContainerMntPoint, doesntMatter)
		if err != nil {
			shared.LogWarnf("Failed to create mountpoint for the container: %s.", newContainerMntPoint)
			failedUpgradeEntities = append(failedUpgradeEntities, fmt.Sprintf("containers/%s: Failed to create container mountpoint: %s", ct, err))
			continue
		}

		// Set new mountpoint for the container's dataset it will be
		// automatically mounted.
		output, err := exec.Command(
			"zfs",
			"set",
			fmt.Sprintf("mountpoint=%s", newContainerMntPoint),
			ctDataset).CombinedOutput()
		if err != nil {
			shared.LogWarnf("Failed to set new ZFS mountpoint: %s.", output)
			failedUpgradeEntities = append(failedUpgradeEntities, fmt.Sprintf("containers/%s: Failed to set new zfs mountpoint: %s", ct, err))
			continue
		}

		// Check if we need to account for snapshots for this container.
		ctSnapshots, err := dbContainerGetSnapshots(d.db, ct)
		if err != nil {
			shared.LogErrorf("Failed to query database")
			return err
		}

		snapshotsPath := shared.VarPath("snapshots", ct)
		for _, cs := range ctSnapshots {
			// Insert storage volumes for snapshots into the
			// database. Note that snapshots have already been moved
			// and symlinked above. So no need to do any work here.
			_, err := dbStoragePoolVolumeGetTypeID(d.db, cs, storagePoolVolumeTypeContainer, poolID)
			if err == nil {
				shared.LogWarnf("Storage volumes database already contains an entry for the container.")
			} else if err == NoSuchObjectError {
				// Insert storage volumes for containers into the database.
				_, err := dbStoragePoolVolumeCreate(d.db, cs, storagePoolVolumeTypeContainer, poolID, volumeConfig)
				if err != nil {
					shared.LogErrorf("Could not insert a storage volume for snapshot \"%s\".", cs)
					return err
				}
			} else {
				shared.LogErrorf("Failed to query database: %s", err)
				return err
			}

			// Create the new mountpoint for snapshots in the new
			// storage api.
			newSnapshotMntPoint := getSnapshotMountPoint(poolName, cs)
			if !shared.PathExists(newSnapshotMntPoint) {
				err = os.MkdirAll(newSnapshotMntPoint, 0711)
				if err != nil {
					shared.LogWarnf("Failed to create mountpoint for snapshot: %s.", newSnapshotMntPoint)
					failedUpgradeEntities = append(failedUpgradeEntities, fmt.Sprintf("snapshots/%s: Failed to create mountpoint for snapshot.", cs))
					continue
				}
			}
		}

		os.RemoveAll(snapshotsPath)

		// Create a symlink for this container's snapshots.
		if len(ctSnapshots) != 0 {
			newSnapshotsMntPoint := getSnapshotMountPoint(poolName, ct)
			if !shared.PathExists(newSnapshotsMntPoint) {
				err := os.Symlink(newSnapshotsMntPoint, snapshotsPath)
				if err != nil {
					shared.LogWarnf("Failed to create symlink for snapshots: %s -> %s.", snapshotsPath, newSnapshotsMntPoint)
				}
			}
		}
	}

	// Insert storage volumes for images into the database. Images don't
	// move. The tarballs remain in their original location.
	images := append(imgPublic, imgPrivate...)
	for _, img := range images {
		_, err := dbStoragePoolVolumeGetTypeID(d.db, img, storagePoolVolumeTypeImage, poolID)
		if err == nil {
			shared.LogWarnf("Storage volumes database already contains an entry for the container.")
		} else if err == NoSuchObjectError {
			// Insert storage volumes for containers into the database.
			_, err := dbStoragePoolVolumeCreate(d.db, img, storagePoolVolumeTypeImage, poolID, volumeConfig)
			if err != nil {
				shared.LogWarnf("Could not insert a storage volume for image \"%s\".", img)
				return err
			}
		} else {
			shared.LogErrorf("Failed to query database: %s", err)
			return err
		}

		imageMntPoint := getImageMountPoint(poolName, img)
		if !shared.PathExists(imageMntPoint) {
			err := os.MkdirAll(imageMntPoint, 0700)
			if err != nil {
				shared.LogWarnf("Failed to create image mountpoint. Proceeding...")
			}
		}

		oldImageMntPoint := shared.VarPath("images", img+".zfs")
		imageDataset := fmt.Sprintf("%s/images/%s", defaultPoolName, img)
		if shared.PathExists(oldImageMntPoint) && shared.IsMountPoint(oldImageMntPoint) {
			_, err := tryExec("zfs", "unmount", "-f", imageDataset)
			if err != nil {
				shared.LogWarnf("Failed to unmount ZFS filesystem via zfs unmount. Trying lazy umount (MNT_DETACH)...")
				err := tryUnmount(oldImageMntPoint, syscall.MNT_DETACH)
				if err != nil {
					shared.LogWarnf("Failed to unmount ZFS filesystem: %s", err)
				}
			}

			os.Remove(oldImageMntPoint)
		}

		// Set new mountpoint for the container's dataset it will be
		// automatically mounted.
		output, err := exec.Command("zfs", "set", "mountpoint=none", imageDataset).CombinedOutput()
		if err != nil {
			shared.LogWarnf("Failed to set new ZFS mountpoint: %s.", output)
		}
	}

	var finalErr error
	if len(failedUpgradeEntities) > 0 {
		finalErr = fmt.Errorf(strings.Join(failedUpgradeEntities, "\n"))
	}

	return finalErr
}

func updatePoolPropertyForAllObjects(d *Daemon, poolName string, allcontainers []string) error {
	// The new storage api enforces that the default storage pool on which
	// containers are created is set in the default profile. If it isn't
	// set, then LXD will refuse to create a container until either an
	// appropriate device including a pool is added to the default profile
	// or the user explicitly passes the pool the container's storage volume
	// is supposed to be created on.
	profiles, err := dbProfiles(d.db)
	if err == nil {
		for _, pName := range profiles {
			pID, p, err := dbProfileGet(d.db, pName)
			if err != nil {
				shared.LogErrorf("Could not query database: %s.", err)
				return err
			}

			// Check for a root disk device entry
			k, _ := containerGetRootDiskDevice(p.Devices)
			if k != "" {
				if p.Devices[k]["pool"] != "" {
					continue
				}
				p.Devices[k]["pool"] = poolName
			} else if k == "" && pName == "default" {
				// The default profile should have a valid root
				// disk device entry.
				rootDev := map[string]string{}
				rootDev["type"] = "disk"
				rootDev["path"] = "/"
				rootDev["pool"] = poolName
				if p.Devices == nil {
					p.Devices = map[string]map[string]string{}
				}

				// Make sure that we do not overwrite a device the user
				// is currently using under the name "root".
				rootDevName := "root"
				for i := 0; i < 100; i++ {
					if p.Devices[rootDevName] == nil {
						break
					}
					rootDevName = fmt.Sprintf("root%d", i)
					continue
				}
				p.Devices["root"] = rootDev
			}

			// This is nasty, but we need to clear the profiles config and
			// devices in order to add the new root device including the
			// newly added storage pool.
			tx, err := dbBegin(d.db)
			if err != nil {
				return err
			}

			err = dbProfileConfigClear(tx, pID)
			if err != nil {
				shared.LogErrorf("Failed to clear old profile configuration for profile %s: %s.", pName, err)
				tx.Rollback()
				continue
			}

			err = dbProfileConfigAdd(tx, pID, p.Config)
			if err != nil {
				shared.LogErrorf("Failed to add new profile configuration: %s: %s.", pName, err)
				tx.Rollback()
				continue
			}

			err = dbDevicesAdd(tx, "profile", pID, p.Devices)
			if err != nil {
				shared.LogErrorf("Failed to add new profile profile root disk device: %s: %s.", pName, err)
				tx.Rollback()
				continue
			}

			err = tx.Commit()
			if err != nil {
				shared.LogErrorf("Failed to commit database transaction: %s: %s.", pName, err)
				tx.Rollback()
				continue
			}
		}
	}

	// When no default profile is detected or some containers do not rely on
	// the default profile for their root disk device, these containers will
	// be given a valid local root disk device."
	for _, ct := range allcontainers {
		c, err := containerLoadByName(d, ct)
		if err != nil {
			continue
		}

		args := containerArgs{
			Architecture: c.Architecture(),
			Config:       c.LocalConfig(),
			Ephemeral:    c.IsEphemeral(),
			CreationDate: c.CreationDate(),
			LastUsedDate: c.LastUsedDate(),
			Name:         c.Name(),
			Profiles:     c.Profiles(),
		}

		if c.IsSnapshot() {
			args.Ctype = cTypeSnapshot
		} else {
			args.Ctype = cTypeRegular
		}

		// Check expanded devices for a valid root entry. If it exists,
		// we skip this container.
		expandedDevices := c.ExpandedDevices()
		k, _ := containerGetRootDiskDevice(expandedDevices)
		if k != "" && expandedDevices[k]["pool"] != "" {
			// On partial upgrade the container might already have a
			// valid root disk device entry.
			if expandedDevices[k]["pool"] == poolName {
				continue
			}
		}

		// Check for a local root disk device entry and set the pool
		// property.
		localDevices := c.LocalDevices()
		k, _ = containerGetRootDiskDevice(localDevices)
		if k != "" {
			if localDevices[k]["pool"] != "" {
				continue
			}
			localDevices[k]["pool"] = poolName
			args.Devices = localDevices
		} else {
			rootDev := map[string]string{}
			rootDev["type"] = "disk"
			rootDev["path"] = "/"
			rootDev["pool"] = poolName

			// Make sure that we do not overwrite a device the user
			// is currently using under the name "root".
			rootDevName := "root"
			for i := 0; i < 100; i++ {
				if localDevices[rootDevName] == nil {
					break
				}
				rootDevName = fmt.Sprintf("root%d", i)
				continue
			}
			localDevices[rootDevName] = rootDev
		}

		err = c.Update(args, false)
		if err != nil {
			continue
		}
	}

	return nil
}

func patchStorageApiV1(name string, d *Daemon) error {
	pools, err := dbStoragePools(d.db)
	if err != nil && err == NoSuchObjectError {
		// No pool was configured in the previous update. So we're on a
		// pristine LXD instance.
		return nil
	} else if err != nil {
		// Database is screwed.
		shared.LogErrorf("Failed to query database: %s", err)
		return err
	}

	if len(pools) != 1 {
		shared.LogWarnf("More than one storage pool found. Not rerunning upgrade.")
		return nil
	}

	cRegular, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return err
	}

	// Get list of existing snapshots.
	cSnapshots, err := dbContainersList(d.db, cTypeSnapshot)
	if err != nil {
		return err
	}

	allcontainers := append(cRegular, cSnapshots...)
	err = updatePoolPropertyForAllObjects(d, pools[0], allcontainers)
	if err != nil {
		return err
	}

	return nil
}

func patchStorageApiDirCleanup(name string, d *Daemon) error {
	_, err := dbExec(d.db, "DELETE FROM storage_volumes WHERE type=? AND name NOT IN (SELECT fingerprint FROM images);", storagePoolVolumeTypeImage)
	if err != nil {
		return err
	}

	return nil
}
