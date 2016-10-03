package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh/terminal"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/logging"
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
		fmt.Println(shared.Version)
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
	shared.Log, err = logging.GetLogger(syslog, *argLogfile, *argVerbose, *argDebug, handler)
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
			return printnet()
		case "forkmigrate":
			return MigrateContainer(os.Args[1:])
		case "forkstart":
			return startContainer(os.Args[1:])
		case "forkexec":
			ret, err := execContainer(os.Args[1:])
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			}
			os.Exit(ret)
		case "netcat":
			return Netcat(os.Args[1:])
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

func cmdCallHook(args []string) error {
	if len(args) < 4 {
		return fmt.Errorf("Invalid arguments")
	}

	path := args[1]
	id := args[2]
	state := args[3]
	target := ""

	err := os.Setenv("LXD_DIR", path)
	if err != nil {
		return err
	}

	c, err := lxd.NewClient(&lxd.DefaultConfig, "local")
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/internal/containers/%s/on%s", c.BaseURL, id, state)

	if state == "stop" {
		target = os.Getenv("LXC_TARGET")
		if target == "" {
			target = "unknown"
		}
		url = fmt.Sprintf("%s?target=%s", url, target)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	hook := make(chan error, 1)
	go func() {
		raw, err := c.Http.Do(req)
		if err != nil {
			hook <- err
			return
		}

		_, err = lxd.HoistResponse(raw, lxd.Sync)
		if err != nil {
			hook <- err
			return
		}

		hook <- nil
	}()

	select {
	case err := <-hook:
		if err != nil {
			return err
		}
		break
	case <-time.After(30 * time.Second):
		return fmt.Errorf("Hook didn't finish within 30s")
	}

	if target == "reboot" {
		return fmt.Errorf("Reboot must be handled by LXD.")
	}

	return nil
}

func cmdDaemon() error {
	if *argCPUProfile != "" {
		f, err := os.Create(*argCPUProfile)
		if err != nil {
			fmt.Printf("Error opening cpu profile file: %s\n", err)
			return nil
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if *argMemProfile != "" {
		go memProfiler(*argMemProfile)
	}

	neededPrograms := []string{"dnsmasq", "setfacl", "rsync", "tar", "unsquashfs", "xz"}
	for _, p := range neededPrograms {
		_, err := exec.LookPath(p)
		if err != nil {
			return err
		}
	}

	if *argPrintGoroutinesEvery > 0 {
		go func() {
			for {
				time.Sleep(time.Duration(*argPrintGoroutinesEvery) * time.Second)
				shared.PrintStack()
			}
		}()
	}

	d := &Daemon{
		group:     *argGroup,
		SetupMode: shared.PathExists(shared.VarPath(".setup_mode"))}
	err := d.Init()
	if err != nil {
		if d != nil && d.db != nil {
			d.db.Close()
		}
		return err
	}

	var ret error
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		ch := make(chan os.Signal)
		signal.Notify(ch, syscall.SIGPWR)
		sig := <-ch

		shared.LogInfof("Received '%s signal', shutting down containers.", sig)

		containersShutdown(d)

		ret = d.Stop()
		wg.Done()
	}()

	go func() {
		<-d.shutdownChan

		shared.LogInfof("Asked to shutdown by API, shutting down containers.")

		containersShutdown(d)

		ret = d.Stop()
		wg.Done()
	}()

	go func() {
		ch := make(chan os.Signal)
		signal.Notify(ch, syscall.SIGINT)
		signal.Notify(ch, syscall.SIGQUIT)
		signal.Notify(ch, syscall.SIGTERM)
		sig := <-ch

		shared.LogInfof("Received '%s signal', exiting.", sig)
		ret = d.Stop()
		wg.Done()
	}()

	wg.Wait()
	return ret
}

func cmdReady() error {
	c, err := lxd.NewClient(&lxd.DefaultConfig, "local")
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", c.BaseURL+"/internal/ready", nil)
	if err != nil {
		return err
	}

	raw, err := c.Http.Do(req)
	if err != nil {
		return err
	}

	_, err = lxd.HoistResponse(raw, lxd.Sync)
	if err != nil {
		return err
	}

	return nil
}

func cmdShutdown() error {
	var timeout int

	if *argTimeout == -1 {
		timeout = 60
	} else {
		timeout = *argTimeout
	}

	c, err := lxd.NewClient(&lxd.DefaultConfig, "local")
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", c.BaseURL+"/internal/shutdown", nil)
	if err != nil {
		return err
	}

	_, err = c.Http.Do(req)
	if err != nil {
		return err
	}

	monitor := make(chan error, 1)
	go func() {
		monitor <- c.Monitor(nil, func(m interface{}) {})
	}()

	select {
	case <-monitor:
		break
	case <-time.After(time.Second * time.Duration(timeout)):
		return fmt.Errorf("LXD still running after %ds timeout.", timeout)
	}

	return nil
}

func cmdActivateIfNeeded() error {
	// Don't start a full daemon, we just need DB access
	d := &Daemon{
		imagesDownloading:     map[string]chan bool{},
		imagesDownloadingLock: sync.RWMutex{},
		lxcpath:               shared.VarPath("containers"),
	}

	err := initializeDbObject(d, shared.VarPath("lxd.db"))
	if err != nil {
		return err
	}

	/* Load all config values from the database */
	err = daemonConfigInit(d.db)
	if err != nil {
		return err
	}

	// Look for network socket
	value := daemonConfig["core.https_address"].Get()
	if value != "" {
		shared.LogDebugf("Daemon has core.https_address set, activating...")
		_, err := lxd.NewClient(&lxd.DefaultConfig, "local")
		return err
	}

	// Look for auto-started or previously started containers
	d.IdmapSet, err = shared.DefaultIdmapSet()
	if err != nil {
		return err
	}

	result, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return err
	}

	for _, name := range result {
		c, err := containerLoadByName(d, name)
		if err != nil {
			return err
		}

		config := c.ExpandedConfig()
		lastState := config["volatile.last_state.power"]
		autoStart := config["boot.autostart"]

		if c.IsRunning() {
			shared.LogDebugf("Daemon has running containers, activating...")
			_, err := lxd.NewClient(&lxd.DefaultConfig, "local")
			return err
		}

		if lastState == "RUNNING" || lastState == "Running" || shared.IsTrue(autoStart) {
			shared.LogDebugf("Daemon has auto-started containers, activating...")
			_, err := lxd.NewClient(&lxd.DefaultConfig, "local")
			return err
		}
	}

	shared.LogDebugf("No need to start the daemon now.")
	return nil
}

func cmdWaitReady() error {
	var timeout int

	if *argTimeout == -1 {
		timeout = 15
	} else {
		timeout = *argTimeout
	}

	finger := make(chan error, 1)
	go func() {
		for {
			c, err := lxd.NewClient(&lxd.DefaultConfig, "local")
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}

			req, err := http.NewRequest("GET", c.BaseURL+"/internal/ready", nil)
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}

			raw, err := c.Http.Do(req)
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}

			_, err = lxd.HoistResponse(raw, lxd.Sync)
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}

			finger <- nil
			return
		}
	}()

	select {
	case <-finger:
		break
	case <-time.After(time.Second * time.Duration(timeout)):
		return fmt.Errorf("LXD still not running after %ds timeout.", timeout)
	}

	return nil
}

