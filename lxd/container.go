package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"
)

// Helper functions
func containerPath(name string, isSnapshot bool) string {
	if isSnapshot {
		return shared.VarPath("snapshots", name)
	}

	return shared.VarPath("containers", name)
}

func validContainerName(name string) error {
	if strings.Contains(name, shared.SnapshotDelimiter) {
		return fmt.Errorf(
			"The character '%s' is reserved for snapshots.",
			shared.SnapshotDelimiter)
	}

	if !shared.ValidHostname(name) {
		return fmt.Errorf("Container name isn't a valid hostname.")
	}

	return nil
}

// The container arguments
type containerArgs struct {
	// Don't set manually
	Id int

	Architecture int
	BaseImage    string
	Config       map[string]string
	Ctype        containerType
	Devices      shared.Devices
	Ephemeral    bool
	Name         string
	Profiles     []string
}

// The container interface
type container interface {
	// Container actions
	Freeze() error
	Shutdown(timeout time.Duration) error
	Start() error
	Stop() error
	Unfreeze() error

	// Snapshots & migration
	Restore(sourceContainer container) error
	Checkpoint(opts lxc.CheckpointOptions) error
	StartFromMigration(imagesDir string) error
	Snapshots() ([]container, error)

	// Config handling
	Rename(newName string) error
	Update(newConfig containerArgs, userRequested bool) error

	Delete() error
	Export(w io.Writer) error

	// Live configuration
	CGroupSet(key string, value string) error
	ConfigKeySet(key string, value string) error

	// Status
	RenderState() (*shared.ContainerState, error)
	IsPrivileged() bool
	IsRunning() bool
	IsFrozen() bool
	IsEphemeral() bool
	IsSnapshot() bool
	IsNesting() bool

	// Hooks
	OnStart() error
	OnStop(target string) error

	// Properties
	Id() int
	Name() string
	Architecture() int
	ExpandedConfig() map[string]string
	ExpandedDevices() shared.Devices
	LocalConfig() map[string]string
	LocalDevices() shared.Devices
	Profiles() []string
	InitPID() int
	State() string

	// Paths
	Path() string
	RootfsPath() string
	TemplatesPath() string
	StatePath() string
	LogFilePath() string
	LogPath() string

	// FIXME: Those should be internal functions
	LXContainerGet() *lxc.Container
	StorageStart() error
	StorageStop() error
	Storage() storage
	IdmapSet() *shared.IdmapSet
	LastIdmapSet() (*shared.IdmapSet, error)
	TemplateApply(trigger string) error
	Daemon() *Daemon
}

// Loader functions
func containerCreateAsEmpty(d *Daemon, args containerArgs) (container, error) {
	// Create the container
	c, err := containerCreateInternal(d, args)
	if err != nil {
		return nil, err
	}

	// Now create the empty storage
	if err := c.Storage().ContainerCreate(c); err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerCreateEmptySnapshot(d *Daemon, args containerArgs) (container, error) {
	// Create the container
	c, err := containerCreateInternal(d, args)
	if err != nil {
		return nil, err
	}

	// Now create the empty snapshot
	if err := c.Storage().ContainerSnapshotCreateEmpty(c); err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerCreateFromImage(d *Daemon, args containerArgs, hash string) (container, error) {
	// Create the container
	c, err := containerCreateInternal(d, args)
	if err != nil {
		return nil, err
	}

	if err := dbImageLastAccessUpdate(d.db, hash); err != nil {
		return nil, fmt.Errorf("Error updating image last use date: %s", err)
	}

	// Now create the storage from an image
	if err := c.Storage().ContainerCreateFromImage(c, hash); err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerCreateAsCopy(d *Daemon, args containerArgs, sourceContainer container) (container, error) {
	// Create the container
	c, err := containerCreateInternal(d, args)
	if err != nil {
		return nil, err
	}

	// Now clone the storage
	if err := c.Storage().ContainerCopy(c, sourceContainer); err != nil {
		c.Delete()
		return nil, err
	}

	return c, nil
}

func containerCreateAsSnapshot(d *Daemon, args containerArgs, sourceContainer container, stateful bool) (container, error) {
	// Create the container
	c, err := containerCreateInternal(d, args)
	if err != nil {
		return nil, err
	}

	// Clone the snapshot
	if err := sourceContainer.Storage().ContainerSnapshotCreate(c, sourceContainer); err != nil {
		c.Delete()
		return nil, err
	}

	// Deal with state
	if stateful {
		stateDir := c.StatePath()
		err = os.MkdirAll(stateDir, 0700)
		if err != nil {
			c.Delete()
			return nil, err
		}

		// TODO - shouldn't we freeze for the duration of rootfs snapshot below?
		if !sourceContainer.IsRunning() {
			c.Delete()
			return nil, fmt.Errorf("Container not running")
		}

		opts := lxc.CheckpointOptions{Directory: stateDir, Stop: true, Verbose: true}
		err = sourceContainer.Checkpoint(opts)
		err2 := CollectCRIULogFile(sourceContainer, stateDir, "snapshot", "dump")
		if err2 != nil {
			shared.Log("warn", "failed to collect criu log file", shared.Ctx{"error": err2})
		}

		if err != nil {
			c.Delete()
			return nil, err
		}
	}

	return c, nil
}

func containerCreateInternal(d *Daemon, args containerArgs) (container, error) {
	// Set default values
	if args.Profiles == nil {
		args.Profiles = []string{"default"}
	}

	if args.Config == nil {
		args.Config = map[string]string{}
	}

	if args.BaseImage != "" {
		args.Config["volatile.base_image"] = args.BaseImage
	}

	if args.Devices == nil {
		args.Devices = shared.Devices{}
	}

	if args.Architecture == 0 {
		args.Architecture = d.architectures[0]
	}

	// Sanity checks
	if args.Ctype == cTypeRegular {
		if err := validContainerName(args.Name); err != nil {
			return nil, err
		}
	}

	_, err := shared.ArchitectureName(args.Architecture)
	if err != nil {
		return nil, err
	}

	profiles, err := dbProfiles(d.db)
	if err != nil {
		return nil, err
	}

	for _, profile := range args.Profiles {
		if !shared.StringInSlice(profile, profiles) {
			return nil, fmt.Errorf("Requested profile '%s' doesn't exist", profile)
		}
	}

	path := containerPath(args.Name, args.Ctype == cTypeSnapshot)
	if shared.PathExists(path) {
		return nil, fmt.Errorf("The container already exists")
	}

	// Create the container entry
	id, err := dbContainerCreate(d.db, args)
	if err != nil {
		return nil, err
	}
	args.Id = id

	return containerLXCCreate(d, args)
}

func containerLoadById(d *Daemon, id int) (container, error) {
	// Get the DB record
	name, err := dbContainerName(d.db, id)
	if err != nil {
		return nil, err
	}

	return containerLoadByName(d, name)
}

func containerLoadByName(d *Daemon, name string) (container, error) {
	// Get the DB record
	args, err := dbContainerGet(d.db, name)
	if err != nil {
		return nil, err
	}

	return containerLXCLoad(d, args)
}
