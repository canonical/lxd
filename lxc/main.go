package main

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"syscall"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/logging"
)

var configPath string

func main() {
	if err := run(); err != nil {
		msg := fmt.Sprintf(i18n.G("error: %v"), err)

		lxdErr := lxd.GetLocalLXDErr(err)
		switch lxdErr {
		case syscall.ENOENT:
			msg = i18n.G("LXD socket not found; is LXD installed and running?")
		case syscall.ECONNREFUSED:
			msg = i18n.G("Connection refused; is LXD running?")
		case syscall.EACCES:
			msg = i18n.G("Permission denied, are you in the lxd group?")
		}

		fmt.Fprintln(os.Stderr, fmt.Sprintf("%s", msg))
		os.Exit(1)
	}
}

func run() error {
	verbose := gnuflag.Bool("verbose", false, i18n.G("Enable verbose mode"))
	debug := gnuflag.Bool("debug", false, i18n.G("Enable debug mode"))
	forceLocal := gnuflag.Bool("force-local", false, i18n.G("Force using the local unix socket"))
	noAlias := gnuflag.Bool("no-alias", false, i18n.G("Ignore aliases when determining what command to run"))

	configDir := "$HOME/.config/lxc"
	if os.Getenv("LXD_CONF") != "" {
		configDir = os.Getenv("LXD_CONF")
	}
	configPath = os.ExpandEnv(path.Join(configDir, "config.yml"))

	if len(os.Args) >= 3 && os.Args[1] == "config" && os.Args[2] == "profile" {
		fmt.Fprintf(os.Stderr, i18n.G("`lxc config profile` is deprecated, please use `lxc profile`")+"\n")
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}

	if len(os.Args) >= 2 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		os.Args[1] = "help"
	}

	if len(os.Args) >= 2 && (os.Args[1] == "--all") {
		os.Args[1] = "help"
		os.Args = append(os.Args, "--all")
	}

	if len(os.Args) == 2 && os.Args[1] == "--version" {
		os.Args[1] = "version"
	}

	if len(os.Args) == 2 && os.Args[1] == "--man" {
		os.Args[1] = "manpage"
	}

	if len(os.Args) < 2 {
		commands["help"].run(nil, nil)
		os.Exit(1)
	}

	var config *lxd.Config
	var err error

	if *forceLocal {
		config = &lxd.DefaultConfig
	} else {
		config, err = lxd.LoadConfig(configPath)
		if err != nil {
			return err
		}
	}

	// This is quite impolite, but it seems gnuflag needs us to shift our
	// own exename out of the arguments before parsing them. However, this
	// is useful for execIfAlias, which wants to know exactly the command
	// line we received, and in some cases is called before this shift, and
	// in others after. So, let's save the original args.
	origArgs := os.Args
	name := os.Args[1]

	/* at this point we haven't parsed the args, so we have to look for
	 * --no-alias by hand.
	 */
	if !shared.StringInSlice("--no-alias", origArgs) {
		execIfAliases(config, origArgs)
	}
	cmd, ok := commands[name]
	if !ok {
		commands["help"].run(nil, nil)
		fmt.Fprintf(os.Stderr, "\n"+i18n.G("error: unknown command: %s")+"\n", name)
		os.Exit(1)
	}
	cmd.flags()
	gnuflag.Usage = func() {
		fmt.Fprintf(os.Stderr, i18n.G("Usage: %s")+"\n\n"+i18n.G("Options:")+"\n\n", strings.TrimSpace(cmd.usage()))
		gnuflag.PrintDefaults()
	}

	os.Args = os.Args[1:]
	gnuflag.Parse(true)

	shared.Log, err = logging.GetLogger("", "", *verbose, *debug, nil)
	if err != nil {
		return err
	}

	// If the user is running a command that may attempt to connect to the local daemon
	// and this is the first time the client has been run by the user, then check to see
	// if LXD has been properly configured.  Don't display the message if the var path
	// does not exist (LXD not installed), as the user may be targeting a remote daemon.
	if os.Args[0] != "help" && os.Args[0] != "version" && shared.PathExists(shared.VarPath("")) && !shared.PathExists(config.ConfigDir) {

		// Create the config dir so that we don't get in here again for this user.
		err = os.MkdirAll(config.ConfigDir, 0750)
		if err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, i18n.G("If this is your first time using LXD, you should also run: sudo lxd init")+"\n")
		fmt.Fprintf(os.Stderr, i18n.G("To start your first container, try: lxc launch ubuntu:16.04")+"\n\n")
	}

	err = cmd.run(config, gnuflag.Args())
	if err == errArgs {
		/* If we got an error about invalid arguments, let's try to
		 * expand this as an alias
		 */
		if !*noAlias {
			execIfAliases(config, origArgs)
		}
		fmt.Fprintf(os.Stderr, "%s\n\n"+i18n.G("error: %v")+"\n", cmd.usage(), err)
		os.Exit(1)
	}
	return err
}

