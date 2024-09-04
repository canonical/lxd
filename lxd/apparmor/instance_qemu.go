package apparmor

import (
	"text/template"
)

var qemuProfileTpl = template.Must(template.New("qemuProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>
  #include <abstractions/consoles>
  #include <abstractions/nameservice>

  # Allow processes to send us signals by default
  signal (receive),

  capability dac_override,
  capability dac_read_search,
  capability ipc_lock,
  capability setgid,
  capability setuid,
  capability sys_chroot,
  capability sys_resource,

  # Needed by qemu
  /dev/hugepages/**                         rw,
  /dev/kvm                                  rw,
  /dev/net/tun                              rw,
  /dev/ptmx                                 rw,
  /dev/sev                                  rw,
  /dev/vfio/**                              rw,
  /dev/vhost-net                            rw,
  /dev/vhost-vsock                          rw,
  /etc/ceph/**                              r,
  /etc/machine-id                           r,
  /run/udev/data/*                          r,
  /sys/bus/                                 r,
  /sys/bus/nd/devices/                      r,
  /sys/bus/usb/devices/                     r,
  /sys/bus/usb/devices/**                   r,
  /sys/class/                               r,
  /sys/devices/**                           r,
  /sys/module/vhost/**                      r,
  /tmp/lxd_sev_*                            r,
  /{,usr/}bin/qemu-system-*                 mrix,
  /usr/share/qemu/**                        kr,
  /usr/share/seabios/**                     kr,
  @{PROC}/@{pid}/cpuset                     r,
  @{PROC}/@{pid}/task/@{tid}/comm           rw,
  {{ .rootPath }}/etc/nsswitch.conf         r,
  {{ .rootPath }}/etc/passwd                r,
  {{ .rootPath }}/etc/group                 r,
  @{PROC}/version                           r,

  # Used by qemu for live migration NBD server and client or when in a container
  unix (bind, listen, accept, send, receive, connect) type=stream,

  # Instance specific paths
  {{ .logPath }}/** rwk,
  {{ .path }}/** rwk,
  {{ .devicesPath }}/** rwk,

  # Needed for lxd fork commands
  {{ .exePath }} mr,
  @{PROC}/@{pid}/cmdline r,
  {{ .rootPath }}/{etc,lib,usr/lib}/os-release r,

  # Things that we definitely don't need
  deny @{PROC}/@{pid}/cgroup r,
  deny /sys/module/apparmor/parameters/enabled r,
  deny /sys/kernel/mm/transparent_hugepage/hpage_pmd_size r,

{{- if .snap }}
  # The binary itself (for nesting)
  /var/snap/lxd/common/lxd.debug            mr,
  /snap/lxd/*/bin/lxd                       mr,
  /snap/lxd/*/bin/qemu-system-*             mrix,
  /snap/lxd/*/share/qemu/**                 kr,

  # Snap-specific paths
  /var/snap/lxd/common/ceph/**                         r,
  /var/snap/microceph/*/conf/**                        r,
  /var/snap/lxd/*/microceph/conf/**                    r,
  {{ .rootPath }}/etc/ceph/**                          r,
  {{ .rootPath }}/run/systemd/resolve/stub-resolv.conf r,
  {{ .rootPath }}/run/systemd/resolve/resolv.conf      r,

  # Snap-specific libraries
  /snap/lxd/*/lib/**.so*            mr,

{{- if .snapExtQemuPrefix }}
  /snap/lxd/*/{{ .snapExtQemuPrefix }}/lib/**.so*        mr,
  /snap/lxd/*/{{ .snapExtQemuPrefix }}/bin/qemu-system-* mrix,
  /snap/lxd/*/{{ .snapExtQemuPrefix }}/share/**          r,
{{- end }}

{{- end }}

{{if .libraryPath -}}
  # Entries from LD_LIBRARY_PATH
{{range $index, $element := .libraryPath}}
  {{$element}}/** mr,
{{- end }}
{{- end }}

{{if .firmwarePath -}}
  # Firmware path
  {{ .firmwarePath }}                         kr,
{{- end }}

{{- if .raw }}

  ### Configuration: raw.apparmor
{{ .raw }}
{{- end }}
}
`))
