package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
	"github.com/lxc/lxd/shared/version"
)

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
	// Pupulate a new Args instance by parsing the command line arguments
	// passed.
	context := cmd.DefaultContext()
	args := &Args{}
	parser := cmd.NewParser(context, usage)
	parser.Parse(os.Args, args)

	// Set the global variables
	debug = args.Debug
	verbose = args.Verbose

	if args.Help {
		context.Output(usage)
		return nil
	}

	// Deal with --version right here
	if args.Version {
		fmt.Println(version.Version)
		return nil
	}

	if len(shared.VarPath("unix.sock")) > 107 {
		return fmt.Errorf("LXD_DIR is too long, must be < %d", 107-len("unix.sock"))
	}

	// Configure logging
	syslog := ""
	if args.Syslog {
		syslog = "lxd"
	}

	handler := eventsHandler{}

	var err error
	logger.Log, err = logging.GetLogger(syslog, args.Logfile, args.Verbose, args.Debug, handler)
	if err != nil {
		fmt.Printf("%s\n", err)
		return nil
	}

	// Process sub-commands
	if args.Subcommand != "" {
		// "forkputfile", "forkgetfile", "forkmount" and "forkumount" are handled specially in main_nsexec.go
		// "forkgetnet" is partially handled in nsexec.go (setns)
		switch args.Subcommand {
		// Main commands
		case "activateifneeded":
			return cmdActivateIfNeeded()
		case "daemon":
			return cmdDaemon(args)
		case "callhook":
			return cmdCallHook(args)
		case "init":
			return cmdInit(args)
		case "ready":
			return cmdReady()
		case "shutdown":
			return cmdShutdown(args)
		case "waitready":
			return cmdWaitReady(args)
		// Internal commands
		case "forkgetnet":
			return cmdForkGetNet()
		case "forkmigrate":
			return cmdForkMigrate(args)
		case "forkstart":
			return cmdForkStart(args)
		case "forkexec":
			ret, err := cmdForkExec(args)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			}
			os.Exit(ret)
		case "netcat":
			return cmdNetcat(args)
		case "migratedumpsuccess":
			return cmdMigrateDumpSuccess(args)
		}
	} else {
		return cmdDaemon(args) // Default subcommand
	}

	context.Output(usage)
	return fmt.Errorf("Unknown arguments")
}
