package main

import (
	"bufio"
	"fmt"
	"math/rand"
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
var argNetworkPort = gnuflag.Int("network-port", -1, "")
var argPrintGoroutinesEvery = gnuflag.Int("print-goroutines-every", -1, "")
var argStorageBackend = gnuflag.String("storage-backend", "dir", "")
var argStorageCreateDevice = gnuflag.String("storage-create-device", "", "")
var argStorageCreateLoop = gnuflag.Int("storage-create-loop", -1, "")
var argStoragePool = gnuflag.String("storage-pool", "", "")
var argSyslog = gnuflag.Bool("syslog", false, "")
var argTimeout = gnuflag.Int("timeout", -1, "")
var argTrustPassword = gnuflag.String("trust-password", "", "")
var argVerbose = gnuflag.Bool("verbose", false, "")
var argVersion = gnuflag.Bool("version", false, "")

// Global variables
var debug bool
var verbose bool

func init() {
	rand.Seed(time.Now().UTC().UnixNano())
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
		// "forkputfile" and "forkgetfile" are handled specially in copyfile.go
		switch os.Args[1] {
		case "activateifneeded":
			return activateIfNeeded()
		case "daemon":
			return daemon()
		case "forkmigrate":
			return MigrateContainer(os.Args[1:])
		case "forkstart":
			return startContainer(os.Args[1:])
		case "callhook":
			return callHook(os.Args[1:])
		case "init":
			return setupLXD()
		case "shutdown":
			return cleanShutdown()
		case "waitready":
			return waitReady()
		}
	}

	// Fail if some other command is passed
	if gnuflag.NArg() > 0 {
		gnuflag.Usage()
		return fmt.Errorf("Unknown arguments")
	}

	return daemon()
}

func callHook(args []string) error {
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

func daemon() error {
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

	neededPrograms := []string{"setfacl", "rsync", "tar", "xz"}
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

	d, err := startDaemon(*argGroup)

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

		shared.Log.Info(
			fmt.Sprintf("Received '%s signal', shutting down containers.", sig))

		containersShutdown(d)

		ret = d.Stop()
		wg.Done()
	}()

	go func() {
		<-d.shutdownChan

		shared.Log.Info(
			fmt.Sprintf("Asked to shutdown by API, shutting down containers."))

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

		shared.Log.Info(fmt.Sprintf("Received '%s signal', exiting.\n", sig))
		ret = d.Stop()
		wg.Done()
	}()

	wg.Wait()
	return ret
}

