package main

import (
	"fmt"

	log "github.com/lxc/lxd/shared/log15"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
	"github.com/lxc/lxd/shared/version"
)

// SubCommand is function that performs the logic of a specific LXD sub-command.
type SubCommand func(*Args) error

// SubCommandError implements the error interface and also carries with it an integer
// exit code. If a Command returns an error of this kind, it will use its code
// as exit status.
type SubCommandError struct {
	Code    int
	Message string
}

func (e *SubCommandError) Error() string {
	return e.Message
}

// SubCommandErrorf returns a new SubCommandError with the given code and the
// given message (formatted with fmt.Sprintf).
func SubCommandErrorf(code int, format string, a ...interface{}) *SubCommandError {
	return &SubCommandError{
		Code:    code,
		Message: fmt.Sprintf(format, a...),
	}
}

// RunSubCommand is the main entry point for all LXD subcommands, performing
// common setup logic before firing up the subcommand.
//
// The ctx parameter provides input/output streams and related utilities, the
// args one contains command line parameters, and handler is an additional
// custom handler which will be added to the configured logger, along with the
// default one (stderr) and the ones possibly installed by command line
// arguments (via args.Syslog and args.Logfile).
func RunSubCommand(command SubCommand, ctx *cmd.Context, args *Args, handler log.Handler) int {
	// In case of --help or --version we just print the relevant output and
	// return immediately
	if args.Help {
		ctx.Output(usage)
		return 0
	}
	if args.Version {
		ctx.Output("%s\n", version.Version)
		return 0
	}

	// Run the setup code and, if successful, the command.
	err := setupSubCommand(ctx, args, handler)
	if err == nil {
		err = command(args)
	}
	if err != nil {
		code := 1
		message := err.Error()
		subCommandError, ok := err.(*SubCommandError)
		if ok {
			code = subCommandError.Code
		}
		if message != "" {
			// FIXME: with Go 1.6, go vet complains if we just write
			//        this as ctx.Error("error: %s\n", message), while
			//        with Go > 1.6 it'd be fine.
			ctx.Error(fmt.Sprintf("error: %s\n", message))
		}
		return code
	}
	return 0
}

// Setup logic common across all LXD subcommands.
func setupSubCommand(context *cmd.Context, args *Args, handler log.Handler) error {
	// Check if LXD_DIR is valid.
	if len(shared.VarPath("unix.sock")) > 107 {
		return fmt.Errorf("LXD_DIR is too long, must be < %d", 107-len("unix.sock"))
	}

	// Configure logging.
	syslog := ""
	if args.Syslog {
		syslog = "lxd"
	}

	var err error
	logger.Log, err = logging.GetLogger(syslog, args.Logfile, args.Verbose, args.Debug, handler)
	if err != nil {
		context.Output("%v\n", err)
		return err
	}

	return nil
}
