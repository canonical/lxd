package instance

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"os"
	"time"

	liblxc "github.com/lxc/go-lxc"
	"github.com/pkg/sftp"
	"google.golang.org/protobuf/proto"

	"github.com/canonical/lxd/lxd/backup"
	"github.com/canonical/lxd/lxd/cgroup"
	"github.com/canonical/lxd/lxd/db"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/idmap"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/instance/operationlock"
	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/ioprogress"
)

// HookStart hook used when instance has started.
const HookStart = "onstart"

// HookStartHost hook used when instance is fully ready to be started.
const HookStartHost = "onstarthost"

// HookStopNS hook used when instance has stopped but before namespaces have been destroyed.
const HookStopNS = "onstopns"

// HookStop hook used when instance has stopped.
const HookStop = "onstop"

// Possible values for the protocol argument of the Instance.Console() method.
const (
	ConsoleTypeConsole = "console"
	ConsoleTypeVGA     = "vga"
)

// TemplateTrigger trigger name.
type TemplateTrigger string

// TemplateTriggerCreate for when an instance is created.
const TemplateTriggerCreate TemplateTrigger = "create"

// TemplateTriggerCopy for when an instance is copied.
const TemplateTriggerCopy TemplateTrigger = "copy"

// TemplateTriggerRename for when an instance is renamed.
const TemplateTriggerRename TemplateTrigger = "rename"

// PowerStateRunning represents the power state stored when an instance is running.
const PowerStateRunning = "RUNNING"

// PowerStateStopped represents the power state stored when an instance is stopped.
const PowerStateStopped = "STOPPED"

// ConfigReader is used to read instance config.
type ConfigReader interface {
	Project() api.Project
	Type() instancetype.Type
	Architecture() int
	ID() int

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
	Restart(timeout time.Duration) error
	Rebuild(img *api.Image, op *operations.Operation) error
	Unfreeze() error
	RegisterDevices()

	Info() Info
	IsPrivileged() bool

	// Snapshots & migration & backups.
	Restore(source Instance, stateful bool) error
	Snapshot(name string, expiry *time.Time, stateful bool) error
	Snapshots() ([]Instance, error)
	Backups() ([]backup.InstanceBackup, error)
	UpdateBackupFile() error

	// Config handling.
	Rename(newName string, applyTemplateTrigger bool) error
	Update(newConfig db.InstanceArgs, userRequested bool) error

	Delete(force bool) error
	Export(w io.Writer, properties map[string]string, expiration time.Time, tracker *ioprogress.ProgressTracker) (api.ImageMetadata, error)

	// Live configuration.
	CGroup() (*cgroup.CGroup, error)
	VolatileSet(changes map[string]string) error
	SetAffinity(set []string) error

	// File handling.
	FileSFTPConn() (net.Conn, error)
	FileSFTP() (*sftp.Client, error)

	// Console - Allocate and run a console tty or a spice Unix socket.
	Console(protocol string) (*os.File, chan error, error)
	Exec(req api.InstanceExecPost, stdin *os.File, stdout *os.File, stderr *os.File) (Cmd, error)

	// Status
	Render(options ...func(response any) error) (any, any, error)
	RenderFull(hostInterfaces []net.Interface) (*api.InstanceFull, any, error)
	RenderState(hostInterfaces []net.Interface) (*api.InstanceState, error)
	IsRunning() bool
	IsFrozen() bool
	IsEphemeral() bool
	IsSnapshot() bool
	IsStateful() bool
	LockExclusive() (*operationlock.InstanceOperation, error)

	// Hooks.
	DeviceEventHandler(*deviceConfig.RunConfig) error
	OnHook(hookName string, args map[string]string) error

	// Properties.
	Location() string
	Name() string
	CloudInitID() string
	Description() string
	CreationDate() time.Time
	LastUsedDate() time.Time

	Profiles() []api.Profile
	InitPID() int
	State() string
	ExpiryDate() time.Time
	FillNetworkDevice(name string, m deviceConfig.Device) (deviceConfig.Device, error)

	// Paths.
	Path() string
	ExecOutputPath() string
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
	CanMigrate() (bool, bool)
	MigrateSend(args MigrateSendArgs) error
	MigrateReceive(args MigrateReceiveArgs) error
	PostMigrateSend() error

	// Conversion.
	ConversionReceive(args ConversionReceiveArgs) error

	// Progress reporting.
	SetOperation(op *operations.Operation)
	Operation() *operations.Operation

	DeferTemplateApply(trigger TemplateTrigger) error

	Metrics(hostInterfaces []net.Interface) (*metrics.MetricSet, error)
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
	IdmappedStorage(path string, fstype string) idmap.IdmapStorageType
}

// VM interface is for VM specific functions.
type VM interface {
	Instance

	AgentCertificate() *x509.Certificate

	FirmwarePath() string

	// UEFI vars handling.
	UEFIVars() (*api.InstanceUEFIVars, error)
	UEFIVarsUpdate(newUEFIVarsSet api.InstanceUEFIVars) error
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
	Op           *operationlock.InstanceOperation
}

// Info represents information about an instance driver.
type Info struct {
	Name     string            // Name of an instance driver, e.g. "lxc"
	Version  string            // Version number of a loaded instance driver
	Error    error             // Whether there is an operational impediment.
	Type     instancetype.Type // Instance type that the driver provides support for.
	Features map[string]any    // Map of supported features.
}

// MigrateArgs represent arguments for instance migration send and receive.
type MigrateArgs struct {
	ControlSend           func(m proto.Message) error
	ControlReceive        func(m proto.Message) error
	StateConn             func(ctx context.Context) (io.ReadWriteCloser, error)
	FilesystemConn        func(ctx context.Context) (io.ReadWriteCloser, error)
	Snapshots             bool
	Live                  bool
	Disconnect            func()
	ClusterMoveSourceName string // Will be empty if not a cluster move, othwise indicates the source instance.
}

// MigrateSendArgs represent arguments for instance migration send.
type MigrateSendArgs struct {
	MigrateArgs

	AllowInconsistent bool
}

// MigrateReceiveArgs represent arguments for instance migration receive.
type MigrateReceiveArgs struct {
	MigrateArgs

	InstanceOperation *operationlock.InstanceOperation
	Refresh           bool
}

// ConversionArgs represent arguments for instance conversion send and receive.
type ConversionArgs struct {
	FilesystemConn func(ctx context.Context) (io.ReadWriteCloser, error)
	Disconnect     func()
}

// ConversionReceiveArgs represent arguments for instance conversion receive.
type ConversionReceiveArgs struct {
	ConversionArgs
	SourceDiskSize    int64 // Size of the disk in bytes.
	ConversionOptions []string
}
