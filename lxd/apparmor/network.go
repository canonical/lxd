package apparmor

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/lxc/lxd/lxd/state"
)

// Internal copy of the network interface.
type network interface {
	Name() string
}

// NetworkLoad ensures that the network's profiles are loaded into the kernel.
func NetworkLoad(state *state.State, n network) error {
	/* In order to avoid forcing a profile parse (potentially slow) on
	 * every network start, let's use AppArmor's binary policy cache,
	 * which checks mtime of the files to figure out if the policy needs to
	 * be regenerated.
	 *
	 * Since it uses mtimes, we shouldn't just always write out our local
	 * AppArmor template; instead we should check to see whether the
	 * template is the same as ours. If it isn't we should write our
	 * version out so that the new changes are reflected and we definitely
	 * force a recompile.
	 */
	profile := filepath.Join(aaPath, "profiles", dnsmasqProfileFilename(n))
	content, err := ioutil.ReadFile(profile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	updated, err := dnsmasqProfile(state, n)
	if err != nil {
		return err
	}

	if string(content) != string(updated) {
		err = ioutil.WriteFile(profile, []byte(updated), 0600)
		if err != nil {
			return err
		}
	}

	err = loadProfile(state, dnsmasqProfileFilename(n))
	if err != nil {
		return err
	}

	return nil
}

// NetworkUnload ensures that the network's profiles are unloaded to free kernel memory.
// This does not delete the policy from disk or cache.
func NetworkUnload(state *state.State, n network) error {
	err := unloadProfile(state, dnsmasqProfileFilename(n))
	if err != nil {
		return err
	}

	return nil
}

// NetworkDelete removes the profiles from cache/disk.
func NetworkDelete(state *state.State, n network) error {
	return deleteProfile(state, dnsmasqProfileFilename(n))
}