func cleanShutdown() error {
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

func activateIfNeeded() error {
	// Don't start a full daemon, we just need DB access
	d := &Daemon{
		IsMock:                false,
		imagesDownloading:     map[string]chan bool{},
		imagesDownloadingLock: sync.RWMutex{},
	}

	err := initializeDbObject(d, shared.VarPath("lxd.db"))
	if err != nil {
		return err
	}

	// Look for network socket
	value, err := d.ConfigValueGet("core.https_address")
	if err != nil {
		return err
	}

	if value != "" {
		shared.Debugf("Daemon has core.https_address set, activating...")
		_, err := lxd.NewClient(&lxd.DefaultConfig, "local")
		return err
	}

	// Look for auto-started or previously started containers
	d.IdmapSet, err = shared.DefaultIdmapSet()
	if err != nil {
		return err
	}

	containers, err := doContainersGet(d, true)
	if err != nil {
		return err
	}

	containerInfo := containers.(shared.ContainerInfoList)
	for _, container := range containerInfo {
		lastState := container.State.Config["volatile.last_state.power"]
		autoStart := container.State.ExpandedConfig["boot.autostart"]

		if lastState == "RUNNING" || lastState == "Running" || autoStart == "true" {
			shared.Debugf("Daemon has auto-started containers, activating...")
			_, err := lxd.NewClient(&lxd.DefaultConfig, "local")
			return err
		}
	}

	shared.Debugf("No need to start the daemon now.")
	return nil
}

func waitReady() error {
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

			err = c.Finger()
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

func setupLXD() error {
	var storageBackend string // dir or zfs
	var storageMode string    // existing, loop or device
	var storageLoopSize int   // Size in GB
	var storageDevice string  // Path
	var storagePool string    // pool name
	var networkAddress string // Address
	var networkPort int       // Port
	var trustPassword string  // Trust password

	reader := bufio.NewReader(os.Stdin)

	askBool := func(question string) bool {
		for {
			fmt.Printf(question)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSuffix(input, "\n")
			if shared.StringInSlice(strings.ToLower(input), []string{"yes", "y", "true"}) {
				return true
			} else if shared.StringInSlice(strings.ToLower(input), []string{"no", "n", "false"}) {
				return false
			}

			fmt.Printf("Invalid input, try again.\n\n")
		}
	}

	askChoice := func(question string, choices []string) string {
		for {
			fmt.Printf(question)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSuffix(input, "\n")
			if shared.StringInSlice(input, choices) {
				return input
			}

			fmt.Printf("Invalid input, try again.\n\n")
		}
	}

	askInt := func(question string, min int, max int) int {
		for {
			fmt.Printf(question)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSuffix(input, "\n")
			intInput, err := strconv.Atoi(input)

			if err == nil && (min == -1 || intInput >= min) && (max == -1 || intInput <= max) {
				return intInput
			}

			fmt.Printf("Invalid input, try again.\n\n")
		}
	}

	askString := func(question string) string {
		for {
			fmt.Printf(question)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSuffix(input, "\n")
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

			if len(inFirst) != 0 && inFirst == inSecond {
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
		// Do a bunch of sanity checks
		if *argStorageBackend == "dir" {
			if *argStorageCreateLoop != -1 || *argStorageCreateDevice != "" || *argStoragePool != "" {
				return fmt.Errorf("None of --storage-pool, --storage-create-device or --storage-create-pool may be used with the 'dir' backend.")
			}
		}

		if *argStorageBackend == "zfs" {
			if *argStorageCreateLoop != -1 && *argStorageCreateDevice != "" {
				return fmt.Errorf("Only one of --storage-create-device or --storage-create-pool can be specified with the 'zfs' backend.")
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
		storageBackend = askChoice("Name of the storage backend to use (dir or zfs): ", []string{"dir", "zfs"})

		if storageBackend == "zfs" {
			if askBool("Create a new ZFS pool (yes/no)? ") {
				storagePool = askString("Name of the new ZFS pool: ")
				if askBool("Would you like to use an existing block device (yes/no)? ") {
					storageDevice = askString("Path to the existing block device: ")
					storageMode = "device"
				} else {
					storageLoopSize = askInt("Size in GB of the new loop device (1GB minimum): ", 1, -1)
					storageMode = "loop"
				}
			} else {
				storagePool = askString("Name of the existing ZFS pool or dataset: ")
				storageMode = "existing"
			}
		}

		if askBool("Would you like LXD to be available over the network (yes/no)? ") {
			networkAddress = askString("Address to bind LXD to: ")
			networkPort = askInt("Port to bind LXD to: ", 1, 65535)
			trustPassword = askPassword("Trust password for new clients: ")
		}
	}

	if !shared.StringInSlice(storageBackend, []string{"dir", "zfs"}) {
		return fmt.Errorf("Invalid storage backend: %s", storageBackend)
	}

	// Unset all storage keys, core.https_address and core.trust_password
	for _, key := range []string{"core.https_address", "core.trust_password"} {
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
		out, err := exec.LookPath("zfs")
		if err != nil || len(out) == 0 {
			return fmt.Errorf("The 'zfs' tool isn't available")
		}

		if storageMode == "loop" {
			storageDevice = shared.VarPath("zfs.img")
			f, err := os.Create(storageDevice)
			if err != nil {
				return fmt.Errorf("Failed to open %s: %s", storageDevice, err)
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
				"-m", "none").CombinedOutput()
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

	fmt.Printf("LXD has been succesfuly configured.\n")
	return nil
}
