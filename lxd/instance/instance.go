package instance

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	yaml "gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

// StorageVolumeFillDefault links to storageVolumeFillDefault in main.
var StorageVolumeFillDefault func(name string, config map[string]string, parentPool *api.StoragePool) error

// StoragePoolVolumeContainerCreateInit links to storagePoolVolumeContainerCreateInit in main.
var StoragePoolVolumeContainerCreateInit func(s *state.State, project string, poolName string, containerName string) (Storage, error)

// StoragePoolVolumeContainerLoadInit links to storagePoolVolumeContainerLoadInit in main.
var StoragePoolVolumeContainerLoadInit func(s *state.State, project, containerName string) (Storage, error)

// NetworkUpdateStatic links to networkUpdateStatic in main.
var NetworkUpdateStatic func(s *state.State, networkName string) error

// DevLXDEventSend links to devLXDEventSend in main.
var DevLXDEventSend func(c Instance, eventType string, eventMessage interface{}) error

// InstanceLoadAll Legacy interface.
func InstanceLoadAll(s *state.State) ([]Instance, error) {
	return instanceLoadByProject(s, "default")
}

func instanceLoadByProject(s *state.State, project string) ([]Instance, error) {
	// Get all the containers
	var cts []db.Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		filter := db.InstanceFilter{
			Project: project,
			Type:    instancetype.Container,
		}
		var err error
		cts, err = tx.InstanceList(filter)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return instanceLoadAllInternal(cts, s)
}

func instanceLoadAllInternal(dbInstances []db.Instance, s *state.State) ([]Instance, error) {
	// Figure out what profiles are in use
	profiles := map[string]map[string]api.Profile{}
	for _, instArgs := range dbInstances {
		projectProfiles, ok := profiles[instArgs.Project]
		if !ok {
			projectProfiles = map[string]api.Profile{}
			profiles[instArgs.Project] = projectProfiles
		}
		for _, profile := range instArgs.Profiles {
			_, ok := projectProfiles[profile]
			if !ok {
				projectProfiles[profile] = api.Profile{}
			}
		}
	}

	// Get the profile data
	for project, projectProfiles := range profiles {
		for name := range projectProfiles {
			_, profile, err := s.Cluster.ProfileGet(project, name)
			if err != nil {
				return nil, err
			}

			projectProfiles[name] = *profile
		}
	}

	// Load the instances structs
	instances := []Instance{}
	for _, dbInstance := range dbInstances {
		// Figure out the instances's profiles
		cProfiles := []api.Profile{}
		for _, name := range dbInstance.Profiles {
			cProfiles = append(cProfiles, profiles[dbInstance.Project][name])
		}

		if dbInstance.Type == instancetype.Container {
			args := db.ContainerToArgs(&dbInstance)
			ct, err := ContainerLXCLoad(s, args, cProfiles)
			if err != nil {
				return nil, err
			}
			instances = append(instances, ct)
		} else {
			// TODO add virtual machine load here.
			continue
		}

	}

	return instances, nil
}

