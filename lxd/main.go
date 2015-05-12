package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/lxc/lxd/internal/gnuflag"
	"github.com/lxc/lxd/shared"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

var verbose = gnuflag.Bool("v", false, "Enables verbose mode.")
var debug = gnuflag.Bool("debug", false, "Enables debug mode.")
var listenAddr = gnuflag.String("tcp", "", "TCP address <addr:port> to listen on in addition to the unix socket (e.g., 127.0.0.1:8443)")
var group = gnuflag.String("group", "", "Group which owns the shared socket")
var help = gnuflag.Bool("help", false, "Print this help message.")
var version = gnuflag.Bool("version", false, "Print LXD's version number and exit.")
var printGoroutines = gnuflag.Int("print-goroutines-every", -1, "For debugging, print a complete stack trace every n seconds")
var cpuProfile = gnuflag.String("cpuprofile", "", "Enable cpu profiling into the specified file.")
var memProfile = gnuflag.String("memprofile", "", "Enable memory profiling into the specified file.")

func init() {
	myGroup, err := shared.GroupName(os.Getgid())
	if err != nil {
		shared.Debugf("Problem finding default group %s", err)
	}
	*group = myGroup

	rand.Seed(time.Now().UTC().UnixNano())
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "forkstart" {
		return startContainer(os.Args[1:])
	}

	gnuflag.Usage = func() {
		fmt.Printf("Usage: lxd [options]\n\nOptions:\n")
		gnuflag.PrintDefaults()
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

	if *verbose || *debug {
		shared.SetLogger(log.New(os.Stderr, "", log.LstdFlags))
		shared.SetDebug(*debug)
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

	needed_programs := []string{"setfacl", "rsync", "tar", "xz"}
	for _, p := range needed_programs {
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

	d, err := StartDaemon(*listenAddr)

	if err != nil {
		if d != nil && d.db != nil {
			d.db.Close()
		}
		return err
	}

	defer d.db.Close()

	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGINT)
	signal.Notify(ch, syscall.SIGTERM)
	<-ch
	return d.Stop()
}
