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

	"github.com/codegangsta/cli"
	"golang.org/x/crypto/ssh/terminal"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logging"
	"github.com/ubuntu-core/snappy/i18n"
)

var commandGlobalFlags = []cli.Flag{
	cli.BoolFlag{
		Name:  "debug",
		Usage: i18n.G("Print debug information."),
	},

	cli.BoolFlag{
		Name:  "verbose",
		Usage: i18n.G("Print verbose information."),
	},

	cli.StringFlag{
		Name:  "logfile",
		Usage: i18n.G("Logfile to log to (e.g., /var/log/lxd/lxd.log)."),
	},

	cli.BoolFlag{
		Name:  "syslog",
		Usage: i18n.G("Enable syslog logging"),
	},
}

func commandGlobalFlagsWrapper(flags ...cli.Flag) []cli.Flag {
	return append(commandGlobalFlags, flags...)
}

func commandAction(c *cli.Context) {
	if err := run(c); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func main() {
	app := cli.NewApp()
	app.Name = "lxd"
	app.Version = shared.Version
	app.Usage = "LXD is pronounced lex-dee."
	app.Flags = commandGlobalFlags
	app.Commands = []cli.Command{

		cli.Command{
			Name:   "activateifneeded",
			Usage:  i18n.G("Check if LXD should be started (at boot) and if so, spawns it through socket activation."),
			Flags:  commandGlobalFlags,
			Action: commandAction,
		},

		cli.Command{
			Name:      "daemon",
			Usage:     i18n.G("Start the main LXD daemon."),
			ArgsUsage: i18n.G("[--group=lxd] [--cpuprofile=FILE] [--memprofile=FILE] [--print-goroutines-every=SECONDS]"),
			Flags: commandGlobalFlagsWrapper(
				cli.StringFlag{
					Name:  "group",
					Usage: i18n.G("Group which owns the shared socket."),
				},

				cli.StringFlag{
					Name:  "cpuprofile",
					Usage: i18n.G("Enable cpu profiling into the specified file."),
				},

				cli.StringFlag{
					Name:  "memprofile",
					Usage: i18n.G("Enable memory profiling into the specified file."),
				},

				cli.IntFlag{
					Name:  "print-goroutines-every",
					Value: -1,
					Usage: i18n.G("For debugging, print a complete stack trace every n seconds."),
				},
			),
			Action: commandAction,
		},

		cli.Command{
			Name:      "init",
			Usage:     i18n.G("Setup storage and networking"),
			ArgsUsage: i18n.G("[--auto] [--network-address=IP] [--network-port=8443] [--storage-backend=dir] [--storage-create-device=DEVICE] [--storage-create-loop=SIZE] [--storage-pool=POOL] [--trust-password=]"),
			Flags: commandGlobalFlagsWrapper(
				cli.BoolFlag{
					Name:  "auto",
					Usage: i18n.G("Automatic (non-interactive) mode."),
				},

				cli.StringFlag{
					Name:  "network-address",
					Usage: i18n.G("Address to bind LXD to (default: none)."),
				},

				cli.IntFlag{
					Name:  "network-port",
					Usage: i18n.G("Port to bind LXD to (default: 8443)."),
				},

				cli.StringFlag{
					Name:  "storage-backend",
					Value: "dir",
					Usage: i18n.G("Storage backend to use (zfs or dir, default: dir)."),
				},

				cli.IntFlag{
					Name:  "storage-create-loop",
					Value: -1,
					Usage: i18n.G("Setup loop based storage with SIZE in GB."),
				},

				cli.StringFlag{
					Name:  "storage-pool",
					Usage: i18n.G("Storage pool to use or create."),
				},

				cli.StringFlag{
					Name:  "trust-password",
					Usage: i18n.G("Password required to add new clients."),
				},
			),
			Action: commandAction,
		},

		cli.Command{
			Name:      "shutdown",
			Usage:     i18n.G("Perform a clean shutdown of LXD and all running containers."),
			ArgsUsage: i18n.G("[--timeout=60]"),
			Flags: commandGlobalFlagsWrapper(
				cli.IntFlag{
					Name:  "timeout",
					Value: 60,
					Usage: i18n.G("How long to wait before failing."),
				},
			),

			Action: commandAction,
		},

		cli.Command{
			Name:      "waitready",
			Usage:     i18n.G("Wait until LXD is ready to handle requests."),
			ArgsUsage: i18n.G("[--timeout=15]"),
			Flags: commandGlobalFlagsWrapper(
				cli.IntFlag{
					Name:  "timeout",
					Value: 15,
					Usage: i18n.G("Wait until LXD is ready to handle requests."),
				},
			),

			Action: commandAction,
		},

		cli.Command{
			Name:  "version",
			Usage: i18n.G("Prints the version number of LXD."),

			Action: func(c *cli.Context) {
				println(shared.Version)
			},
		},

		cli.Command{
			Name:  "forkgetnet",
			Usage: i18n.G("INTERNAL: Get container network information."),
			Flags: commandGlobalFlags,

			Action: commandAction,
		},

		cli.Command{
			Name:  "forkgetfile",
			Usage: i18n.G("INTERNAL: Grab a file from a running container."),
			Flags: commandGlobalFlags,

			Action: commandAction,
		},

		cli.Command{
			Name:  "forkmigrate",
			Usage: i18n.G("INTERNAL: Restore a container after migration."),
			Flags: commandGlobalFlags,

			Action: commandAction,
		},

		cli.Command{
			Name:  "forkputfile",
			Usage: i18n.G("INTERNAL: Push a file to a running container."),
			Flags: commandGlobalFlags,

			Action: commandAction,
		},

		cli.Command{
			Name:  "forkstart",
			Usage: i18n.G("INTERNAL: Start a container."),
			Flags: commandGlobalFlags,

			Action: commandAction,
		},

		cli.Command{
			Name:  "callhook",
			Usage: i18n.G("INTERNAL: Call a container hook."),
			Flags: commandGlobalFlags,

			Action: commandAction,
		},
	}

	app.Action = commandAction

	app.Run(os.Args)
}

// Global variables
var debug bool
var verbose bool

func init() {
	rand.Seed(time.Now().UTC().UnixNano())
}

func run(c *cli.Context) error {
	// Set the global variables
	debug = false
	if c.GlobalBool("debug") || c.Bool("debug") {
		debug = true
	}
	verbose = false
	if c.GlobalBool("verbose") || c.Bool("verbose") {
		verbose = true
	}

	if len(shared.VarPath("unix.sock")) > 107 {
		return fmt.Errorf("LXD_DIR is too long, must be < %d", 107-len("unix.sock"))
	}

	// Configure logging
	syslog := ""
	if c.String("syslog") != "" {
		syslog = c.String("syslog")
	}
	if syslog == "" && c.GlobalString("syslog") != "" {
		syslog = c.GlobalString("syslog")
	}

	logfile := ""
	if c.String("logfile") != "" {
		logfile = c.String("logfile")
	}
	if logfile == "" && c.GlobalString("logfile") != "" {
		logfile = c.GlobalString("logfile")
	}

	handler := eventsHandler{}
	var err error
	shared.Log, err = logging.GetLogger(syslog, logfile, verbose, debug, handler)
	if err != nil {
		fmt.Printf("%s", err)
		return nil
	}

	// Process sub-commands
	if c.Command.Name != "" {
		// "forkputfile", "forkgetfile", "forkmount" and "forkumount" are handled specially in nsexec.go
		// "forkgetnet" is partially handled in nsexec.go (setns)
		switch c.Command.Name {
		case "activateifneeded":
			return cmdActivateIfNeeded()
		case "daemon":
			return cmdDaemon(c)
		case "callhook":
			return cmdCallHook(c.Args())
		case "init":
			return cmdInit(c)
		case "ready":
			return cmdReady()
		case "shutdown":
			return cmdShutdown(c)
		case "waitready":
			return cmdWaitReady(c)

		// Internal commands
		case "forkgetnet":
			return printnet()
		case "forkmigrate":
			return MigrateContainer(c.Args())
		case "forkstart":
			return startContainer(c.Args())
		}
	}

	if len(c.Args()) > 0 {
		return fmt.Errorf("Unknown arguments, run 'lxd help' for the usage.")
	}

	return cmdDaemon(c)
}

func cmdCallHook(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("Invalid arguments")
	}

	path := args[0]
	id := args[1]
	state := args[2]
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

func cmdDaemon(c *cli.Context) error {
	if c.String("cpuprofile") != "" {
		f, err := os.Create(c.String("cpuprofile"))
		if err != nil {
			fmt.Printf("Error opening cpu profile file: %s\n", err)
			return nil
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if c.String("memprofile") != "" {
		go memProfiler(c.String("memprofile"))
	}

	neededPrograms := []string{"setfacl", "rsync", "tar", "xz"}
	for _, p := range neededPrograms {
		_, err := exec.LookPath(p)
		if err != nil {
			return err
		}
	}

	if c.Int("print-goroutines-every") > 0 {
		go func() {
			for {
				time.Sleep(time.Duration(c.Int("print-goroutines-every")) * time.Second)
				shared.PrintStack()
			}
		}()
	}

	d := &Daemon{
		group:     c.String("group"),
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

		shared.Log.Info(fmt.Sprintf("Received '%s signal', exiting.", sig))
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

func cmdShutdown(context *cli.Context) error {
	timeout := context.Int("timeout")

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
		return fmt.Errorf("LXD still running after %ds timeout", timeout)
	}

	return nil
}

func cmdActivateIfNeeded() error {
	// Don't start a full daemon, we just need DB access
	d := &Daemon{
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

		if lastState == "RUNNING" || lastState == "Running" || autoStart == "true" {
			shared.Debugf("Daemon has auto-started containers, activating...")
			_, err := lxd.NewClient(&lxd.DefaultConfig, "local")
			return err
		}
	}

	shared.Debugf("No need to start the daemon now.")
	return nil
}

func cmdWaitReady(context *cli.Context) error {
	timeout := context.Int("timeout")

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
		return fmt.Errorf("LXD still not running after %ds timeout", timeout)
	}

	return nil
}

func cmdInit(context *cli.Context) error {
	var storageBackend string // dir or zfs
	var storageMode string    // existing, loop or device
	var storageLoopSize int   // Size in GB
	var storageDevice string  // Path
	var storagePool string    // pool name
	var networkAddress string // Address
	var networkPort int       // Port
	var trustPassword string  // Trust password

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

	if context.Bool("auto") {
		// Do a bunch of sanity checks
		if !shared.StringInSlice(context.String("storage-backend"), backendsSupported) {
			return fmt.Errorf("The requested backend '%s' isn't supported by lxd init.", context.String("storage-backend"))
		}

		if !shared.StringInSlice(context.String("storage-backend"), backendsAvailable) {
			return fmt.Errorf("The requested backend '%s' isn't available on your system (missing tools).", context.String("storage-backend"))
		}

		if context.String("storage-backend") == "dir" {
			if context.Int("storage-create-loop") != -1 || context.String("storage-create-device") != "" || context.String("storage-pool") != "" {
				return fmt.Errorf("None of --storage-pool, --storage-create-device or --storage-create-pool may be used with the 'dir' backend.")
			}
		}

		if context.String("storage-backend") == "zfs" {
			if context.Int("storage-create-loop") != -1 && context.String("storage-create-device") != "" {
				return fmt.Errorf("Only one of --storage-create-device or --storage-create-pool can be specified with the 'zfs' backend.")
			}

			if context.String("storage-pool") == "" {
				return fmt.Errorf("--storage-pool must be specified with the 'zfs' backend")
			}
		}

		if context.String("network-address") == "" {
			if context.Int("network-port") != -1 {
				return fmt.Errorf("--network-port cannot be used without --network-address")
			}
			if context.String("trust-password") != "" {
				return fmt.Errorf("--trust-password cannot be used without --network-address")
			}
		}

		// Set the local variables
		if context.String("storage-create-device") != "" {
			storageMode = "device"
		} else if context.Int("storage-create-loop") != -1 {
			storageMode = "loop"
		} else {
			storageMode = "existing"
		}

		storageBackend = context.String("storage-backend")
		storageLoopSize = context.Int("storage-create-loop")
		storageDevice = context.String("storage-create-device")
		storagePool = context.String("storage-pool")
		networkAddress = context.String("network-address")
		networkPort = context.Int("network-port")
		trustPassword = context.String("trust-password")
	} else {
		if context.String("storage-backend") != "" ||
			context.String("storage-create-device") != "" ||
			context.Int("storage-create-loop") != -1 ||
			context.String("storage-pool") != "" ||
			context.String("network-address") != "" ||
			context.Int("network-port") != -1 ||
			context.String("trust-password") != "" {

			return fmt.Errorf("Init configuration is only valid with --auto")
		}

		storageBackend = askChoice("Name of the storage backend to use (dir or zfs): ", backendsSupported)

		if !shared.StringInSlice(storageBackend, backendsSupported) {
			return fmt.Errorf("The requested backend '%s' isn't supported by lxd init.", storageBackend)
		}

		if !shared.StringInSlice(storageBackend, backendsAvailable) {
			return fmt.Errorf("The requested backend '%s' isn't available on your system (missing tools).", storageBackend)
		}

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
			networkAddress = askString("Address to bind LXD to (not including port): ")
			networkPort = askInt("Port to bind LXD to (8443 recommended): ", 1, 65535)
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
		_ = exec.Command("modprobe", "zfs").Run()

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
				"-f", "-m", "none").CombinedOutput()
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
