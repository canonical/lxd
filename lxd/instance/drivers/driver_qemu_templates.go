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
gic-version = "host"
{{end -}}
{{if eq .architecture "ppc64le" -}}
type = "pseries"
{{end -}}
{{if eq .architecture "s390x" -}}
type = "s390-ccw-virtio"
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

# Console
[chardev "console"]
backend = "pty"

# Graphical console
[spice]
unix = "on"
addr = "{{.spicePath}}"
disable-ticketing = "on"
`))

var qemuMemory = template.Must(template.New("qemuMemory").Parse(`
# Memory
[memory]
size = "{{.memSizeBytes}}M"
`))

var qemuSerial = template.Must(template.New("qemuSerial").Parse(`
# LXD serial identifier
[device "dev-qemu_serial"]
{{- if eq .bus "pci" "pcie"}}
driver = "virtio-serial-pci"
bus = "{{.devBus}}"
addr = "{{.devAddr}}"
{{- end}}
{{if eq .bus "ccw" -}}
driver = "virtio-serial-ccw"
{{- end}}
{{if .multifunction -}}
multifunction = "on"
{{- end }}

[chardev "qemu_serial-chardev"]
backend = "ringbuf"
size = "{{.ringbufSizeBytes}}B"

[device "qemu_serial"]
driver = "virtserialport"
name = "org.linuxcontainers.lxd"
chardev = "{{.chardevName}}"
bus = "dev-qemu_serial.0"
`))

var qemuPCIe = template.Must(template.New("qemuPCIe").Parse(`
[device "qemu_pcie{{.index}}"]
driver = "pcie-root-port"
bus = "pcie.0"
addr = "{{.addr}}"
chassis = "{{.index}}"
{{if .multifunction -}}
multifunction = "on"
{{- end }}
`))

var qemuSCSI = template.Must(template.New("qemuSCSI").Parse(`
# SCSI controller
[device "qemu_scsi"]
{{- if eq .bus "pci" "pcie"}}
driver = "virtio-scsi-pci"
bus = "{{.devBus}}"
addr = "{{.devAddr}}"
{{- end}}
{{if eq .bus "ccw" -}}
driver = "virtio-scsi-ccw"
{{- end}}
{{if .multifunction -}}
multifunction = "on"
{{- end }}
`))

var qemuBalloon = template.Must(template.New("qemuBalloon").Parse(`
# Balloon driver
[device "qemu_balloon"]
{{- if eq .bus "pci" "pcie"}}
driver = "virtio-balloon-pci"
bus = "{{.devBus}}"
addr = "{{.devAddr}}"
{{- end}}
{{if eq .bus "ccw" -}}
driver = "virtio-balloon-ccw"
{{- end}}
{{if .multifunction -}}
multifunction = "on"
{{- end }}
`))

var qemuRNG = template.Must(template.New("qemuRNG").Parse(`
# Random number generator
[object "qemu_rng"]
qom-type = "rng-random"
filename = "/dev/urandom"

