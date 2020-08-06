package instance

import (
	"io"
	"os"
	"time"

	liblxc "gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/lxd/backup"
	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/idmap"
)

// HookStart hook used when instance has started.
const HookStart = "onstart"

// HookStopNS hook used when instance has stopped but before namespaces have been destroyed.
const HookStopNS = "onstopns"

// HookStop hook used when instance has stopped.
const HookStop = "onstop"

// Possible values for the protocol argument of the Instance.Console() method.
const (
	ConsoleTypeConsole = "console"
	ConsoleTypeVGA     = "vga"
)

// ConfigReader is used to read instance config.
type ConfigReader interface {
	Type() instancetype.Type
	ExpandedConfig() map[string]string
	ExpandedDevices() deviceConfig.Devices
	LocalConfig() map[string]string
	LocalDevices() deviceConfig.Devices
}

// Instance interface.
type Instance interface {
	ConfigReader

	// Instance actions.
	Freeze() error
	Shutdown(timeout time.Duration) error
	Start(stateful bool) error
	Stop(stateful bool) error
	Unfreeze() error
	RegisterDevices()
	SaveConfigFile() error

	IsPrivileged() bool

	// Snapshots & migration & backups.
	Restore(source Instance, stateful bool) error
	Snapshots() ([]Instance, error)
	Backups() ([]backup.Backup, error)
	UpdateBackupFile() error

	// Config handling.
	Rename(newName string) error
	Update(newConfig db.InstanceArgs, userRequested bool) error

	Delete() error
	Export(w io.Writer, properties map[string]string) (api.ImageMetadata, error)

	// Live configuration.
	CGroupSet(key string, value string) error
	VolatileSet(changes map[string]string) error

	// File handling.
	FileExists(path string) error
	FilePull(srcpath string, dstpath string) (int64, int64, os.FileMode, string, []string, error)
	FilePush(fileType string, srcpath string, dstpath string, uid int64, gid int64, mode int, write string) error
	FileRemove(path string) error

	// Console - Allocate and run a console tty or a spice Unix socket.
	Console(protocol string) (*os.File, chan error, error)
	Exec(req api.InstanceExecPost, stdin *os.File, stdout *os.File, stderr *os.File) (Cmd, error)

	// Status
	Render(options ...func(response interface{}) error) (interface{}, interface{}, error)
	RenderFull() (*api.InstanceFull, interface{}, error)
	RenderState() (*api.InstanceState, error)
	IsRunning() bool
	IsFrozen() bool
	IsEphemeral() bool
	IsSnapshot() bool
	IsStateful() bool

	// Hooks.
	DeviceEventHandler(*deviceConfig.RunConfig) error
	OnHook(hookName string, args map[string]string) error

	// Properties.
	ID() int
	Location() string
	Project() string
	Name() string
	Description() string
	Architecture() int
	CreationDate() time.Time
	LastUsedDate() time.Time

	Profiles() []string
	InitPID() int
	State() string
	ExpiryDate() time.Time
	FillNetworkDevice(name string, m deviceConfig.Device) (deviceConfig.Device, error)

	// Paths.
	Path() string
	RootfsPath() string
	TemplatesPath() string
	StatePath() string
	LogFilePath() string
	ConsoleBufferLogPath() string
	LogPath() string
	DevicesPath() string

	// Storage.
	StoragePool() (string, error)

	// Migration.
	Migrate(args *CriuMigrationArgs) error

	// Progress reporting.
	SetOperation(op *operations.Operation)

	// FIXME: Those should be internal functions.
	// Needed for migration for now.
	StorageStart() (bool, error)
	StorageStop() (bool, error)
	DeferTemplateApply(trigger string) error
}

// Container interface is for container specific functions.
type Container interface {
	Instance

	CurrentIdmap() (*idmap.IdmapSet, error)
	DiskIdmap() (*idmap.IdmapSet, error)
	NextIdmap() (*idmap.IdmapSet, error)
	ConsoleLog(opts liblxc.ConsoleLogOptions) (string, error)
	InsertSeccompUnixDevice(prefix string, m deviceConfig.Device, pid int) error
	DevptsFd() (*os.File, error)
	SeccompNotifyFd() (*os.File, error)
}

// CriuMigrationArgs arguments for CRIU migration.
type CriuMigrationArgs struct {
	Cmd          uint
	StateDir     string
	Function     string
	Stop         bool
	ActionScript bool
	DumpDir      string
	PreDumpDir   string
	Features     liblxc.CriuFeatures
}
