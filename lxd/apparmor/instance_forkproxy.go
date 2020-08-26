package apparmor

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
)

// Internal copy of the device interface.
type device interface {
	Name() string
}

var forkproxyProfileTpl = template.Must(template.New("forkproxyProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>

  # Capabilities
  capability dac_read_search,
  capability dac_override,
  capability kill,
  capability net_bind_service,
  capability sys_admin,
  capability sys_ptrace,

  # Network access
  network inet dgram,
  network inet6 dgram,

  # Forkproxy operation
  @{PROC}/** rw,
  / rw,
  ptrace (read),

  # Needed for lxd fork commands
  @{PROC}/@{pid}/cmdline r,
  {{ .rootPath }}/{etc,lib,usr/lib}/os-release r,

  # Things that we definitely don't need
  deny @{PROC}/@{pid}/cgroup r,
  deny /sys/module/apparmor/parameters/enabled r,
  deny /sys/kernel/mm/transparent_hugepage/hpage_pmd_size r,

{{- if .snap }}
  # The binary itself (for nesting)
  /var/snap/lxd/common/lxd.debug      mr,
  /snap/lxd/*/bin/lxd                 mr,

  # Snap-specific libraries
  /snap/lxd/*/lib/**.so*              mr,
{{- end }}

{{if .libraryPath -}}
  # Entries from LD_LIBRARY_PATH
{{range $index, $element := .libraryPath}}
  {{$element}}/** mr,
{{- end }}
{{- end }}
}
`))

// forkproxyProfile generates the AppArmor profile template from the given network.
func forkproxyProfile(state *state.State, inst instance, dev device) (string, error) {
	rootPath := ""
	if shared.InSnap() {
		rootPath = "/var/lib/snapd/hostfs"
	}

	// Render the profile.
	var sb *strings.Builder = &strings.Builder{}
	err := forkproxyProfileTpl.Execute(sb, map[string]interface{}{
		"name":        ForkproxyProfileName(inst, dev),
		"varPath":     shared.VarPath(""),
		"rootPath":    rootPath,
		"snap":        shared.InSnap(),
		"libraryPath": strings.Split(os.Getenv("LD_LIBRARY_PATH"), ":"),
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}

// ForkproxyProfileName returns the AppArmor profile name.
func ForkproxyProfileName(inst instance, dev device) string {
	path := shared.VarPath("")
	name := fmt.Sprintf("%s_%s_<%s>", dev.Name(), project.Instance(inst.Project(), inst.Name()), path)
	return profileName("", name)
}

// forkproxyProfileFilename returns the name of the on-disk profile name.
func forkproxyProfileFilename(inst instance, dev device) string {
	name := fmt.Sprintf("%s_%s", dev.Name(), project.Instance(inst.Project(), inst.Name()))
	return profileName("forkproxy", name)
}

// ForkproxyLoad ensures that the instances's policy is loaded into the kernel so the it can boot.
func ForkproxyLoad(state *state.State, inst instance, dev device) error {
	/* In order to avoid forcing a profile parse (potentially slow) on
	 * every container start, let's use AppArmor's binary policy cache,
	 * which checks mtime of the files to figure out if the policy needs to
	 * be regenerated.
	 *
	 * Since it uses mtimes, we shouldn't just always write out our local
	 * AppArmor template; instead we should check to see whether the
	 * template is the same as ours. If it isn't we should write our
	 * version out so that the new changes are reflected and we definitely
	 * force a recompile.
	 */
	profile := filepath.Join(aaPath, "profiles", forkproxyProfileFilename(inst, dev))
	content, err := ioutil.ReadFile(profile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	updated, err := forkproxyProfile(state, inst, dev)
	if err != nil {
		return err
	}

	if string(content) != string(updated) {
		err = ioutil.WriteFile(profile, []byte(updated), 0600)
		if err != nil {
			return err
		}
	}

	err = loadProfile(state, forkproxyProfileFilename(inst, dev))
	if err != nil {
		return err
	}

	return nil
}

// ForkproxyUnload ensures that the instances's policy namespace is unloaded to free kernel memory.
// This does not delete the policy from disk or cache.
func ForkproxyUnload(state *state.State, inst instance, dev device) error {
	return unloadProfile(state, forkproxyProfileFilename(inst, dev))
}

// ForkproxyDelete removes the policy from cache/disk.
func ForkproxyDelete(state *state.State, inst instance, dev device) error {
	return deleteProfile(state, forkproxyProfileFilename(inst, dev))
}
