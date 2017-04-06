package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
	"github.com/lxc/lxd/shared/version"
)

// Global arguments
var argAuto = gnuflag.Bool("auto", false, "")
var argCPUProfile = gnuflag.String("cpuprofile", "", "")
var argDebug = gnuflag.Bool("debug", false, "")
var argGroup = gnuflag.String("group", "", "")
var argHelp = gnuflag.Bool("help", false, "")
var argLogfile = gnuflag.String("logfile", "", "")
var argMemProfile = gnuflag.String("memprofile", "", "")
var argNetworkAddress = gnuflag.String("network-address", "", "")
var argNetworkPort = gnuflag.Int64("network-port", -1, "")
var argPrintGoroutinesEvery = gnuflag.Int("print-goroutines-every", -1, "")
var argStorageBackend = gnuflag.String("storage-backend", "", "")
var argStorageCreateDevice = gnuflag.String("storage-create-device", "", "")
var argStorageCreateLoop = gnuflag.Int64("storage-create-loop", -1, "")
var argStoragePool = gnuflag.String("storage-pool", "", "")
var argSyslog = gnuflag.Bool("syslog", false, "")
var argTimeout = gnuflag.Int("timeout", -1, "")
var argTrustPassword = gnuflag.String("trust-password", "", "")
var argVerbose = gnuflag.Bool("verbose", false, "")
var argVersion = gnuflag.Bool("version", false, "")

// Global variables
var debug bool
var verbose bool
var execPath string

