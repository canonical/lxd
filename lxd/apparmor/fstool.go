package apparmor

import (
	"strings"
	"text/template"

	"github.com/canonical/lxd/shared"
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
