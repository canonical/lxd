package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"strings"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
)

// defaultAliases contains LXC's built-in command line aliases.  The built-in
// aliases are checked only if no user-defined alias was found.
var defaultAliases = map[string]string{
	"shell": "exec @ARGS@ -- su -l",
}

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

func expandAlias(conf *config.Config, args []string) ([]string, bool) {
	var newArgs []string
	var origArgs []string

	for _, arg := range args[1:] {
		if arg[0] != '-' {
			break
		}

		newArgs = append(newArgs, arg)
	}

	origArgs = append([]string{args[0]}, args[len(newArgs)+1:]...)

	aliasKey, aliasValue, foundAlias := findAlias(conf.Aliases, origArgs)
	if !foundAlias {
		aliasKey, aliasValue, foundAlias = findAlias(defaultAliases, origArgs)
		if !foundAlias {
			return []string{}, false
		}
	}

	if !strings.HasPrefix(aliasValue[0], "/") {
		newArgs = append([]string{origArgs[0]}, newArgs...)
	}
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
		// Add the rest of the arguments
		newArgs = append(newArgs, origArgs[len(aliasKey)+1:]...)
	}

	return newArgs, true
}

func execIfAliases() error {
	args := os.Args

	// Avoid loops
	if os.Getenv("LXC_ALIASES") == "1" {
		return nil
	}

	// Figure out the config directory and config path
	var configDir string
	if os.Getenv("LXD_CONF") != "" {
		configDir = os.Getenv("LXD_CONF")
	} else if os.Getenv("HOME") != "" {
		configDir = path.Join(os.Getenv("HOME"), ".config", "lxc")
	} else {
		user, err := user.Current()
		if err != nil {
			return nil
		}

		configDir = path.Join(user.HomeDir, ".config", "lxc")
	}

	confPath := os.ExpandEnv(path.Join(configDir, "config.yml"))

	// Load the configuration
	var conf *config.Config
	var err error
	if shared.PathExists(confPath) {
		conf, err = config.LoadConfig(confPath)
		if err != nil {
			return nil
		}
	} else {
		conf = config.NewConfig(filepath.Dir(confPath), true)
	}

	// Expand the aliases
	newArgs, expanded := expandAlias(conf, args)
	if !expanded {
		return nil
	}

	// Look for the executable
	path, err := exec.LookPath(newArgs[0])
	if err != nil {
		return fmt.Errorf(i18n.G("Processing aliases failed: %s"), err)
	}

	// Re-exec
	environ := getEnviron()
	environ = append(environ, "LXC_ALIASES=1")
	ret := doExec(path, newArgs, environ)
	return fmt.Errorf(i18n.G("Processing aliases failed: %s"), ret)
}