func init() {
	rand.Seed(time.Now().UTC().UnixNano())
	absPath, err := os.Readlink("/proc/self/exe")
	if err != nil {
		absPath = "bad-exec-path"
	}
	execPath = absPath
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Our massive custom usage
	gnuflag.Usage = func() {
		fmt.Printf("Usage: lxd [command] [options]\n")

		fmt.Printf("\nCommands:\n")
		fmt.Printf("    activateifneeded\n")
		fmt.Printf("        Check if LXD should be started (at boot) and if so, spawns it through socket activation\n")
		fmt.Printf("    daemon [--group=lxd] (default command)\n")
		fmt.Printf("        Start the main LXD daemon\n")
		fmt.Printf("    init [--auto] [--network-address=IP] [--network-port=8443] [--storage-backend=dir]\n")
		fmt.Printf("         [--storage-create-device=DEVICE] [--storage-create-loop=SIZE] [--storage-pool=POOL]\n")
		fmt.Printf("         [--trust-password=]\n")
		fmt.Printf("        Setup storage and networking\n")
		fmt.Printf("    ready\n")
		fmt.Printf("        Tells LXD that any setup-mode configuration has been done and that it can start containers.\n")
		fmt.Printf("    shutdown [--timeout=60]\n")
		fmt.Printf("        Perform a clean shutdown of LXD and all running containers\n")
		fmt.Printf("    waitready [--timeout=15]\n")
		fmt.Printf("        Wait until LXD is ready to handle requests\n")

		fmt.Printf("\n\nCommon options:\n")
		fmt.Printf("    --debug\n")
		fmt.Printf("        Enable debug mode\n")
		fmt.Printf("    --help\n")
		fmt.Printf("        Print this help message\n")
		fmt.Printf("    --logfile FILE\n")
		fmt.Printf("        Logfile to log to (e.g., /var/log/lxd/lxd.log)\n")
		fmt.Printf("    --syslog\n")
		fmt.Printf("        Enable syslog logging\n")
		fmt.Printf("    --verbose\n")
		fmt.Printf("        Enable verbose mode\n")
		fmt.Printf("    --version\n")
		fmt.Printf("        Print LXD's version number and exit\n")

		fmt.Printf("\nDaemon options:\n")
		fmt.Printf("    --group GROUP\n")
		fmt.Printf("        Group which owns the shared socket\n")

		fmt.Printf("\nDaemon debug options:\n")
		fmt.Printf("    --cpuprofile FILE\n")
		fmt.Printf("        Enable cpu profiling into the specified file\n")
		fmt.Printf("    --memprofile FILE\n")
		fmt.Printf("        Enable memory profiling into the specified file\n")
		fmt.Printf("    --print-goroutines-every SECONDS\n")
		fmt.Printf("        For debugging, print a complete stack trace every n seconds\n")

		fmt.Printf("\nInit options:\n")
		fmt.Printf("    --auto\n")
		fmt.Printf("        Automatic (non-interactive) mode\n")

		fmt.Printf("\nInit options for non-interactive mode (--auto):\n")
		fmt.Printf("    --network-address ADDRESS\n")
		fmt.Printf("        Address to bind LXD to (default: none)\n")
		fmt.Printf("    --network-port PORT\n")
		fmt.Printf("        Port to bind LXD to (default: 8443)\n")
		fmt.Printf("    --storage-backend NAME\n")
		fmt.Printf("        Storage backend to use (zfs or dir, default: dir)\n")
		fmt.Printf("    --storage-create-device DEVICE\n")
		fmt.Printf("        Setup device based storage using DEVICE\n")
		fmt.Printf("    --storage-create-loop SIZE\n")
		fmt.Printf("        Setup loop based storage with SIZE in GB\n")
		fmt.Printf("    --storage-pool NAME\n")
		fmt.Printf("        Storage pool to use or create\n")
		fmt.Printf("    --trust-password PASSWORD\n")
		fmt.Printf("        Password required to add new clients\n")

		fmt.Printf("\nShutdown options:\n")
		fmt.Printf("    --timeout SECONDS\n")
		fmt.Printf("        How long to wait before failing\n")

		fmt.Printf("\nWaitready options:\n")
		fmt.Printf("    --timeout SECONDS\n")
		fmt.Printf("        How long to wait before failing\n")

		fmt.Printf("\n\nInternal commands (don't call these directly):\n")
		fmt.Printf("    forkexec\n")
		fmt.Printf("        Execute a command in a container\n")
		fmt.Printf("    forkgetnet\n")
		fmt.Printf("        Get container network information\n")
		fmt.Printf("    forkgetfile\n")
		fmt.Printf("        Grab a file from a running container\n")
		fmt.Printf("    forkmigrate\n")
		fmt.Printf("        Restore a container after migration\n")
		fmt.Printf("    forkputfile\n")
		fmt.Printf("        Push a file to a running container\n")
		fmt.Printf("    forkstart\n")
		fmt.Printf("        Start a container\n")
		fmt.Printf("    callhook\n")
		fmt.Printf("        Call a container hook\n")
		fmt.Printf("    migratedumpsuccess\n")
		fmt.Printf("        Indicate that a migration dump was successful\n")
		fmt.Printf("    netcat\n")
		fmt.Printf("        Mirror a unix socket to stdin/stdout\n")
	}

	// Parse the arguments
	gnuflag.Parse(true)

	// Set the global variables
	debug = *argDebug
	verbose = *argVerbose

	if *argHelp {
		// The user asked for help via --help, so we shouldn't print to
		// stderr.
		gnuflag.SetOut(os.Stdout)
		gnuflag.Usage()
		return nil
	}

	// Deal with --version right here
	if *argVersion {
		fmt.Println(version.Version)
		return nil
	}

	if len(shared.VarPath("unix.sock")) > 107 {
		return fmt.Errorf("LXD_DIR is too long, must be < %d", 107-len("unix.sock"))
	}

	// Configure logging
	syslog := ""
	if *argSyslog {
		syslog = "lxd"
	}

	handler := eventsHandler{}
	var err error
	logger.Log, err = logging.GetLogger(syslog, *argLogfile, *argVerbose, *argDebug, handler)
	if err != nil {
		fmt.Printf("%s", err)
		return nil
	}

	// Process sub-commands
	if len(os.Args) > 1 {
		// "forkputfile", "forkgetfile", "forkmount" and "forkumount" are handled specially in nsexec.go
		// "forkgetnet" is partially handled in nsexec.go (setns)
		switch os.Args[1] {
		// Main commands
		case "activateifneeded":
			return cmdActivateIfNeeded()
		case "daemon":
			return cmdDaemon()
		case "callhook":
			return cmdCallHook(os.Args[1:])
		case "init":
			return cmdInit()
		case "ready":
			return cmdReady()
		case "shutdown":
			return cmdShutdown()
		case "waitready":
			return cmdWaitReady()

		// Internal commands
		case "forkgetnet":
			return cmdForkGetNet()
		case "forkmigrate":
			return cmdForkMigrate(os.Args[1:])
		case "forkstart":
			return cmdForkStart(os.Args[1:])
		case "forkexec":
			ret, err := cmdForkExec(os.Args[1:])
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			}
			os.Exit(ret)
		case "netcat":
			return cmdNetcat(os.Args[1:])
		case "migratedumpsuccess":
			return cmdMigrateDumpSuccess(os.Args[1:])
		}
	}

	// Fail if some other command is passed
	if gnuflag.NArg() > 0 {
		gnuflag.Usage()
		return fmt.Errorf("Unknown arguments")
	}

	return cmdDaemon()
}