// InstanceLoadByProjectAndName loads instances by project and name.
func InstanceLoadByProjectAndName(s *state.State, project, name string) (Instance, error) {
	// Get the DB record
	var container *db.Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error

		if strings.Contains(name, shared.SnapshotDelimiter) {
			parts := strings.SplitN(name, shared.SnapshotDelimiter, 2)
			instanceName := parts[0]
			snapshotName := parts[1]

			instance, err := tx.InstanceGet(project, instanceName)
			if err != nil {
				return errors.Wrapf(err, "Failed to fetch instance %q in project %q", name, project)
			}

			snapshot, err := tx.InstanceSnapshotGet(project, instanceName, snapshotName)
			if err != nil {
				return errors.Wrapf(err, "Failed to fetch snapshot %q of instance %q in project %q", snapshotName, instanceName, project)
			}

			c := db.InstanceSnapshotToInstance(instance, snapshot)
			container = &c
		} else {
			container, err = tx.InstanceGet(project, name)
			if err != nil {
				return errors.Wrapf(err, "Failed to fetch container %q in project %q", name, project)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	args := db.ContainerToArgs(container)

	c, err := ContainerLXCLoad(s, args, nil)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to load container")
	}

	return c, nil
}

// InstanceLoadById loads instance by ID.
func InstanceLoadById(s *state.State, id int) (Instance, error) {
	// Get the DB record
	project, name, err := s.Cluster.ContainerProjectAndName(id)
	if err != nil {
		return nil, err
	}

	return InstanceLoadByProjectAndName(s, project, name)
}

// InstanceDeleteSnapshots deletes instance snapshots.
func InstanceDeleteSnapshots(s *state.State, project, name string) error {
	results, err := s.Cluster.ContainerGetSnapshots(project, name)
	if err != nil {
		return err
	}

	for _, sname := range results {
		sc, err := InstanceLoadByProjectAndName(s, project, sname)
		if err != nil {
			logger.Error(
				"InstanceDeleteSnapshots: Failed to load the snapshot container",
				log.Ctx{"instance": name, "snapshot": sname, "err": err})

			continue
		}

		if err := sc.Delete(); err != nil {
			logger.Error(
				"InstanceDeleteSnapshots: Failed to delete a snapshot container",
				log.Ctx{"instance": name, "snapshot": sname, "err": err})
		}
	}

	return nil
}

// InstanceLoadNodeAll loads all instances of this node.
func InstanceLoadNodeAll(s *state.State) ([]Instance, error) {
	// Get all the container arguments
	var cts []db.Instance
	err := s.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		cts, err = tx.ContainerNodeList()
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return instanceLoadAllInternal(cts, s)
}

// BackupFile represents a backup config file.
type BackupFile struct {
	Container *api.Instance           `yaml:"container"`
	Snapshots []*api.InstanceSnapshot `yaml:"snapshots"`
	Pool      *api.StoragePool        `yaml:"pool"`
	Volume    *api.StorageVolume      `yaml:"volume"`
}

// WriteBackupFile writes backup config file.
func WriteBackupFile(c Instance) error {
	// We only write backup files out for actual containers
	if c.IsSnapshot() {
		return nil
	}

	// Immediately return if the container directory doesn't exist yet
	if !shared.PathExists(c.Path()) {
		return os.ErrNotExist
	}

	// Generate the YAML
	ci, _, err := c.Render()
	if err != nil {
		return errors.Wrap(err, "Failed to render container metadata")
	}

	snapshots, err := c.Snapshots()
	if err != nil {
		return errors.Wrap(err, "Failed to get snapshots")
	}

	var sis []*api.InstanceSnapshot

	for _, s := range snapshots {
		si, _, err := s.Render()
		if err != nil {
			return err
		}

		sis = append(sis, si.(*api.InstanceSnapshot))
	}

	poolName, err := c.StoragePool()
	if err != nil {
		return err
	}

	s := c.DaemonState()
	poolID, pool, err := s.Cluster.StoragePoolGet(poolName)
	if err != nil {
		return err
	}

	_, volume, err := s.Cluster.StoragePoolNodeVolumeGetTypeByProject(c.Project(), c.Name(), db.StoragePoolVolumeTypeContainer, poolID)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&BackupFile{
		Container: ci.(*api.Instance),
		Snapshots: sis,
		Pool:      pool,
		Volume:    volume,
	})
	if err != nil {
		return err
	}

	// Ensure the container is currently mounted
	if !shared.PathExists(c.RootfsPath()) {
		logger.Debug("Unable to update backup.yaml at this time", log.Ctx{"name": c.Name(), "project": c.Project()})
		return nil
	}

	// Write the YAML
	f, err := os.Create(filepath.Join(c.Path(), "backup.yaml"))
	if err != nil {
		return err
	}
	defer f.Close()

	err = f.Chmod(0400)
	if err != nil {
		return err
	}

	err = shared.WriteAll(f, data)
	if err != nil {
		return err
	}

	return nil
}
