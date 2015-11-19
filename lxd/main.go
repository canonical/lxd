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

var cpuProfile = gnuflag.String("cpuprofile", "", "Enable cpu profiling into the specified file.")
var debug = gnuflag.Bool("debug", false, "Enables debug mode.")
var group = gnuflag.String("group", "", "Group which owns the shared socket.")
var help = gnuflag.Bool("help", false, "Print this help message.")
var logfile = gnuflag.String("logfile", "", "Logfile to log to (e.g., /var/log/lxd/lxd.log).")
var memProfile = gnuflag.String("memprofile", "", "Enable memory profiling into the specified file.")
var printGoroutines = gnuflag.Int("print-goroutines-every", -1, "For debugging, print a complete stack trace every n seconds")
var syslogFlag = gnuflag.Bool("syslog", false, "Enables syslog logging.")
var verbose = gnuflag.Bool("verbose", false, "Enables verbose mode.")
var version = gnuflag.Bool("version", false, "Print LXD's version number and exit.")

func init() {
	myGroup, err := shared.GroupName(os.Getgid())
	if err != nil {
		shared.Debugf("Problem finding default group %s", err)
	}
	*group = myGroup

	rand.Seed(time.Now().UTC().UnixNano())
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	gnuflag.Usage = func() {
		fmt.Printf("Usage: lxd [command] [options]\n\nOptions:\n")
		gnuflag.PrintDefaults()

		fmt.Printf("\nCommands:\n")
		fmt.Printf("    activateifneeded\n")
		fmt.Printf("        Check if LXD should be started (at boot) and if so, spawns it through socket activation\n")
		fmt.Printf("    shutdown\n")
		fmt.Printf("        Perform a clean shutdown of LXD and all running containers\n")

		fmt.Printf("\nInternal commands (don't call those directly):\n")
		fmt.Printf("    forkgetfile\n")
		fmt.Printf("        Grab a file from a running container\n")
		fmt.Printf("    forkmigrate\n")
		fmt.Printf("        Restore a container after migration\n")
		fmt.Printf("    forkputfile\n")
		fmt.Printf("        Push a file to a running container\n")
		fmt.Printf("    forkstart\n")
		fmt.Printf("        Start a container\n")
	}

	gnuflag.Parse(true)
	if *help {
		// The user asked for help via --help, so we shouldn't print to
		// stderr.
		gnuflag.SetOut(os.Stdout)
		gnuflag.Usage()
		return nil
	}

	if *version {
		fmt.Println(shared.Version)
		return nil
	}

	if len(shared.VarPath("unix.sock")) > 107 {
		return fmt.Errorf("LXD_DIR is too long, must be < %d", 107-len("unix.sock"))
	}

	// Configure logging
	syslog := ""
	if *syslogFlag {
		syslog = "lxd"
	}

	handler := eventsHandler{}

	err := shared.SetLogger(syslog, *logfile, *verbose, *debug, handler)
	if err != nil {
		fmt.Printf("%s", err)
		return nil
	}

	// Process sub-commands
	if len(os.Args) > 1 {
		// "forkputfile" and "forkgetfile" are handled specially in copyfile.go
		switch os.Args[1] {
		case "forkstart":
			return startContainer(os.Args[1:])
		case "forkmigrate":
			return MigrateContainer(os.Args[1:])
		case "shutdown":
			return cleanShutdown()
		case "activateifneeded":
			return activateIfNeeded()
		}
	}

	if gnuflag.NArg() != 0 {
		gnuflag.Usage()
		return fmt.Errorf("Unknown arguments")
	}

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			fmt.Printf("Error opening cpu profile file: %s\n", err)
			return nil
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if *memProfile != "" {
		go memProfiler()
	}

	neededPrograms := []string{"setfacl", "rsync", "tar", "xz"}
	for _, p := range neededPrograms {
		_, err := exec.LookPath(p)
		if err != nil {
			return err
		}
	}

	if *printGoroutines > 0 {
		go func() {
			for {
				time.Sleep(time.Duration(*printGoroutines) * time.Second)
				shared.PrintStack()
			}
		}()
	}

	d, err := startDaemon()

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

	// This should be replaced with a connection to /1.0/events once the
	// events websocket is implemented as the polling loop is expensive.
	timeout := 60 * 1e9
	for timeout > 0 {
		timeout -= 500 * 1e6

		err := c.Finger()
		if err != nil {
			return nil
		}

		time.Sleep(500 * 1e6 * time.Nanosecond)
	}

	return fmt.Errorf("LXD still running after 60s timeout.")
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
