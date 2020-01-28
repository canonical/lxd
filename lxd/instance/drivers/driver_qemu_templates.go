package drivers

import (
	"text/template"
)

// Base config. This is common for all VMs and has no variables in it.
var qemuBase = template.Must(template.New("qemuBase").Parse(`
# Machine
[machine]
graphics = "off"
type = "{{.qemuType}}"
accel = "kvm"
usb = "off"
graphics = "off"
{{ .qemuConf -}}

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

# PCIe root
[device "qemu_pcie1"]
driver = "pcie-root-port"
port = "0x10"
chassis = "1"
bus = "pcie.0"
multifunction = "on"
addr = "0x2"

[device "qemu_scsi"]
driver = "virtio-scsi-pci"
bus = "qemu_pcie1"
addr = "0x0"

# Balloon driver
[device "qemu_pcie2"]
driver = "pcie-root-port"
port = "0x11"
chassis = "2"
bus = "pcie.0"
addr = "0x2.0x1"

[device "qemu_ballon"]
driver = "virtio-balloon-pci"
bus = "qemu_pcie2"
addr = "0x0"

# Random number generator
[object "qemu_rng"]
qom-type = "rng-random"
filename = "/dev/urandom"

[device "qemu_pcie3"]
driver = "pcie-root-port"
port = "0x12"
chassis = "3"
bus = "pcie.0"
addr = "0x2.0x2"

[device "dev-qemu_rng"]
driver = "virtio-rng-pci"
rng = "qemu_rng"
bus = "qemu_pcie3"
addr = "0x0"

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
[device "qemu_pcie4"]
driver = "pcie-root-port"
port = "0x13"
chassis = "4"
bus = "pcie.0"
addr = "0x2.0x3"

[device]
driver = "vhost-vsock-pci"
guest-cid = "{{.vsockID}}"
bus = "qemu_pcie4"
addr = "0x0"
`))

var qemuCPU = template.Must(template.New("qemuCPU").Parse(`
# CPU
[smp-opts]
cpus = "{{.cpuCount}}"
#sockets = "1"
#cores = "1"
#threads = "1"
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

// Devices use "lxd_" prefix indicating that this is a user named device.
var qemuDrive = template.Must(template.New("qemuDrive").Parse(`
# {{.devName}} drive
[drive "lxd_{{.devName}}"]
file = "{{.devPath}}"
format = "raw"
if = "none"
cache = "{{.cacheMode}}"
aio = "{{.aioMode}}"

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
[device "qemu_pcie{{.chassisIndex}}"]
driver = "pcie-root-port"
port = "0x{{.portIndex}}"
chassis = "{{.chassisIndex}}"
bus = "pcie.0"
addr = "0x2.0x{{.pcieAddr}}"

[device "dev-lxd_{{.devName}}"]
driver = "virtio-net-pci"
netdev = "lxd_{{.devName}}"
mac = "{{.devHwaddr}}"
bus = "qemu_pcie{{.chassisIndex}}"
addr = "0x0"
bootindex = "{{.bootIndex}}"
`))

// Devices use "lxd_" prefix indicating that this is a user named device.
var qemuNetDevTapTun = template.Must(qemuDevTapCommon.New("qemuNetDevTapTun").Parse(`
# Network card ("{{.devName}}" device)
[netdev "lxd_{{.devName}}"]
type = "tap"
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
