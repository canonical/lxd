package main

import (
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/device"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared/api"
)

// The Instance interface
type Instance interface {
	// Instance actions
	Freeze() error
	Shutdown(timeout time.Duration) error
	Start(stateful bool) error
	Stop(stateful bool) error
	Unfreeze() error

	IsPrivileged() bool

	// Snapshots & migration & backups
	Restore(source Instance, stateful bool) error
	Snapshots() ([]Instance, error)
	Backups() ([]backup, error)

	// Config handling
	Rename(newName string) error

	// TODO rename db.InstanceArgs to db.InstanceArgs.
	Update(newConfig db.InstanceArgs, userRequested bool) error

	Delete() error
	Export(w io.Writer, properties map[string]string) error

	// Live configuration
	CGroupGet(key string) (string, error)
	CGroupSet(key string, value string) error
	VolatileSet(changes map[string]string) error

	// File handling
	FileExists(path string) error
	FilePull(srcpath string, dstpath string) (int64, int64, os.FileMode, string, []string, error)
	FilePush(type_ string, srcpath string, dstpath string, uid int64, gid int64, mode int, write string) error
	FileRemove(path string) error

	// Console - Allocate and run a console tty.
	//
	// terminal  - Bidirectional file descriptor.
	//
	// This function will not return until the console has been exited by
	// the user.
	Console(terminal *os.File) *exec.Cmd
	Exec(command []string, env map[string]string, stdin *os.File, stdout *os.File, stderr *os.File, wait bool, cwd string, uid uint32, gid uint32) (*exec.Cmd, int, int, error)

	// Status
	Render() (interface{}, interface{}, error)
	RenderFull() (*api.InstanceFull, interface{}, error)
	RenderState() (*api.InstanceState, error)
	IsRunning() bool
	IsFrozen() bool
	IsEphemeral() bool
	IsSnapshot() bool
	IsStateful() bool

	// Hooks
	DeviceEventHandler(*device.RunConfig) error

	// Properties
	ID() int
	Location() string
	Project() string
	Name() string
	Type() instancetype.Type
	Description() string
	Architecture() int
	CreationDate() time.Time
	LastUsedDate() time.Time
	ExpandedConfig() map[string]string
	ExpandedDevices() deviceConfig.Devices
	LocalConfig() map[string]string
	LocalDevices() deviceConfig.Devices
	Profiles() []string
	InitPID() int
	State() string
	ExpiryDate() time.Time

	// Paths
	Path() string
	RootfsPath() string
	TemplatesPath() string
	StatePath() string
	LogFilePath() string
	ConsoleBufferLogPath() string
	LogPath() string
	DevicesPath() string

	// Storage
	StoragePool() (string, error)

	// Progress reporting

	SetOperation(op *operations.Operation)

	// FIXME: Those should be internal functions
	// Needed for migration for now.
	StorageStart() (bool, error)
	StorageStop() (bool, error)
	Storage() storage
	TemplateApply(trigger string) error
	DaemonState() *state.State
}
