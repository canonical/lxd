package apparmor

import (
	"text/template"
)

var dnsmasqProfileTpl = template.Must(template.New("dnsmasqProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>
  #include <abstractions/dbus>
  #include <abstractions/nameservice>

  # Capabilities
  capability chown,
  capability net_bind_service,
  capability setgid,
  capability setuid,
  capability dac_override,
  capability dac_read_search,
  capability net_admin,         # for DHCP server
  capability net_raw,           # for DHCP server ping checks

  # Network access
  network inet raw,
  network inet6 raw,

  # Network-specific paths
  {{ .varPath }}/networks/{{ .networkName }}/dnsmasq.hosts/{,*} r,
  {{ .varPath }}/networks/{{ .networkName }}/dnsmasq.leases rw,
  {{ .varPath }}/networks/{{ .networkName }}/dnsmasq.raw r,

  # Additional system files
  @{PROC}/sys/net/ipv6/conf/*/mtu r,

  # System configuration access
  {{ .rootPath }}/etc/gai.conf           r,
  {{ .rootPath }}/etc/group              r,
  {{ .rootPath }}/etc/host.conf          r,
  {{ .rootPath }}/etc/hosts              r,
  {{ .rootPath }}/etc/nsswitch.conf      r,
  {{ .rootPath }}/etc/passwd             r,
  {{ .rootPath }}/etc/protocols          r,

  {{ .rootPath }}/etc/resolv.conf        r,
  {{ .rootPath }}/etc/resolvconf/run/resolv.conf r,

  {{ .rootPath }}/run/{resolvconf,NetworkManager,systemd/resolve,connman,netconfig}/resolv.conf r,
  {{ .rootPath }}/run/systemd/resolve/stub-resolv.conf r,

{{- if .snap }}

  # Snap-specific libraries
  /snap/lxd/current/lib/**.so*            mr,
  /snap/lxd/*/lib/**.so*                  mr,
{{- end }}
}
`))