func cmdInit() error {
	var defaultPrivileged int // controls whether we set security.privileged=true
	var storageBackend string // dir or zfs
	var storageMode string    // existing, loop or device
	var storageLoopSize int64 // Size in GB
	var storageDevice string  // Path
	var storagePool string    // pool name
	var networkAddress string // Address
	var networkPort int64     // Port
	var trustPassword string  // Trust password
	var imagesAutoUpdate bool // controls whether we set images.auto_update_interval to 0
	var bridgeName string     // Bridge name
	var bridgeIPv4 string     // IPv4 address
	var bridgeIPv4Nat bool    // IPv4 address
	var bridgeIPv6 string     // IPv6 address
	var bridgeIPv6Nat bool    // IPv6 address

	// Detect userns
	defaultPrivileged = -1
	runningInUserns = shared.RunningInUserNS()
	imagesAutoUpdate = true

	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	backendsAvailable := []string{"dir"}
	backendsSupported := []string{"dir", "zfs"}

	// Detect zfs
	out, err := exec.LookPath("zfs")
	if err == nil && len(out) != 0 {
		backendsAvailable = append(backendsAvailable, "zfs")
	}

	reader := bufio.NewReader(os.Stdin)

	askBool := func(question string, default_ string) bool {
		for {
			fmt.Printf(question)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSuffix(input, "\n")
			if input == "" {
				input = default_
			}
			if shared.StringInSlice(strings.ToLower(input), []string{"yes", "y"}) {
				return true
			} else if shared.StringInSlice(strings.ToLower(input), []string{"no", "n"}) {
				return false
			}

			fmt.Printf("Invalid input, try again.\n\n")
		}
	}

	askChoice := func(question string, choices []string, default_ string) string {
		for {
			fmt.Printf(question)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSuffix(input, "\n")
			if input == "" {
				input = default_
			}
			if shared.StringInSlice(input, choices) {
				return input
			}

			fmt.Printf("Invalid input, try again.\n\n")
		}
	}

	askInt := func(question string, min int64, max int64, default_ string) int64 {
		for {
			fmt.Printf(question)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSuffix(input, "\n")
			if input == "" {
				input = default_
			}
			intInput, err := strconv.ParseInt(input, 10, 64)

			if err == nil && (min == -1 || intInput >= min) && (max == -1 || intInput <= max) {
				return intInput
			}

			fmt.Printf("Invalid input, try again.\n\n")
		}
	}

	askString := func(question string, default_ string, validate func(string) error) string {
		for {
			fmt.Printf(question)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSuffix(input, "\n")
			if input == "" {
				input = default_
			}
			if validate != nil {
				result := validate(input)
				if result != nil {
					fmt.Printf("Invalid input: %s\n\n", result)
					continue
				}
			}
			if len(input) != 0 {
				return input
			}

			fmt.Printf("Invalid input, try again.\n\n")
		}
	}

	askPassword := func(question string) string {
		for {
			fmt.Printf(question)
			pwd, _ := terminal.ReadPassword(0)
			fmt.Printf("\n")
			inFirst := string(pwd)
			inFirst = strings.TrimSuffix(inFirst, "\n")

			fmt.Printf("Again: ")
			pwd, _ = terminal.ReadPassword(0)
			fmt.Printf("\n")
			inSecond := string(pwd)
			inSecond = strings.TrimSuffix(inSecond, "\n")

			if inFirst == inSecond {
				return inFirst
			}

			fmt.Printf("Invalid input, try again.\n\n")
		}
	}

	// Confirm that LXD is online
	c, err := lxd.NewClient(&lxd.DefaultConfig, "local")
	if err != nil {
		return fmt.Errorf("Unable to talk to LXD: %s", err)
	}

	// Check that we have no containers or images in the store
	containers, err := c.ListContainers()
	if err != nil {
		return fmt.Errorf("Unable to list the LXD containers: %s", err)
	}

	images, err := c.ListImages()
	if err != nil {
		return fmt.Errorf("Unable to list the LXD images: %s", err)
	}

	if len(containers) > 0 || len(images) > 0 {
		return fmt.Errorf("You have existing containers or images. lxd init requires an empty LXD.")
	}

	if *argAuto {
		if *argStorageBackend == "" {
			*argStorageBackend = "dir"
		}

		// Do a bunch of sanity checks
		if !shared.StringInSlice(*argStorageBackend, backendsSupported) {
			return fmt.Errorf("The requested backend '%s' isn't supported by lxd init.", *argStorageBackend)
		}

		if !shared.StringInSlice(*argStorageBackend, backendsAvailable) {
			return fmt.Errorf("The requested backend '%s' isn't available on your system (missing tools).", *argStorageBackend)
		}

		if *argStorageBackend == "dir" {
			if *argStorageCreateLoop != -1 || *argStorageCreateDevice != "" || *argStoragePool != "" {
				return fmt.Errorf("None of --storage-pool, --storage-create-device or --storage-create-loop may be used with the 'dir' backend.")
			}
		}

		if *argStorageBackend == "zfs" {
			if *argStorageCreateLoop != -1 && *argStorageCreateDevice != "" {
				return fmt.Errorf("Only one of --storage-create-device or --storage-create-loop can be specified with the 'zfs' backend.")
			}

			if *argStoragePool == "" {
				return fmt.Errorf("--storage-pool must be specified with the 'zfs' backend.")
			}
		}

		if *argNetworkAddress == "" {
			if *argNetworkPort != -1 {
				return fmt.Errorf("--network-port cannot be used without --network-address.")
			}
			if *argTrustPassword != "" {
				return fmt.Errorf("--trust-password cannot be used without --network-address.")
			}
		}

		// Set the local variables
		if *argStorageCreateDevice != "" {
			storageMode = "device"
		} else if *argStorageCreateLoop != -1 {
			storageMode = "loop"
		} else {
			storageMode = "existing"
		}

		storageBackend = *argStorageBackend
		storageLoopSize = *argStorageCreateLoop
		storageDevice = *argStorageCreateDevice
		storagePool = *argStoragePool
		networkAddress = *argNetworkAddress
		networkPort = *argNetworkPort
		trustPassword = *argTrustPassword
	} else {
		if *argStorageBackend != "" || *argStorageCreateDevice != "" || *argStorageCreateLoop != -1 || *argStoragePool != "" || *argNetworkAddress != "" || *argNetworkPort != -1 || *argTrustPassword != "" {
			return fmt.Errorf("Init configuration is only valid with --auto")
		}

		defaultStorage := "dir"
		if shared.StringInSlice("zfs", backendsAvailable) {
			defaultStorage = "zfs"
		}

		storageBackend = askChoice(fmt.Sprintf("Name of the storage backend to use (dir or zfs) [default=%s]: ", defaultStorage), backendsSupported, defaultStorage)

		if !shared.StringInSlice(storageBackend, backendsSupported) {
			return fmt.Errorf("The requested backend '%s' isn't supported by lxd init.", storageBackend)
		}

		if !shared.StringInSlice(storageBackend, backendsAvailable) {
			return fmt.Errorf("The requested backend '%s' isn't available on your system (missing tools).", storageBackend)
		}

		if storageBackend == "zfs" {
			if askBool("Create a new ZFS pool (yes/no) [default=yes]? ", "yes") {
				storagePool = askString("Name of the new ZFS pool [default=lxd]: ", "lxd", nil)
				if askBool("Would you like to use an existing block device (yes/no) [default=no]? ", "no") {
					deviceExists := func(path string) error {
						if !shared.IsBlockdevPath(path) {
							return fmt.Errorf("'%s' is not a block device", path)
						}
						return nil
					}
					storageDevice = askString("Path to the existing block device: ", "", deviceExists)
					storageMode = "device"
				} else {
					st := syscall.Statfs_t{}
					err := syscall.Statfs(shared.VarPath(), &st)
					if err != nil {
						return fmt.Errorf("couldn't statfs %s: %s", shared.VarPath(), err)
					}

					/* choose 15 GB < x < 100GB, where x is 20% of the disk size */
					def := uint64(st.Frsize) * st.Blocks / (1024 * 1024 * 1024) / 5
					if def > 100 {
						def = 100
					}
					if def < 15 {
						def = 15
					}

					q := fmt.Sprintf("Size in GB of the new loop device (1GB minimum) [default=%d]: ", def)
					storageLoopSize = askInt(q, 1, -1, fmt.Sprintf("%d", def))
					storageMode = "loop"
				}
			} else {
				storagePool = askString("Name of the existing ZFS pool or dataset: ", "", nil)
				storageMode = "existing"
			}
		}

		if runningInUserns {
			fmt.Printf(`
We detected that you are running inside an unprivileged container.
This means that unless you manually configured your host otherwise,
you will not have enough uid and gid to allocate to your containers.

LXD can re-use your container's own allocation to avoid the problem.
Doing so makes your nested containers slightly less safe as they could
in theory attack their parent container and gain more privileges than
they otherwise would.

`)
			if askBool("Would you like to have your containers share their parent's allocation (yes/no) [default=yes]? ", "yes") {
				defaultPrivileged = 1
			} else {
				defaultPrivileged = 0
			}
		}

		if askBool("Would you like LXD to be available over the network (yes/no) [default=no]? ", "no") {
			isIPAddress := func(s string) error {
				if s != "all" && net.ParseIP(s) == nil {
					return fmt.Errorf("'%s' is not an IP address", s)
				}
				return nil
			}

			networkAddress = askString("Address to bind LXD to (not including port) [default=all]: ", "all", isIPAddress)
			if networkAddress == "all" {
				networkAddress = "::"
			}

			if net.ParseIP(networkAddress).To4() == nil {
				networkAddress = fmt.Sprintf("[%s]", networkAddress)
			}
			networkPort = askInt("Port to bind LXD to [default=8443]: ", 1, 65535, "8443")
			trustPassword = askPassword("Trust password for new clients: ")
		}

		if !askBool("Would you like stale cached images to be updated automatically (yes/no) [default=yes]? ", "yes") {
			imagesAutoUpdate = false
		}

		if askBool("Would you like to create a new network bridge (yes/no) [default=yes]? ", "yes") {
			bridgeName = askString("What should the new bridge be called [default=lxdbr0]? ", "lxdbr0", networkValidName)
			bridgeIPv4 = askString("What IPv4 subnet should be used (CIDR notation, “auto” or “none”) [default=auto]? ", "auto", func(value string) error {
				if shared.StringInSlice(value, []string{"auto", "none"}) {
					return nil
				}
				return networkValidAddressCIDRV4(value)
			})

			if !shared.StringInSlice(bridgeIPv4, []string{"auto", "none"}) {
				bridgeIPv4Nat = askBool("Would you like LXD to NAT IPv4 traffic on your bridge? [default=yes]? ", "yes")
			}

			bridgeIPv6 = askString("What IPv6 subnet should be used (CIDR notation, “auto” or “none”) [default=auto]? ", "auto", func(value string) error {
				if shared.StringInSlice(value, []string{"auto", "none"}) {
					return nil
				}
				return networkValidAddressCIDRV6(value)
			})

			if !shared.StringInSlice(bridgeIPv6, []string{"auto", "none"}) {
				bridgeIPv6Nat = askBool("Would you like LXD to NAT IPv6 traffic on your bridge? [default=yes]? ", "yes")
			}
		}
	}

	if !shared.StringInSlice(storageBackend, []string{"dir", "zfs"}) {
		return fmt.Errorf("Invalid storage backend: %s", storageBackend)
	}

	// Unset all storage keys, core.https_address and core.trust_password
	for _, key := range []string{"storage.zfs_pool_name", "core.https_address", "core.trust_password"} {
		_, err = c.SetServerConfig(key, "")
		if err != nil {
			return err
		}
	}

	// Destroy any existing loop device
	for _, file := range []string{"zfs.img"} {
		os.Remove(shared.VarPath(file))
	}

	if storageBackend == "zfs" {
		_ = exec.Command("modprobe", "zfs").Run()

		if storageMode == "loop" {
			storageDevice = shared.VarPath("zfs.img")
			f, err := os.Create(storageDevice)
			if err != nil {
				return fmt.Errorf("Failed to open %s: %s", storageDevice, err)
			}

			err = f.Chmod(0600)
			if err != nil {
				return fmt.Errorf("Failed to chmod %s: %s", storageDevice, err)
			}

			err = f.Truncate(int64(storageLoopSize * 1024 * 1024 * 1024))
			if err != nil {
				return fmt.Errorf("Failed to create sparse file %s: %s", storageDevice, err)
			}

			err = f.Close()
			if err != nil {
				return fmt.Errorf("Failed to close %s: %s", storageDevice, err)
			}
		}

		if shared.StringInSlice(storageMode, []string{"loop", "device"}) {
			output, err := exec.Command(
				"zpool",
				"create", storagePool, storageDevice,
				"-f", "-m", "none", "-O", "compression=on").CombinedOutput()
			if err != nil {
				return fmt.Errorf("Failed to create the ZFS pool: %s", output)
			}
		}

		// Configure LXD to use the pool
		_, err = c.SetServerConfig("storage.zfs_pool_name", storagePool)
		if err != nil {
			return err
		}
	}

	if defaultPrivileged == 0 {
		err = c.SetProfileConfigItem("default", "security.privileged", "")
		if err != nil {
			return err
		}
	} else if defaultPrivileged == 1 {
		err = c.SetProfileConfigItem("default", "security.privileged", "true")
		if err != nil {
		}
	}

	if imagesAutoUpdate {
		ss, err := c.ServerStatus()
		if err != nil {
			return err
		}
		if val, ok := ss.Config["images.auto_update_interval"]; ok && val == "0" {
			_, err = c.SetServerConfig("images.auto_update_interval", "")
			if err != nil {
				return err
			}
		}
	} else {
		_, err = c.SetServerConfig("images.auto_update_interval", "0")
		if err != nil {
			return err
		}
	}

	if networkAddress != "" {
		_, err = c.SetServerConfig("core.https_address", fmt.Sprintf("%s:%d", networkAddress, networkPort))
		if err != nil {
			return err
		}

		if trustPassword != "" {
			_, err = c.SetServerConfig("core.trust_password", trustPassword)
			if err != nil {
				return err
			}
		}
	}

	if bridgeName != "" {
		bridgeConfig := map[string]string{}
		bridgeConfig["ipv4.address"] = bridgeIPv4
		bridgeConfig["ipv6.address"] = bridgeIPv6

		if bridgeIPv4Nat {
			bridgeConfig["ipv4.nat"] = "true"
		}

		if bridgeIPv6Nat {
			bridgeConfig["ipv6.nat"] = "true"
		}

		err = c.NetworkCreate(bridgeName, bridgeConfig)
		if err != nil {
			return err
		}

		props := []string{"nictype=bridged", fmt.Sprintf("parent=%s", bridgeName)}
		_, err = c.ProfileDeviceAdd("default", "eth0", "nic", props)
		if err != nil {
			return err
		}
	}

	fmt.Printf("LXD has been successfully configured.\n")
	return nil
}