[device "dev-qemu_rng"]
{{- if eq .bus "pci" "pcie"}}
driver = "virtio-rng-pci"
bus = "{{.devBus}}"
addr = "{{.devAddr}}"
{{- end}}
{{if eq .bus "ccw" -}}
driver = "virtio-rng-ccw"
{{- end}}
rng = "qemu_rng"
{{if .multifunction -}}
multifunction = "on"
{{- end }}
`))

var qemuVsock = template.Must(template.New("qemuVsock").Parse(`
# Vsock
[device "qemu_vsock"]
{{- if eq .bus "pci" "pcie"}}
driver = "vhost-vsock-pci"
bus = "{{.devBus}}"
addr = "{{.devAddr}}"
{{- end}}
{{if eq .bus "ccw" -}}
driver = "vhost-vsock-ccw"
{{- end}}
guest-cid = "{{.vsockID}}"
{{if .multifunction -}}
multifunction = "on"
{{- end }}
`))

var qemuGPU = template.Must(template.New("qemuGPU").Parse(`
# GPU
[device "qemu_gpu"]
{{- if eq .bus "pci" "pcie"}}
{{if eq .architecture "x86_64" -}}
driver = "virtio-vga"
{{- else}}
driver = "virtio-gpu-pci"
{{- end}}
bus = "{{.devBus}}"
addr = "{{.devAddr}}"
{{- end}}
{{if eq .bus "ccw" -}}
driver = "virtio-gpu-ccw"
{{- end}}
{{if .multifunction -}}
multifunction = "on"
{{- end }}
`))

var qemuKeyboard = template.Must(template.New("qemuKeyboard").Parse(`
# Input
[device "qemu_keyboard"]
{{- if eq .bus "pci" "pcie"}}
driver = "virtio-keyboard-pci"
bus = "{{.devBus}}"
addr = "{{.devAddr}}"
{{- end}}
{{if eq .bus "ccw" -}}
driver = "virtio-keyboard-ccw"
{{- end}}
{{if .multifunction -}}
multifunction = "on"
{{- end }}
`))

var qemuTablet = template.Must(template.New("qemuTablet").Parse(`
# Input
[device "qemu_tablet"]
{{- if eq .bus "pci" "pcie"}}
driver = "virtio-tablet-pci"
bus = "{{.devBus}}"
addr = "{{.devAddr}}"
{{- end}}
{{if eq .bus "ccw" -}}
driver = "virtio-tablet-ccw"
{{- end}}
{{if .multifunction -}}
multifunction = "on"
{{- end }}
`))

var qemuCPU = template.Must(template.New("qemuCPU").Parse(`
# CPU
[smp-opts]
cpus = "{{.cpuCount}}"
sockets = "{{.cpuSockets}}"
cores = "{{.cpuCores}}"
threads = "{{.cpuThreads}}"

{{if eq .architecture "x86_64" -}}
{{$memory := .memory -}}
{{$hugepages := .hugepages -}}
{{if .cpuNumaHostNodes -}}
{{range $index, $element := .cpuNumaHostNodes}}
[object "mem{{$index}}"]
{{if ne $hugepages "" -}}
qom-type = "memory-backend-file"
mem-path = "{{$hugepages}}"
prealloc = "on"
discard-data = "on"
{{- else}}
qom-type = "memory-backend-ram"
{{- end }}
size = "{{$memory}}M"
host-nodes = "{{$element}}"
policy = "bind"

[numa]
type = "node"
nodeid = "{{$element}}"
memdev = "mem{{$element}}"
{{end}}
{{else}}
[object "mem0"]
{{if ne $hugepages "" -}}
qom-type = "memory-backend-file"
mem-path = "{{$hugepages}}"
prealloc = "on"
discard-data = "on"
{{- else}}
qom-type = "memory-backend-ram"
{{- end }}
size = "{{$memory}}M"

[numa]
type = "node"
nodeid = "0"
memdev = "mem0"
{{end}}

{{range .cpuNumaMapping}}
[numa]
type = "cpu"
node-id = "{{.node}}"
socket-id = "{{.socket}}"
core-id = "{{.core}}"
thread-id = "{{.thread}}"
{{end}}
{{end}}
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
{{if eq .architecture "x86_64" "aarch64" -}}
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
{{- end }}
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
{{- if eq .bus "pci" "pcie"}}
driver = "virtio-9p-pci"
bus = "{{.devBus}}"
addr = "{{.devAddr}}"
{{- end}}
{{if eq .bus "ccw" -}}
driver = "virtio-9p-ccw"
{{- end}}
mount_tag = "config"
fsdev = "qemu_config"
{{if .multifunction -}}
multifunction = "on"
{{- end }}
`))

// Devices use "lxd_" prefix indicating that this is a user named device.
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
{{- if eq .bus "pci" "pcie"}}
driver = "virtio-9p-pci"
bus = "{{.devBus}}"
addr = "{{.devAddr}}"
{{- end}}
{{if eq .bus "ccw" -}}
driver = "virtio-9p-ccw"
{{- end}}
fsdev = "lxd_{{.devName}}"
mount_tag = "{{.mountTag}}"
{{if .multifunction -}}
multifunction = "on"
{{- end }}
`))

