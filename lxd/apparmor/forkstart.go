package apparmor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/canonical/lxd/lxd/sys"
)

// The policy is host-global and contains no per-daemon paths, so all LXD
// daemons on a host intentionally share the same kernel profile.
const forkstartProfileName = "lxd_forkstart"

var (
	forkstartProfileTpl = template.Must(template.New("forkstartProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  capability,
  file,
  mount,
  umount,
  pivot_root,
  network,
  unix,
  ptrace,
  signal,
  dbus,
{{- if .feature_userns_rule }}
  userns,
{{- end }}

  change_profile -> lxd-**,
  change_profile -> ":lxd-**:*",
}
`))
)

// ForkstartWrapper confines container helper processes before they enter an instance user namespace.
func ForkstartWrapper(sysOS *sys.OS, cmd *exec.Cmd) error {
	if !sysOS.AppArmorAvailable || !sysOS.AppArmorAdmin || sysOS.AppArmorConfined || sysOS.AppArmorStacked {
		return nil
	}

	execPath, err := exec.LookPath("aa-exec")
	if err != nil {
		return nil
	}

	args := make([]string, 0, 3+len(cmd.Args))
	args = append(args, "aa-exec", "-p", forkstartProfileName)
	args = append(args, cmd.Args...)
	cmd.Args = args
	cmd.Path = execPath

	return nil
}

// ForkstartLoad ensures that the daemon's container helper profile is loaded at startup.
func ForkstartLoad(sysOS *sys.OS) error {
	if !sysOS.AppArmorAvailable || !sysOS.AppArmorAdmin || sysOS.AppArmorConfined || sysOS.AppArmorStacked {
		return nil
	}

	_, err := exec.LookPath("aa-exec")
	if err != nil {
		return nil
	}

	usernsRuleSupported, err := parserSupports(sysOS, "userns_rule")
	if err != nil {
		return err
	}

	updated, err := forkstartProfile(forkstartProfileName, usernsRuleSupported)
	if err != nil {
		return err
	}

	profilePath := filepath.Join(aaPath, "profiles", forkstartProfileName)
	content, err := os.ReadFile(profilePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	changed := string(content) != updated
	if changed {
		err = os.WriteFile(profilePath, []byte(updated), 0600)
		if err != nil {
			return err
		}
	}

	return loadProfile(sysOS, forkstartProfileName)
}

func forkstartProfile(name string, usernsRuleSupported bool) (string, error) {
	sb := &strings.Builder{}
	err := forkstartProfileTpl.Execute(sb, map[string]any{
		"name":                name,
		"feature_userns_rule": usernsRuleSupported,
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}