func printnet() error {
	networks := map[string]shared.ContainerStateNetwork{}

	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}

	stats := map[string][]int64{}

	content, err := ioutil.ReadFile("/proc/net/dev")
	if err == nil {
		for _, line := range strings.Split(string(content), "\n") {
			fields := strings.Fields(line)

			if len(fields) != 17 {
				continue
			}

			rxBytes, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				continue
			}

			rxPackets, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				continue
			}

			txBytes, err := strconv.ParseInt(fields[9], 10, 64)
			if err != nil {
				continue
			}

			txPackets, err := strconv.ParseInt(fields[10], 10, 64)
			if err != nil {
				continue
			}

			intName := strings.TrimSuffix(fields[0], ":")
			stats[intName] = []int64{rxBytes, rxPackets, txBytes, txPackets}
		}
	}

	for _, netIf := range interfaces {
		netState := "down"
		netType := "unknown"

		if netIf.Flags&net.FlagBroadcast > 0 {
			netType = "broadcast"
		}

		if netIf.Flags&net.FlagPointToPoint > 0 {
			netType = "point-to-point"
		}

		if netIf.Flags&net.FlagLoopback > 0 {
			netType = "loopback"
		}

		if netIf.Flags&net.FlagUp > 0 {
			netState = "up"
		}

		network := shared.ContainerStateNetwork{
			Addresses: []shared.ContainerStateNetworkAddress{},
			Counters:  shared.ContainerStateNetworkCounters{},
			Hwaddr:    netIf.HardwareAddr.String(),
			Mtu:       netIf.MTU,
			State:     netState,
			Type:      netType,
		}

		addrs, err := netIf.Addrs()
		if err == nil {
			for _, addr := range addrs {
				fields := strings.SplitN(addr.String(), "/", 2)
				if len(fields) != 2 {
					continue
				}

				family := "inet"
				if strings.Contains(fields[0], ":") {
					family = "inet6"
				}

				scope := "global"
				if strings.HasPrefix(fields[0], "127") {
					scope = "local"
				}

				if fields[0] == "::1" {
					scope = "local"
				}

				if strings.HasPrefix(fields[0], "169.254") {
					scope = "link"
				}

				if strings.HasPrefix(fields[0], "fe80:") {
					scope = "link"
				}

				address := shared.ContainerStateNetworkAddress{}
				address.Family = family
				address.Address = fields[0]
				address.Netmask = fields[1]
				address.Scope = scope

				network.Addresses = append(network.Addresses, address)
			}
		}

		counters, ok := stats[netIf.Name]
		if ok {
			network.Counters.BytesReceived = counters[0]
			network.Counters.PacketsReceived = counters[1]
			network.Counters.BytesSent = counters[2]
			network.Counters.PacketsSent = counters[3]
		}

		networks[netIf.Name] = network
	}

	buf, err := json.Marshal(networks)
	if err != nil {
		return err
	}

	fmt.Printf("%s\n", buf)

	return nil
}

func cmdMigrateDumpSuccess(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("bad migrate dump success args %s", args)
	}

	c, err := lxd.NewClient(&lxd.DefaultConfig, "local")
	if err != nil {
		return err
	}

	conn, err := c.Websocket(args[1], args[2])
	if err != nil {
		return err
	}
	conn.Close()

	return c.WaitForSuccess(args[1])
}
