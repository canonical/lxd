package apparmor

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
)

// Internal copy of the network interface.
type network interface {
	Name() string
}

// DnsmasqProfileName returns the AppArmor profile name.
func DnsmasqProfileName(n network) string {
	path := shared.VarPath("")
	name := fmt.Sprintf("%s_<%s>", n.Name(), path)

	// Max length in AppArmor is 253 chars.
	if len(name)+12 >= 253 {
		hash := sha256.New()
		io.WriteString(hash, name)
		name = fmt.Sprintf("%x", hash.Sum(nil))
	}

	return fmt.Sprintf("lxd_dnsmasq-%s", name)
}

// dnsmasqProfileFilename returns the name of the on-disk profile name.
func dnsmasqProfileFilename(n network) string {
	name := n.Name()

	// Max length in AppArmor is 253 chars.
	if len(name)+12 >= 253 {
		hash := sha256.New()
		io.WriteString(hash, name)
		name = fmt.Sprintf("%x", hash.Sum(nil))
	}

	return fmt.Sprintf("lxd_dnsmasq-%s", name)
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

// dnsmasqProfile generates the AppArmor profile template from the given network.
func dnsmasqProfile(state *state.State, n network) (string, error) {
	rootPath := ""
	if shared.InSnap() {
		rootPath = "/var/lib/snapd/hostfs"
	}

	// Render the profile.
	var sb *strings.Builder = &strings.Builder{}
	err := dnsmasqProfileTpl.Execute(sb, map[string]interface{}{
		"name":        DnsmasqProfileName(n),
		"networkName": n.Name(),
		"varPath":     shared.VarPath(""),
		"rootPath":    rootPath,
		"snap":        shared.InSnap(),
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}
