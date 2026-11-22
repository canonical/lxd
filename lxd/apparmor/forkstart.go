package apparmor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared"
)

const forkstartProfileFilename = "lxd_forkstart"

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

func forkstartProfileEnabled(sysOS *sys.OS) bool {
	return sysOS.AppArmorAvailable && sysOS.AppArmorAdmin && !sysOS.AppArmorConfined && !sysOS.AppArmorStacked
}

// forkstartProfileName returns the daemon-scoped AppArmor profile name for container helper processes.
func forkstartProfileName(sysOS *sys.OS) string {
	varDir := sysOS.VarDir
	if varDir == "" {
		varDir = shared.VarPath("")
	} else {
		varDir = filepath.Clean(varDir)
	}

	return profileName("forkstart", "<"+varDir+">")
}

// ForkstartWrapper confines container helper processes before they enter an instance user namespace.
func ForkstartWrapper(sysOS *sys.OS, cmd *exec.Cmd) error {
	if !forkstartProfileEnabled(sysOS) {
		return nil
	}

	execPath, err := exec.LookPath("aa-exec")
	if err != nil {
		return nil
	}

	args := make([]string, 0, 3+len(cmd.Args))
	args = append(args, "aa-exec", "-p", forkstartProfileName(sysOS))
	args = append(args, cmd.Args...)
	cmd.Args = args
	cmd.Path = execPath

	return nil
}

// ForkstartLoad ensures that the daemon's container helper profile is loaded at startup.
func ForkstartLoad(sysOS *sys.OS) error {
	if !forkstartProfileEnabled(sysOS) {
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

	updated, err := forkstartProfile(forkstartProfileName(sysOS), usernsRuleSupported)
	if err != nil {
		return err
	}

	profilePath := filepath.Join(aaPath, "profiles", forkstartProfileFilename)
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

	return loadProfile(sysOS, forkstartProfileFilename)
}

// ForkstartDelete unloads and removes the daemon's container helper profile from the kernel, cache and disk.
func ForkstartDelete(sysOS *sys.OS) error {
	if !forkstartProfileEnabled(sysOS) {
		return nil
	}

	return deleteProfile(sysOS, forkstartProfileName(sysOS), forkstartProfileFilename)
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
