package drivers

import (
	"text/template"
)

// Base config. This is common for all VMs and has no variables in it.
var qemuBase = template.Must(template.New("qemuBase").Parse(`
# Machine
[machine]
graphics = "off"
{{if eq .architecture "x86_64" -}}
type = "q35"
{{end -}}
{{if eq .architecture "aarch64" -}}
type = "virt"
{{end -}}
{{if eq .architecture "ppc64le" -}}
type = "pseries"
{{end -}}
accel = "kvm"
usb = "off"
graphics = "off"

{{if eq .architecture "x86_64" -}}
[global]
driver = "ICH9-LPC"
property = "disable_s3"
value = "1"

[global]
driver = "ICH9-LPC"
property = "disable_s4"
value = "1"
{{end -}}

[boot-opts]
strict = "on"

# LXD serial identifier
[device]
driver = "virtio-serial"

[device]
driver = "virtserialport"
name = "org.linuxcontainers.lxd"
chardev = "vserial"

[chardev "vserial"]
backend = "ringbuf"
size = "{{.ringbufSizeBytes}}B"

# SCSI controller
{{if ne .architecture "ppc64le" -}}
[device "qemu_pcie1"]
driver = "pcie-root-port"
port = "0x10"
chassis = "1"
bus = "pcie.0"
multifunction = "on"
addr = "0x2"
{{- end }}

[device "qemu_scsi"]
driver = "virtio-scsi-pci"
{{if eq .architecture "ppc64le" -}}
bus = "pci.0"
{{else -}}
bus = "qemu_pcie1"
addr = "0x0"
{{end -}}

# Balloon driver
{{if ne .architecture "ppc64le" -}}
[device "qemu_pcie2"]
driver = "pcie-root-port"
port = "0x11"
chassis = "2"
bus = "pcie.0"
addr = "0x2.0x1"
{{- end }}

[device "qemu_ballon"]
driver = "virtio-balloon-pci"
{{if eq .architecture "ppc64le" -}}
bus = "pci.0"
{{else -}}
bus = "qemu_pcie2"
addr = "0x0"
{{end -}}

# Random number generator
[object "qemu_rng"]
qom-type = "rng-random"
filename = "/dev/urandom"

{{if ne .architecture "ppc64le" -}}
[device "qemu_pcie3"]
driver = "pcie-root-port"
port = "0x12"
chassis = "3"
bus = "pcie.0"
addr = "0x2.0x2"
{{- end }}

[device "dev-qemu_rng"]
driver = "virtio-rng-pci"
rng = "qemu_rng"
{{if eq .architecture "ppc64le" -}}
bus = "pci.0"
{{else -}}
bus = "qemu_pcie3"
addr = "0x0"
{{end -}}

# Console
[chardev "console"]
backend = "pty"
`))

var qemuMemory = template.Must(template.New("qemuMemory").Parse(`
# Memory
[memory]
size = "{{.memSizeBytes}}B"
`))

var qemuVsock = template.Must(template.New("qemuVsock").Parse(`
# Vsock
{{if ne .architecture "ppc64le" -}}
[device "qemu_pcie4"]
driver = "pcie-root-port"
port = "0x13"
chassis = "4"
bus = "pcie.0"
addr = "0x2.0x3"
{{- end }}

[device]
driver = "vhost-vsock-pci"
guest-cid = "{{.vsockID}}"
{{if eq .architecture "ppc64le" -}}
bus = "pci.0"
{{else -}}
bus = "qemu_pcie4"
addr = "0x0"
{{end -}}
`))

var qemuCPU = template.Must(template.New("qemuCPU").Parse(`
# CPU
[smp-opts]
cpus = "{{.cpuCount}}"
sockets = "{{.cpuSockets}}"
cores = "{{.cpuCores}}"
threads = "{{.cpuThreads}}"
`))

var qemuControlSocket = template.Must(template.New("qemuControlSocket").Parse(`
# Qemu control
[chardev "monitor"]
backend = "socket"
path = "{{.path}}"
server = "on"
wait = "off"

[mon]
chardev = "monitor"
mode = "control"
`))