type command interface {
	usage() string
	flags()
	showByDefault() bool
	run(config *lxd.Config, args []string) error
}

var commands = map[string]command{
	"config":  &configCmd{},
	"copy":    &copyCmd{},
	"delete":  &deleteCmd{},
	"exec":    &execCmd{},
	"file":    &fileCmd{},
	"finger":  &fingerCmd{},
	"help":    &helpCmd{},
	"image":   &imageCmd{},
	"info":    &infoCmd{},
	"init":    &initCmd{},
	"launch":  &launchCmd{},
	"list":    &listCmd{},
	"manpage": &manpageCmd{},
	"monitor": &monitorCmd{},
	"move":    &moveCmd{},
	"network": &networkCmd{},
	"pause": &actionCmd{
		action:         shared.Freeze,
		name:           "pause",
		additionalHelp: i18n.G("The opposite of `lxc pause` is `lxc start`."),
	},
	"profile": &profileCmd{},
	"publish": &publishCmd{},
	"remote":  &remoteCmd{},
	"restart": &actionCmd{
		action:     shared.Restart,
		hasTimeout: true,
		visible:    true,
		name:       "restart",
		timeout:    -1,
	},
	"restore":  &restoreCmd{},
	"snapshot": &snapshotCmd{},
	"start": &actionCmd{
		action:  shared.Start,
		visible: true,
		name:    "start",
	},
	"stop": &actionCmd{
		action:     shared.Stop,
		hasTimeout: true,
		visible:    true,
		name:       "stop",
		timeout:    -1,
	},
	"storage": &storageCmd{},
	"version": &versionCmd{},
}

// defaultAliases contains LXC's built-in command line aliases.  The built-in
// aliases are checked only if no user-defined alias was found.
var defaultAliases = map[string]string{
	"shell": "exec @ARGS@ -- login -f root",

	"cp":     "copy",
	"ls":     "list",
	"mv":     "move",
	"rename": "move",
	"rm":     "delete",

	"image cp": "image copy",
	"image ls": "image list",
	"image rm": "image delete",

	"image alias ls": "image alias list",
	"image alias rm": "image alias delete",

	"remote ls": "remote list",
	"remote mv": "remote rename",
	"remote rm": "remote remove",

	"config device ls": "config device list",
	"config device rm": "config device remove",
}

var errArgs = fmt.Errorf(i18n.G("wrong number of subcommand arguments"))

func findAlias(aliases map[string]string, origArgs []string) ([]string, []string, bool) {
	foundAlias := false
	aliasKey := []string{}
	aliasValue := []string{}

	for k, v := range aliases {
		foundAlias = true
		for i, key := range strings.Split(k, " ") {
			if len(origArgs) <= i+1 || origArgs[i+1] != key {
				foundAlias = false
				break
			}
		}

		if foundAlias {
			aliasKey = strings.Split(k, " ")
			aliasValue = strings.Split(v, " ")
			break
		}
	}

	return aliasKey, aliasValue, foundAlias
}

func expandAlias(config *lxd.Config, origArgs []string) ([]string, bool) {
	aliasKey, aliasValue, foundAlias := findAlias(config.Aliases, origArgs)
	if !foundAlias {
		aliasKey, aliasValue, foundAlias = findAlias(defaultAliases, origArgs)
		if !foundAlias {
			return []string{}, false
		}
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

func execIfAliases(config *lxd.Config, origArgs []string) {
	newArgs, expanded := expandAlias(config, origArgs)
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

type ProgressRenderer struct {
	Format string

	maxLength int
}

func (p *ProgressRenderer) Done(msg string) {
	if msg != "" {
		msg += "\n"
	}

	if len(msg) > p.maxLength {
		p.maxLength = len(msg)
	} else {
		fmt.Printf("\r%s", strings.Repeat(" ", p.maxLength))
	}

	fmt.Print("\r")
	fmt.Print(msg)
}

func (p *ProgressRenderer) Update(status string) {
	msg := "%s"
	if p.Format != "" {
		msg = p.Format
	}

	msg = fmt.Sprintf("\r"+msg, status)

	if len(msg) > p.maxLength {
		p.maxLength = len(msg)
	} else {
		fmt.Printf("\r%s", strings.Repeat(" ", p.maxLength))
	}

	fmt.Print(msg)
}
