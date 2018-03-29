package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/lxc/lxd/lxc/config"
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

func expandAlias(conf *config.Config, origArgs []string) ([]string, bool) {
	aliasKey, aliasValue, foundAlias := findAlias(conf.Aliases, origArgs)
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

func execIfAliases(conf *config.Config, origArgs []string) {
	newArgs, expanded := expandAlias(conf, origArgs)
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