var qemuDriveFirmware = template.Must(template.New("qemuDriveFirmware").Parse(`
# Firmware (read only)
[drive]
file = "{{.roPath}}"
if = "pflash"
format = "raw"
unit = "0"
readonly = "on"

# Firmware settings (writable)
[drive]
file = "{{.nvramPath}}"
if = "pflash"
format = "raw"
unit = "1"
`))

// Devices use "qemu_" prefix indicating that this is a internally named device.
var qemuDriveConfig = template.Must(template.New("qemuDriveConfig").Parse(`
# Config drive
[fsdev "qemu_config"]
fsdriver = "local"
security_model = "none"
readonly = "on"
path = "{{.path}}"

[device "dev-qemu_config"]
driver = "virtio-9p-pci"
fsdev = "qemu_config"
mount_tag = "config"
`))

// Devices use "lxd_" prefix indicating that this is a internally named device.
var qemuDriveDir = template.Must(template.New("qemuDriveDir").Parse(`
# {{.devName}} drive
[fsdev "lxd_{{.devName}}"]
{{- if .readonly}}
readonly = "on"
fsdriver = "local"
security_model = "none"
path = "{{.path}}"
{{- else}}
readonly = "off"
fsdriver = "proxy"
sock_fd = "{{.proxyFD}}"
{{- end}}

[device "dev-lxd_{{.devName}}"]
driver = "virtio-9p-pci"
fsdev = "lxd_{{.devName}}"
mount_tag = "{{.mountTag}}"
`))

// Devices use "lxd_" prefix indicating that this is a user named device.
var qemuDrive = template.Must(template.New("qemuDrive").Parse(`
# {{.devName}} drive
[drive "lxd_{{.devName}}"]
file = "{{.devPath}}"
format = "raw"
if = "none"
cache = "{{.cacheMode}}"
aio = "{{.aioMode}}"
discard = "on"

[device "dev-lxd_{{.devName}}"]
driver = "scsi-hd"
bus = "qemu_scsi.0"
channel = "0"
scsi-id = "{{.bootIndex}}"
lun = "1"
drive = "lxd_{{.devName}}"
bootindex = "{{.bootIndex}}"
`))

// qemuDevTapCommon is common PCI device template for tap based netdevs.
var qemuDevTapCommon = template.Must(template.New("qemuDevTapCommon").Parse(`
{{if ne .architecture "ppc64le" -}}
[device "qemu_pcie{{.chassisIndex}}"]
driver = "pcie-root-port"
port = "0x{{.portIndex}}"
chassis = "{{.chassisIndex}}"
bus = "pcie.0"
addr = "0x2.0x{{.pcieAddr}}"
{{- end }}

[device "dev-lxd_{{.devName}}"]
driver = "virtio-net-pci"
netdev = "lxd_{{.devName}}"
mac = "{{.devHwaddr}}"
{{if eq .architecture "ppc64le" -}}
bus = "pci.0"
{{else -}}
bus = "qemu_pcie{{.chassisIndex}}"
addr = "0x0"
{{end -}}
bootindex = "{{.bootIndex}}"
`))

// Devices use "lxd_" prefix indicating that this is a user named device.
var qemuNetDevTapTun = template.Must(qemuDevTapCommon.New("qemuNetDevTapTun").Parse(`
# Network card ("{{.devName}}" device)
[netdev "lxd_{{.devName}}"]
type = "tap"
vhost = "on"
ifname = "{{.ifName}}"
script = "no"
downscript = "no"
{{ template "qemuDevTapCommon" . -}}
`))

// Devices use "lxd_" prefix indicating that this is a user named device.
var qemuNetdevTapFD = template.Must(qemuDevTapCommon.New("qemuNetdevTapFD").Parse(`
# Network card ("{{.devName}}" device)
[netdev "lxd_{{.devName}}"]
type = "tap"
vhost = "on"
fd = "{{.tapFD}}"
{{ template "qemuDevTapCommon" . -}}
`))

// Devices use "lxd_" prefix indicating that this is a user named device.
var qemuNetdevPhysical = template.Must(template.New("qemuNetdevPhysical").Parse(`
# Network card ("{{.devName}}" device)
[device "dev-lxd_{{.devName}}"]
driver = "vfio-pci"
host = "{{.pciSlotName}}"
bootindex = "{{.bootIndex}}"
`))
