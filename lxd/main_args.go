package main

// Args contains all supported LXD command line flags.
type Args struct {
	Auto                 bool   `flag:"auto"`
	Preseed              bool   `flag:"preseed"`
	CPUProfile           string `flag:"cpuprofile"`
	Debug                bool   `flag:"debug"`
	Group                string `flag:"group"`
	Help                 bool   `flag:"help"`
	Logfile              string `flag:"logfile"`
	MemProfile           string `flag:"memprofile"`
	NetworkAddress       string `flag:"network-address"`
	NetworkPort          int64  `flag:"network-port"`
	PrintGoroutinesEvery int    `flag:"print-goroutines-every"`
	StorageBackend       string `flag:"storage-backend"`
	StorageCreateDevice  string `flag:"storage-create-device"`
	StorageCreateLoop    int64  `flag:"storage-create-loop"`
	StorageDataset       string `flag:"storage-pool"`
	Syslog               bool   `flag:"syslog"`
	Timeout              int    `flag:"timeout"`
	TrustPassword        string `flag:"trust-password"`
	Verbose              bool   `flag:"verbose"`
	Version              bool   `flag:"version"`
	Force                bool   `flag:"force"`

	// The LXD subcommand, if any (e.g. "init" for "lxd init")
	Subcommand string

	// The subcommand parameters (e.g. []string{"foo"} for "lxd import foo").
	Params []string

	// Any extra arguments following the "--" separator.
	Extra []string
}

const usage = `Usage: lxd [command] [options]

Commands:
    activateifneeded
        Check if LXD should be started (at boot) and if so, spawns it through socket activation
    daemon [--group=lxd] (default command)
        Start the main LXD daemon
    init [--auto] [--network-address=IP] [--network-port=8443] [--storage-backend=dir]
         [--storage-create-device=DEVICE] [--storage-create-loop=SIZE] [--storage-pool=POOL]
         [--trust-password=] [--preseed]
        Setup storage and networking
    ready
        Tells LXD that any setup-mode configuration has been done and that it can start containers.
    shutdown [--timeout=60]
        Perform a clean shutdown of LXD and all running containers
    waitready [--timeout=15]
        Wait until LXD is ready to handle requests
    import <container name> [--force]
        Import a pre-existing container from storage


Common options:
    --debug
        Enable debug mode
    --help
        Print this help message
    --logfile FILE
        Logfile to log to (e.g., /var/log/lxd/lxd.log)
    --syslog
        Enable syslog logging
    --verbose
        Enable verbose mode
    --version
        Print LXD's version number and exit

Daemon options:
    --group GROUP
        Group which owns the shared socket (ignored with socket-based activation)

Daemon debug options:
    --cpuprofile FILE
        Enable cpu profiling into the specified file
    --memprofile FILE
        Enable memory profiling into the specified file
    --print-goroutines-every SECONDS
        For debugging, print a complete stack trace every n seconds

Init options:
    --auto
        Automatic (non-interactive) mode
    --preseed
        Pre-seed mode, expects YAML config from stdin

Init options for non-interactive mode (--auto):
    --network-address ADDRESS
        Address to bind LXD to (default: none)
    --network-port PORT
        Port to bind LXD to (default: 8443)
    --storage-backend NAME
        Storage backend to use (btrfs, dir, lvm or zfs, default: dir)
    --storage-create-device DEVICE
        Setup device based storage using DEVICE
    --storage-create-loop SIZE
        Setup loop based storage with SIZE in GB
    --storage-pool NAME
        Storage pool to use or create
    --trust-password PASSWORD
        Password required to add new clients

Shutdown options:
    --timeout SECONDS
        How long to wait before failing

Waitready options:
    --timeout SECONDS
        How long to wait before failing


Internal commands (don't call these directly):
    forkconsole
        Attach to the console of a container
    forkexec
        Execute a command in a container
    forkgetnet
        Get container network information
    forkgetfile
        Grab a file from a running container
    forkmigrate
        Restore a container after migration
    forkputfile
        Push a file to a running container
    forkstart
        Start a container
    callhook
        Call a container hook
    migratedumpsuccess
        Indicate that a migration dump was successful
    netcat
        Mirror a unix socket to stdin/stdout
`
