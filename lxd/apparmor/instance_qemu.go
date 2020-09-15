package apparmor

import (
	"text/template"
)

var qemuProfileTpl = template.Must(template.New("qemuProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>
  #include <abstractions/consoles>
  #include <abstractions/nameservice>

  capability dac_override,
  capability dac_read_search,
  capability setgid,
  capability setuid,
  capability sys_chroot,
  capability sys_resource,

  # Needed by qemu
  /{,usr/}bin/qemu*                         mrix,
  /dev/hugepages/**                         w,
  /dev/kvm                                  w,
  /dev/net/tun                              w,
  /dev/ptmx                                 w,
  /dev/vfio/**                              w,
  /dev/vhost-net                            w,
  /dev/vhost-vsock                          w,
  /etc/ceph/**                              r,
  /usr/share/OVMF/OVMF_CODE.fd              kr,
  owner @{PROC}/@{pid}/task/@{tid}/comm     rw,

  # Instance specific paths
  {{ .logPath }}/** rwk,
  {{ .path }}/qemu.nvram rwk,
{{range $index, $element := .devPaths}}
  {{$element}} rwk,
{{- end }}

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
  /snap/lxd/*/bin/qemu*                     mrix,
  /snap/lxd/*/share/qemu/OVMF_CODE.fd       kr,

  # Snap-specific libraries
  /snap/lxd/*/lib/**.so*            mr,
{{- end }}

{{if .libraryPath -}}
  # Entries from LD_LIBRARY_PATH
{{range $index, $element := .libraryPath}}
  {{$element}}/** mr,
{{- end }}
{{- end }}

{{- if .raw }}

  ### Configuration: raw.apparmor
{{ .raw }}
{{- end }}
}
`))
