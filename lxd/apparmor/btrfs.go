package apparmor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared/revert"
)

var btrfsProfileTpl = template.Must(template.New("btrfsProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>

  # Allow processes to send us signals by default
  signal (receive),

  capability chown,
  capability dac_override,
  capability dac_read_search,
  capability fowner,
  capability fsetid,
  capability mknod,
  capability setfcap,
  # CAP_SYS_ADMIN is needed for btrfs send/receive.
  capability sys_admin,

  {{ .execPath }} mixr,

  # Prevent "cannot open /proc/<pid>/mounts: Permission denied" error.
  @{PROC}/@{pid}/mounts r,

   # Allow checking for btrfs features if available in the kernel.
  /sys/fs/btrfs/features/* r,

{{- if .sourcePath }}
  {{ .sourcePath }}/** r,
  {{ .sourcePath }} r,
  {{ .sourcePath }}/ r,
{{- end }}

{{- if .dstPath }}
  {{ .dstPath }}/** rwkl,
  {{ .dstPath }} rwkl,
  {{ .dstPath }}/ rwkl,
{{- end }}

{{- if .poolMountPath }}
  # btrfs send/receive need broad read access across the whole pool when transferring snapshots.
  {{ .poolMountPath }}/containers/** r,
  {{ .poolMountPath }}/containers-snapshots/** r,
  {{ .poolMountPath }}/virtual-machines/** r,
  {{ .poolMountPath }}/virtual-machines-snapshots/** r,
  {{ .poolMountPath }}/custom/** r,
  {{ .poolMountPath }}/custom-snapshots/** r,
  {{ .poolMountPath }}/ r,
{{- end }}
}
`))

// BtrfsWrapper is used to run the btrfs send/receive commands under a restricted AppArmor profile.
func BtrfsWrapper(sysOS *sys.OS, cmd *exec.Cmd, sourcePath string, dstPath string, poolMountPath string) (func(), error) {
	if !sysOS.AppArmorAvailable {
		return func() {}, nil
	}

	revert := revert.New()
	defer revert.Fail()

	// Attempt to deref all paths.
	if sourcePath != "" {
		fullPath, err := filepath.EvalSymlinks(sourcePath)
		if err == nil {
			sourcePath = fullPath
		}
	}

	if dstPath != "" {
		fullPath, err := filepath.EvalSymlinks(dstPath)
		if err == nil {
			dstPath = fullPath
		}
	}

	if poolMountPath != "" {
		fullPath, err := filepath.EvalSymlinks(poolMountPath)
		if err == nil {
			poolMountPath = fullPath
		}
	}

	// Load the profile.
	profileName, err := btrfsProfileLoad(sysOS, sourcePath, dstPath, poolMountPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to load btrfs profile: %w", err)
	}

	revert.Add(func() { _ = deleteProfile(sysOS, profileName, profileName) })

	// Resolve aa-exec.
	execPath, err := exec.LookPath("aa-exec")
	if err != nil {
		return nil, err
	}

	// Override the command.
	newArgs := []string{"aa-exec", "-p", profileName}
	newArgs = append(newArgs, cmd.Args...)
	cmd.Args = newArgs
	cmd.Path = execPath

	// All done, setup a cleanup function and disarm reverter.
	cleanup := func() {
		_ = deleteProfile(sysOS, profileName, profileName)
	}

	revert.Success()

	return cleanup, nil
}

func btrfsProfileLoad(sysOS *sys.OS, sourcePath string, dstPath string, poolMountPath string) (string, error) {
	revert := revert.New()
	defer revert.Fail()

	// Generate a temporary profile name.
	name := profileName("btrfs", uuid.New().String())
	profilePath := filepath.Join(aaPath, "profiles", name)

	// Generate the profile
	content, err := btrfsProfile(name, sourcePath, dstPath, poolMountPath)
	if err != nil {
		return "", err
	}

	// Write it to disk.
	err = os.WriteFile(profilePath, []byte(content), 0600)
	if err != nil {
		return "", err
	}

	revert.Add(func() { os.Remove(profilePath) })

	// Load it.
	err = loadProfile(sysOS, name)
	if err != nil {
		return "", err
	}

	revert.Success()
	return name, nil
}

// btrfsProfile generates the AppArmor profile template from the given source/destination paths.
func btrfsProfile(name string, sourcePath string, dstPath string, poolMountPath string) (string, error) {
	// Fully deref the executable path.
	execPath, err := exec.LookPath("btrfs")
	if err != nil {
		return "", err
	}

	fullPath, err := filepath.EvalSymlinks(execPath)
	if err == nil {
		execPath = fullPath
	}

	sb := &strings.Builder{}
	err = btrfsProfileTpl.Execute(sb, map[string]any{
		"name":          name,
		"execPath":      execPath,
		"sourcePath":    sourcePath,
		"dstPath":       dstPath,
		"poolMountPath": poolMountPath,
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}