// Devices use "lxd_" prefix indicating that this is a user named device.
// The device name prefix must not be changed as we want to have /dev/disk/by-id be a usable stable identifier
// inside the VM guest.
var qemuDrive = template.Must(template.New("qemuDrive").Parse(`
# {{.devName}} drive
[drive "lxd_{{.devName}}"]
file = "{{.devPath}}"
format = "raw"
if = "none"
cache = "{{.cacheMode}}"
aio = "{{.aioMode}}"
discard = "on"
{{if .shared -}}
file.locking = "off"
{{- end }}

[device "dev-lxd_{{.devName}}"]
driver = "scsi-hd"
bus = "qemu_scsi.0"
channel = "0"
scsi-id = "{{.bootIndex}}"
lun = "1"
drive = "lxd_{{.devName}}"
bootindex = "{{.bootIndex}}"
{{if .multifunction -}}
multifunction = "on"
{{- end }}
`))

// qemuNetDevTapCommon is common PCI device template for tap based netdevs.
var qemuNetDevTapCommon = template.Must(template.New("qemuNetDevTapCommon").Parse(`
[device "dev-lxd_{{.devName}}"]
{{- if eq .bus "pci" "pcie"}}
driver = "virtio-net-pci"
bus = "{{.devBus}}"
addr = "{{.devAddr}}"
{{- end}}
{{if eq .bus "ccw" -}}
driver = "virtio-net-ccw"
{{- end}}
netdev = "lxd_{{.devName}}"
mac = "{{.devHwaddr}}"
bootindex = "{{.bootIndex}}"
{{if .multifunction -}}
multifunction = "on"
{{- end }}
`))

// Devices use "lxd_" prefix indicating that this is a user named device.
var qemuNetDevTapTun = template.Must(qemuNetDevTapCommon.New("qemuNetDevTapTun").Parse(`
# Network card ("{{.devName}}" device)
[netdev "lxd_{{.devName}}"]
type = "tap"
vhost = "on"
ifname = "{{.ifName}}"
script = "no"
downscript = "no"
{{ template "qemuNetDevTapCommon" . -}}
`))

// Devices use "lxd_" prefix indicating that this is a user named device.
var qemuNetDevTapFD = template.Must(qemuNetDevTapCommon.New("qemuNetDevTapFD").Parse(`
# Network card ("{{.devName}}" device)
[netdev "lxd_{{.devName}}"]
type = "tap"
vhost = "on"
fd = "{{.tapFD}}"
{{ template "qemuNetDevTapCommon" . -}}
`))

// Devices use "lxd_" prefix indicating that this is a user named device.
var qemuNetDevPhysical = template.Must(template.New("qemuNetDevPhysical").Parse(`
# Network card ("{{.devName}}" device)
[device "dev-lxd_{{.devName}}"]
{{- if eq .bus "pci" "pcie"}}
driver = "vfio-pci"
bus = "{{.devBus}}"
addr = "{{.devAddr}}"
{{- end}}
{{if eq .bus "ccw" -}}
driver = "vfio-ccw"
{{- end}}
host = "{{.pciSlotName}}"
bootindex = "{{.bootIndex}}"
{{if .multifunction -}}
multifunction = "on"
{{- end }}
`))

// Devices use "lxd_" prefix indicating that this is a user named device.
var qemuGPUDevPhysical = template.Must(template.New("qemuGPUDevPhysical").Parse(`
# GPU card ("{{.devName}}" device)
[device "dev-lxd_{{.devName}}"]
{{- if eq .bus "pci" "pcie"}}
driver = "vfio-pci"
bus = "{{.devBus}}"
addr = "{{.devAddr}}"
{{- end}}
{{if eq .bus "ccw" -}}
driver = "vfio-ccw"
{{- end}}
host = "{{.pciSlotName}}"
{{if .vga -}}
x-vga = "on"
{{- end }}
{{if .multifunction -}}
multifunction = "on"
{{- end }}
`))
