package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"
	"syscall"

	"github.com/codegangsta/cli"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/logging"
)

var errArgs = fmt.Errorf(i18n.G("wrong number of subcommand arguments"))

// Gets set in commandGetConfig
var commandConfigPath string

var commandGlobalFlags = []cli.Flag{
	cli.StringFlag{
		Name:   "directory",
		Usage:  i18n.G("Path to an alternate server directory."),
		EnvVar: "LXD_DIR",
	},

	// BoolFlag is a switch that defaults to false
	cli.BoolFlag{
		Name:  "debug",
		Usage: i18n.G("Print debug information."),
	},

	cli.BoolFlag{
		Name:  "verbose",
		Usage: i18n.G("Print verbose information."),
	},

	cli.BoolFlag{
		Name:  "no-alias",
		Usage: i18n.G("Magic flag to run lxc without alias support."),
	},

	cli.BoolFlag{
		Name:  "force-local",
		Usage: i18n.G("Force using the local unix socket."),
	},
}

func main() {
	app := cli.NewApp()
	app.Name = "lxc"
	app.Version = shared.Version
	app.Usage = "LXD is pronounced lex-dee."
	app.Flags = commandGlobalFlagsWrapper(cli.StringFlag{
		Name:   "config",
		Usage:  i18n.G("Alternate config directory."),
		EnvVar: "LXD_CONF",
	})
	app.Commands = []cli.Command{
		commandConfig,
		commandCopy,
		commandDelete,
		commandExec,
		commandFile,
		commandFinger,
		commandImage,
		commandInfo,
		commandInit,
		commandLaunch,
		commandList,
		commandMonitor,
		commandMove,
		commandProfile,
		commandPublish,
		commandRemote,
		commandRestart,
		commandRestore,
		commandSnapshot,
		commandStart,
		commandStop,

		cli.Command{
			Name:      "version",
			Usage:     i18n.G("Prints the version number of LXD."),
			ArgsUsage: i18n.G(""),

			Action: commandActionVersion,
		},
	}

	app.Run(os.Args)
}

func commandGlobalFlagsWrapper(flags ...cli.Flag) []cli.Flag {
	return append(commandGlobalFlags, flags...)
}

func commandWrapper(callable func(*lxd.Config, *cli.Context) error) func(*cli.Context) {
	return func(c *cli.Context) {
		var err error

		// LXD_DIR
		var lxddir = c.String("directory")
		if lxddir == "" {
			lxddir = c.GlobalString("directory")
		}
		if lxddir != "" {
			os.Setenv("LXD_DIR", lxddir)
		}

		// Config
		var configDir = "$HOME/.config/lxc"
		if c.GlobalString("config") != "" {
			configDir = c.GlobalString("config")
		}
		commandConfigPath = os.ExpandEnv(path.Join(configDir, "config.yml"))

		var config *lxd.Config
		var forceLocal = false
		if c.GlobalBool("force-local") || c.Bool("force-local") {
			forceLocal = true
		}
		if forceLocal {
			config = &lxd.DefaultConfig
		} else {
			config, err = lxd.LoadConfig(commandConfigPath)
			if err != nil {
				commandExitError(err)
			}

			// One time migration from old config
			if config.DefaultRemote == "" {
				_, ok := config.Remotes["local"]
				if !ok {
					config.Remotes["local"] = lxd.LocalRemote
				}
				config.DefaultRemote = "local"
				lxd.SaveConfig(config, commandConfigPath)
			}
		}

		// Handle command aliases
		var noAlias = false
		if c.GlobalBool("no-alias") || c.Bool("no-alias") {
			noAlias = true
		}
		if !noAlias {
			// syscall.Exec replaces that process.
			commandExecIfAliases(config, os.Args)
		}

		// Configure logging
		var verbose = false
		if c.GlobalBool("verbose") || c.Bool("verbose") {
			verbose = true
		}

		var debug = false
		if c.GlobalBool("debug") || c.Bool("debug") {
			debug = true
		}

		shared.Log, err = logging.GetLogger("", "", verbose, debug, nil)
		if err != nil {
			commandExitError(err)
		}

		// Create a client cert on first start.
		certf := config.ConfigPath("client.crt")
		keyf := config.ConfigPath("client.key")

		if !forceLocal && (!shared.PathExists(certf) || !shared.PathExists(keyf)) {
			fmt.Fprintf(os.Stderr, i18n.G("Generating a client certificate. This may take a minute...")+"\n")

			err = shared.FindOrGenCert(certf, keyf)
			if err != nil {
				commandExitError(err)
			}
		}

		commandExitError(callable(config, c))
	}
}

func commandExitError(err error) {
	if err != nil {
		// The action we take depends on the error we get.
		msg := fmt.Sprintf(i18n.G("error: %v"), err)
		switch t := err.(type) {
		case *url.Error:
			switch u := t.Err.(type) {
			case *net.OpError:
				if u.Op == "dial" && u.Net == "unix" {
					switch errno := u.Err.(type) {
					case syscall.Errno:
						switch errno {
						case syscall.ENOENT:
							msg = i18n.G("LXD socket not found; is LXD running?")
						case syscall.ECONNREFUSED:
							msg = i18n.G("Connection refused; is LXD running?")
						case syscall.EACCES:
							msg = i18n.G("Permisson denied, are you in the lxd group?")
						default:
							msg = fmt.Sprintf("%d %s", uintptr(errno), errno.Error())
						}
					}
				}
			}
		}

		fmt.Fprintln(os.Stderr, fmt.Sprintf("%s", msg))
		os.Exit(1)
	}
}

func commandActionVersion(c *cli.Context) {
	println(shared.Version)
}

func commandExpandAlias(config *lxd.Config, origArgs []string) ([]string, bool) {
	foundAlias := false
	aliasKey := []string{}
	aliasValue := []string{}

	for k, v := range config.Aliases {
		matches := false
		for i, key := range strings.Split(k, " ") {
			if len(origArgs) <= i+1 {
				break
			}

			if origArgs[i+1] == key {
				matches = true
				aliasKey = strings.Split(k, " ")
				aliasValue = strings.Split(v, " ")
				break
			}
		}

		if !matches {
			continue
		}

		foundAlias = true
		break
	}

	if !foundAlias {
		return []string{}, false
	}

	newArgs := []string{origArgs[0]}
	hasReplacedArgsVar := false

	for i, aliasArg := range aliasValue {
		if aliasArg == "@ARGS@" && len(origArgs) > i {
			newArgs = append(newArgs, origArgs[i+1:]...)
			hasReplacedArgsVar = true
		} else {
			newArgs = append(newArgs, aliasArg)
		}
	}

	if !hasReplacedArgsVar {
		/* add the rest of the arguments */
		newArgs = append(newArgs, origArgs[len(aliasKey)+1:]...)
	}

	/* don't re-do aliases the next time; this allows us to have recursive
	 * aliases, e.g. `lxc list` to `lxc list -c n`
	 */
	newArgs = append(newArgs[:2], append([]string{"--no-alias"}, newArgs[2:]...)...)

	return newArgs, true
}

func commandExecIfAliases(config *lxd.Config, origArgs []string) {
	newArgs, expanded := commandExpandAlias(config, origArgs)
	if !expanded {
		return
	}

	path, err := exec.LookPath(origArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, i18n.G("processing aliases failed %s\n"), err)
		os.Exit(5)
	}
	ret := syscall.Exec(path, newArgs, syscall.Environ())
	fmt.Fprintf(os.Stderr, i18n.G("processing aliases failed %s\n"), ret)
	os.Exit(5)
}
