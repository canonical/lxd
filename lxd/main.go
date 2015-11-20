package main

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"runtime/pprof"
	"sync"
	"syscall"
	"time"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
)

// Global arguments
var argCPUProfile = gnuflag.String("cpuprofile", "", "")
var argDebug = gnuflag.Bool("debug", false, "")
var argGroup = gnuflag.String("group", "", "")
var argHelp = gnuflag.Bool("help", false, "")
var argLogfile = gnuflag.String("logfile", "", "")
var argMemProfile = gnuflag.String("memprofile", "", "")
var argPrintGoroutinesEvery = gnuflag.Int("print-goroutines-every", -1, "")
var argSyslog = gnuflag.Bool("syslog", false, "")
var argTimeout = gnuflag.Int("timeout", -1, "")
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
		fmt.Printf("    shutdown [--timeout=60]\n")
		fmt.Printf("        Perform a clean shutdown of LXD and all running containers\n")
		fmt.Printf("    waitready [--timeout=15]\n")
		fmt.Printf("        Wait until LXD is ready to handle requests\n")

		fmt.Printf("\n\nCommon options:\n")
		fmt.Printf("    --debug\n")
		fmt.Printf("        Enables debug mode.\n")
		fmt.Printf("    --help\n")
		fmt.Printf("        Print this help message.\n")
		fmt.Printf("    --logfile FILE\n")
		fmt.Printf("        Logfile to log to (e.g., /var/log/lxd/lxd.log).\n")
		fmt.Printf("    --syslog\n")
		fmt.Printf("        Enables syslog logging.\n")
		fmt.Printf("    --verbose\n")
		fmt.Printf("        Enables verbose mode.\n")
		fmt.Printf("    --version\n")
		fmt.Printf("        Print LXD's version number and exit.\n")

		fmt.Printf("\nDaemon options:\n")
		fmt.Printf("    --group GROUP\n")
		fmt.Printf("        Group which owns the shared socket.\n")

		fmt.Printf("\nDaemon debug options:\n")
		fmt.Printf("    --cpuprofile FILE\n")
		fmt.Printf("        Enable cpu profiling into the specified file.\n")
		fmt.Printf("    --memprofile FILE\n")
		fmt.Printf("        Enable memory profiling into the specified file.\n")
		fmt.Printf("    --print-goroutines-every SECONDS\n")
		fmt.Printf("        For debugging, print a complete stack trace every n seconds.\n")

		fmt.Printf("\nShutdown options:\n")
		fmt.Printf("    --timeout SECONDS\n")
		fmt.Printf("        How long to wait before failing.\n")

		fmt.Printf("\nWaitready options:\n")
		fmt.Printf("    --timeout SECONDS\n")
		fmt.Printf("        How long to wait before failing.\n")

		fmt.Printf("\n\nInternal commands (don't call these directly):\n")
		fmt.Printf("    forkgetfile\n")
		fmt.Printf("        Grab a file from a running container\n")
		fmt.Printf("    forkmigrate\n")
		fmt.Printf("        Restore a container after migration\n")
		fmt.Printf("    forkputfile\n")
		fmt.Printf("        Push a file to a running container\n")
		fmt.Printf("    forkstart\n")
		fmt.Printf("        Start a container\n")
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
	err := shared.SetLogger(syslog, *argLogfile, *argVerbose, *argDebug, handler)
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

	serverStatus, err := c.ServerStatus()
	if err != nil {
		return err
	}

	pid := serverStatus.Environment.ServerPid
	if pid < 1 {
		return fmt.Errorf("Invalid server PID: %d", pid)
	}

	err = syscall.Kill(pid, syscall.SIGPWR)
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
				time.Sleep(500 * 1e6 * time.Nanosecond)
				continue
			}

			err = c.Finger()
			if err != nil {
				time.Sleep(500 * 1e6 * time.Nanosecond)
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
