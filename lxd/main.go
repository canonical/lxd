package main

import (
	"math/rand"
	"os"
	"time"

	"github.com/lxc/lxd/shared/cmd"
)

// Global variables
var debug bool
var verbose bool

func init() {
	rand.Seed(time.Now().UTC().UnixNano())
}

func main() {
	// Pupulate a new Args instance by parsing the command line arguments
	// passed.
	context := cmd.DefaultContext()
	args := &Args{}
	parser := cmd.NewParser(context, usage)
	parser.Parse(os.Args, args)

	// Set the global variables
	debug = args.Debug
	verbose = args.Verbose

	// Process sub-commands
	subcommand := cmdDaemon // default sub-command if none is specified
	if args.Subcommand != "" {
		subcommand, _ = subcommands[args.Subcommand]
	}
	if subcommand == nil {
		context.Output(usage)
		context.Error("error: Unknown arguments\n")
		os.Exit(1)
	}

	os.Exit(RunSubCommand(subcommand, context, args, eventsHandler{}))
}

// Index of SubCommand functions by command line name
//
// "forkputfile", "forkgetfile", "forkmount" and "forkumount" are handled specially in main_nsexec.go
// "forkgetnet" is partially handled in nsexec.go (setns)
var subcommands = map[string]SubCommand{
	// Main commands
	"activateifneeded": cmdActivateIfNeeded,
	"daemon":           cmdDaemon,
	"callhook":         cmdCallHook,
	"init":             cmdInit,
	"ready":            cmdReady,
	"shutdown":         cmdShutdown,
	"waitready":        cmdWaitReady,
	"import":           cmdImport,

	// Internal commands
	"forkconsole":        cmdForkConsole,
	"forkgetnet":         cmdForkGetNet,
	"forkmigrate":        cmdForkMigrate,
	"forkstart":          cmdForkStart,
	"forkexec":           cmdForkExec,
	"netcat":             cmdNetcat,
	"migratedumpsuccess": cmdMigrateDumpSuccess,
}
