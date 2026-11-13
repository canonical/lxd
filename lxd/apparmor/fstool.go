package apparmor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/revert"
)

// fsToolProfileTpl confines the tools that format, check or modify a filesystem on a single device or image file.
// Those tools run as root and, depending on their options, can read or write arbitrary host files, so only
// the target device and the system files needed to inspect it are made accessible.
var fsToolProfileTpl = template.Must(template.New("fsToolProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>

  # Allow processes to send us signals by default
  signal (receive),

  capability dac_override,
  capability dac_read_search,
  capability fowner,
  capability chown,
  capability fsetid,
  capability sys_rawio,
  # Needed by the filesystem tools to query and change block devices and to flush their buffers.
  capability sys_admin,
  capability sys_resource,
  capability ipc_lock,

{{range $index, $element := .execPaths}}
  "{{$element}}" mixr,
{{- end }}

  # The target of the operation.
{{- range $index, $element := .devPaths }}
  "{{$element}}" rwk,
{{- end }}

{{- range $index, $element := .mountPaths }}

  # Online operations on a mounted filesystem go through its mount point.
  "{{$element}}" r,
  "{{$element}}/" r,
{{- end }}

  # Device and filesystem probing.
  /dev/ r,
  @{PROC}/devices r,
  @{PROC}/filesystems r,
  @{PROC}/partitions r,
  @{PROC}/swaps r,
  @{PROC}/@{pid}/mounts r,
  @{PROC}/@{pid}/mountinfo r,
  /sys/block/ r,
  /sys/block/** r,
  /sys/class/block/ r,
  /sys/class/block/** r,
  /sys/dev/block/ r,
  /sys/dev/block/** r,
  /sys/devices/** r,
  /sys/fs/*/features/ r,
  /sys/fs/*/features/* r,
  /run/udev/data/* r,
  /run/blkid/ rw,
  /run/blkid/** rwk,

  # Tool configuration.
  /etc/blkid.conf r,
  /etc/e2fsck.conf r,
  /etc/mke2fs.conf r,
  /etc/mke2fs.conf.d/{,*} r,
  /etc/mtab r,
  /etc/xfs/{,**} r,

{{- if .snap }}

  # Snap-specific libraries
  /snap/lxd/*/lib/**.so* mr,
{{- end }}

{{if .libraryPath -}}
  # Shared libraries
{{range $index, $element := .libraryPath}}
  "{{$element}}/**" mr,
{{- end }}
{{- end }}
}
`))

// fsToolHelpers lists the executables that a filesystem tool runs itself, which must be allowed to execute under its profile.
var fsToolHelpers = map[string][]string{
	// xfs_admin is a shell script that runs xfs_db to generate the UUID.
	"xfs_admin": {"xfs_db"},
}

// FSToolWrapper returns the command and arguments to run the given filesystem tool under an AppArmor profile that
// only grants access to the supplied device (or image file) and, for online operations, its mount point. The returned cleanup function must be called once the
// command has finished.
//
// If AppArmor isn't available, the tool is returned unchanged.
func FSToolWrapper(sysOS *sys.OS, tool string, args []string, devPath string, mountPath string) (string, []string, func(), error) {
	if !sysOS.AppArmorAvailable || !sysOS.AppArmorAdmin {
		return tool, args, func() {}, nil
	}

	revert := revert.New()
	defer revert.Fail()

	toolPath, err := exec.LookPath(tool)
	if err != nil {
		return "", nil, nil, fmt.Errorf("Failed finding executable %q: %w", tool, err)
	}

	execPaths := []string{toolPath}
	fullPath, err := filepath.EvalSymlinks(toolPath)
	if err == nil && fullPath != toolPath {
		execPaths = append(execPaths, fullPath)
	}

	for _, helper := range fsToolHelpers[tool] {
		helperPath, err := exec.LookPath(helper)
		if err != nil {
			return "", nil, nil, fmt.Errorf("Failed finding executable %q: %w", helper, err)
		}

		execPaths = append(execPaths, helperPath)
		fullPath, err := filepath.EvalSymlinks(helperPath)
		if err == nil && fullPath != helperPath {
			execPaths = append(execPaths, fullPath)
		}
	}

	// Allow both the path as supplied and what it resolves to (device nodes are often symlinks).
	resolve := func(p string) []string {
		if p == "" {
			return nil
		}

		fullPath, err := filepath.EvalSymlinks(p)
		if err == nil && fullPath != p {
			return []string{p, fullPath}
		}

		return []string{p}
	}

	devPaths := resolve(devPath)
	mountPaths := resolve(mountPath)

	// Resolve aa-exec.
	aaExecPath, err := exec.LookPath("aa-exec")
	if err != nil {
		return "", nil, nil, err
	}

	name := profileName("fstool", uuid.New().String())
	profilePath := filepath.Join(aaPath, "profiles", name)

	content, err := fsToolProfile(name, execPaths, devPaths, mountPaths, profileLibraryPaths(toolPath))
	if err != nil {
		return "", nil, nil, err
	}

	err = os.WriteFile(profilePath, []byte(content), 0600)
	if err != nil {
		return "", nil, nil, err
	}

	revert.Add(func() { _ = os.Remove(profilePath) })

	err = loadProfile(sysOS, name)
	if err != nil {
		return "", nil, nil, fmt.Errorf("Failed loading %s profile: %w", tool, err)
	}

	revert.Add(func() { _ = deleteProfile(sysOS, name, name) })

	newArgs := slices.Concat([]string{"-p", name, tool}, args)

	cleanup := func() {
		_ = deleteProfile(sysOS, name, name)
	}

	revert.Success()

	return aaExecPath, newArgs, cleanup, nil
}

// fsToolProfile generates the AppArmor profile for a filesystem tool.
func fsToolProfile(name string, execPaths []string, devPaths []string, mountPaths []string, libraryPaths []string) (string, error) {
	sb := &strings.Builder{}
	err := fsToolProfileTpl.Execute(sb, map[string]any{
		"name":        name,
		"execPaths":   escapeRulePaths(execPaths),
		"devPaths":    escapeRulePaths(devPaths),
		"mountPaths":  escapeRulePaths(mountPaths),
		"snap":        shared.InSnap(),
		"libraryPath": escapeRulePaths(libraryPaths),
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}
