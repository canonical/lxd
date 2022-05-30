package drivers

import (
	"fmt"
	"strings"
	"text/template"
)

type cfgEntry struct {
	key   string
	value string
}

type cfgSection struct {
	name    string
	comment string
	entries []cfgEntry
}

func qemuAppendSections(sb *strings.Builder, sections ...cfgSection) {
	for _, section := range sections {
		if section.comment != "" {
			sb.WriteString(fmt.Sprintf("# %s\n", section.comment))
		}

		sb.WriteString(fmt.Sprintf("[%s]\n", section.name))

		for _, entry := range section.entries {
			value := entry.value
			if value != "" {
				sb.WriteString(fmt.Sprintf("%s = \"%s\"\n", entry.key, value))
			}
		}

		sb.WriteString("\n")
	}
}

type qemuBaseOpts struct {
	architecture string
}

func qemuBaseSections(opts *qemuBaseOpts) []cfgSection {
	machineType := ""
	gicVersion := ""
	capLargeDecr := ""

	switch opts.architecture {
	case "x86_64":
		machineType = "q35"
	case "aarch64":
		machineType = "virt"
		gicVersion = "max"
	case "ppc64le":
		machineType = "pseries"
		capLargeDecr = "off"
	case "s390x":
		machineType = "s390-ccw-virtio"
	}

	sections := []cfgSection{{
		name:    "machine",
		comment: "Machine",
		entries: []cfgEntry{
			{key: "graphics", value: "off"},
			{key: "type", value: machineType},
			{key: "gic-version", value: gicVersion},
			{key: "cap-large-decr", value: capLargeDecr},
			{key: "accel", value: "kvm"},
			{key: "usb", value: "off"},
		},
	}}

	if opts.architecture == "x86_64" {
		sections = append(sections, []cfgSection{{
			name: "global",
			entries: []cfgEntry{
				{key: "driver", value: "ICH9-LPC"},
				{key: "property", value: "disable_s3"},
				{key: "value", value: "1"},
			},
		}, {
			name: "global",
			entries: []cfgEntry{
				{key: "driver", value: "ICH9-LPC"},
				{key: "property", value: "disable_s4"},
				{key: "value", value: "1"},
			},
		}}...)
	}

	return append(
		sections,
		cfgSection{
			name:    "boot-opts",
			entries: []cfgEntry{{key: "strict", value: "on"}},
		})
}

type qemuMemoryOpts struct {
	memSizeMB int64
}

func qemuMemorySections(opts *qemuMemoryOpts) []cfgSection {
	return []cfgSection{{
		name:    "memory",
		comment: "Memory",
		entries: []cfgEntry{{key: "size", value: fmt.Sprintf("%dM", opts.memSizeMB)}},
	}}
}

type qemuDevOpts struct {
	busName       string
	devBus        string
	devAddr       string
	multifunction bool
}

type qemuDevEntriesOpts struct {
	dev     qemuDevOpts
	pciName string
	ccwName string
}

func qemuDeviceEntries(opts *qemuDevEntriesOpts) []cfgEntry {
	entries := []cfgEntry{}

	if opts.dev.busName == "pci" || opts.dev.busName == "pcie" {
		entries = append(entries, []cfgEntry{
			{key: "driver", value: opts.pciName},
			{key: "bus", value: opts.dev.devBus},
			{key: "addr", value: opts.dev.devAddr},
		}...)
	} else if opts.dev.busName == "ccw" {
		entries = append(entries, cfgEntry{key: "driver", value: opts.ccwName})
	}

	if opts.dev.multifunction {
		entries = append(entries, cfgEntry{key: "multifunction", value: "on"})
	}

	return entries
}

type qemuSerialOpts struct {
	dev              qemuDevOpts
	charDevName      string
	ringbufSizeBytes int
}

func qemuSerialSections(opts *qemuSerialOpts) []cfgSection {
	entriesOpts := qemuDevEntriesOpts{
		dev:     opts.dev,
		pciName: "virtio-serial-pci",
		ccwName: "virtio-serial-ccw",
	}

	return []cfgSection{{
		name:    `device "dev-qemu_serial"`,
		comment: "Virtual serial bus",
		entries: qemuDeviceEntries(&entriesOpts),
	}, {
		name:    fmt.Sprintf(`chardev "%s"`, opts.charDevName),
		comment: "LXD serial identifier",
		entries: []cfgEntry{
			{key: "backend", value: "ringbuf"},
			{key: "size", value: fmt.Sprintf("%dB", opts.ringbufSizeBytes)}},
	}, {
		name: `device "qemu_serial"`,
		entries: []cfgEntry{
			{key: "driver", value: "virtserialport"},
			{key: "name", value: "org.linuxcontainers.lxd"},
			{key: "chardev", value: opts.charDevName},
			{key: "bus", value: "dev-qemu_serial.0"},
		},
	}, {
		name:    `chardev "qemu_spice-chardev"`,
		comment: "Spice agent",
		entries: []cfgEntry{
			{key: "backend", value: "spicevmc"},
			{key: "name", value: "vdagent"},
		},
	}, {
		name: `device "qemu_spice"`,
		entries: []cfgEntry{
			{key: "driver", value: "virtserialport"},
			{key: "name", value: "com.redhat.spice.0"},
			{key: "chardev", value: "qemu_spice-chardev"},
			{key: "bus", value: "dev-qemu_serial.0"},
		},
	}, {
		name:    `chardev "qemu_spicedir-chardev"`,
		comment: "Spice folder",
		entries: []cfgEntry{
			{key: "backend", value: "spiceport"},
			{key: "name", value: "org.spice-space.webdav.0"},
		},
	}, {
		name: `device "qemu_spicedir"`,
		entries: []cfgEntry{
			{key: "driver", value: "virtserialport"},
			{key: "name", value: "org.spice-space.webdav.0"},
			{key: "chardev", value: "qemu_spicedir-chardev"},
			{key: "bus", value: "dev-qemu_serial.0"},
		},
	}}
}

type qemuPCIeOpts struct {
	portName      string
	index         int
	devAddr       string
	multifunction bool
}

func qemuPCIeSections(opts *qemuPCIeOpts) []cfgSection {
	entries := []cfgEntry{
		{key: "driver", value: "pcie-root-port"},
		{key: "bus", value: "pcie.0"},
		{key: "addr", value: opts.devAddr},
		{key: "chassis", value: fmt.Sprintf("%d", opts.index)},
	}

	if opts.multifunction {
		entries = append(entries, cfgEntry{key: "multifunction", value: "on"})
	}

	return []cfgSection{{
		name:    fmt.Sprintf(`device "%s"`, opts.portName),
		entries: entries,
	}}
}

func qemuSCSISections(opts *qemuDevOpts) []cfgSection {
	entriesOpts := qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-scsi-pci",
		ccwName: "virtio-scsi-ccw",
	}

	return []cfgSection{{
		name:    `device "qemu_scsi"`,
		comment: "SCSI controller",
		entries: qemuDeviceEntries(&entriesOpts),
	}}
}

func qemuBalloonSections(opts *qemuDevOpts) []cfgSection {
	entriesOpts := qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-balloon-pci",
		ccwName: "virtio-balloon-ccw",
	}

	return []cfgSection{{
		name:    `device "qemu_balloon"`,
		comment: "Balloon driver",
		entries: qemuDeviceEntries(&entriesOpts),
	}}
}

func qemuRNGSections(opts *qemuDevOpts) []cfgSection {
	entriesOpts := qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-rng-pci",
		ccwName: "virtio-rng-ccw",
	}

	return []cfgSection{{
		name:    `object "qemu_rng"`,
		comment: "Random number generator",
		entries: []cfgEntry{
			{key: "qom-type", value: "rng-random"},
			{key: "filename", value: "/dev/urandom"},
		},
	}, {
		name: `device "dev-qemu_rng"`,
		entries: append(qemuDeviceEntries(&entriesOpts),
			cfgEntry{key: "rng", value: "qemu_rng"}),
	}}
}

type qemuVsockOpts struct {
	dev     qemuDevOpts
	vsockID int
}

func qemuVsockSections(opts *qemuVsockOpts) []cfgSection {
	entriesOpts := qemuDevEntriesOpts{
		dev:     opts.dev,
		pciName: "vhost-vsock-pci",
		ccwName: "vhost-vsock-ccw",
	}

	return []cfgSection{{
		name:    `device "qemu_vsock"`,
		comment: "Vsock",
		entries: append(qemuDeviceEntries(&entriesOpts),
			cfgEntry{key: "guest-cid", value: fmt.Sprintf("%d", opts.vsockID)}),
	}}
}

type qemuGpuOpts struct {
	dev          qemuDevOpts
	architecture string
}

func qemuGPUSections(opts *qemuGpuOpts) []cfgSection {
	var pciName string

	if opts.architecture == "x86_64" {
		pciName = "virtio-vga"
	} else {
		pciName = "virtio-gpu-pci"
	}

	entriesOpts := qemuDevEntriesOpts{
		dev:     opts.dev,
		pciName: pciName,
		ccwName: "virtio-gpu-ccw",
	}

	return []cfgSection{{
		name:    `device "qemu_gpu"`,
		comment: "GPU",
		entries: qemuDeviceEntries(&entriesOpts),
	}}
}

func qemuKeyboardSections(opts *qemuDevOpts) []cfgSection {
	entriesOpts := qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-keyboard-pci",
		ccwName: "virtio-keyboard-ccw",
	}

	return []cfgSection{{
		name:    `device "qemu_keyboard"`,
		comment: "Input",
		entries: qemuDeviceEntries(&entriesOpts),
	}}
}

func qemuTabletSections(opts *qemuDevOpts) []cfgSection {
	entriesOpts := qemuDevEntriesOpts{
		dev:     *opts,
		pciName: "virtio-tablet-pci",
		ccwName: "virtio-tablet-ccw",
	}

	return []cfgSection{{
		name:    `device "qemu_tablet"`,
		comment: "Input",
		entries: qemuDeviceEntries(&entriesOpts),
	}}
}

type qemuNumaEntry struct {
	node   uint64
	socket uint64
	core   uint64
	thread uint64
}

type qemuCPUOpts struct {
	architecture        string
	cpuCount            int
	cpuSockets          int
	cpuCores            int
	cpuThreads          int
	cpuNumaNodes        []uint64
	cpuNumaMapping      []qemuNumaEntry
	cpuNumaHostNodes    []uint64
	hugepages           string
	memory              int64
	qemuMemObjectFormat string
}

func qemuCPUNumaHostNode(opts *qemuCPUOpts, index int) []cfgSection {
	entries := []cfgEntry{}

	if opts.hugepages != "" {
		entries = append(entries, []cfgEntry{
			{key: "qom-type", value: "memory-backend-file"},
			{key: "mem-path", value: opts.hugepages},
			{key: "prealloc", value: "on"},
			{key: "discard-data", value: "on"},
		}...)
	} else {
		entries = append(entries, cfgEntry{key: "qom-type", value: "memory-backend-memfd"})
	}

	entries = append(entries, cfgEntry{key: "size", value: fmt.Sprintf("%dM", opts.memory)})

	return []cfgSection{{
		name:    fmt.Sprintf("object \"mem%d\"", index),
		entries: entries,
	}, {
		name: "numa",
		entries: []cfgEntry{
			{key: "type", value: "node"},
			{key: "nodeid", value: fmt.Sprintf("%d", index)},
			{key: "memdev", value: fmt.Sprintf("mem%d", index)},
		},
	}}
}

func qemuCPUSections(opts *qemuCPUOpts) []cfgSection {
	sections := []cfgSection{{
		name:    "smp-opts",
		comment: "CPU",
		entries: []cfgEntry{
			{key: "cpus", value: fmt.Sprintf("%d", opts.cpuCount)},
			{key: "sockets", value: fmt.Sprintf("%d", opts.cpuSockets)},
			{key: "cores", value: fmt.Sprintf("%d", opts.cpuCores)},
			{key: "threads", value: fmt.Sprintf("%d", opts.cpuThreads)},
		},
	}}

	if opts.architecture != "x86_64" {
		return sections
	}

	share := cfgEntry{key: "share", value: "on"}

	if len(opts.cpuNumaHostNodes) == 0 {
		// add one mem and one numa sections with index 0
		numaHostNodeSections := qemuCPUNumaHostNode(opts, 0)
		// unconditionally append "share = "on" to the [object "mem0"] section
		numaHostNodeSections[0].entries = append(numaHostNodeSections[0].entries, share)
		return append(sections, numaHostNodeSections...)
	}

	for index, element := range opts.cpuNumaHostNodes {
		numaHostNodeSections := qemuCPUNumaHostNode(opts, index)

		extraMemEntries := []cfgEntry{{key: "policy", value: "bind"}}

		if opts.hugepages != "" {
			// append share = "on" only if hugepages is set
			extraMemEntries = append(extraMemEntries, share)
		}

		var hostNodesKey string
		if opts.qemuMemObjectFormat == "indexed" {
			hostNodesKey = "host-nodes.0"
		} else {
			hostNodesKey = "host-nodes"
		}

		hostNode := cfgEntry{key: hostNodesKey, value: fmt.Sprintf("%d", element)}
		extraMemEntries = append(extraMemEntries, hostNode)
		// append the extra entries to the [object "mem{{idx}}"] section
		numaHostNodeSections[0].entries = append(numaHostNodeSections[0].entries, extraMemEntries...)
		sections = append(sections, numaHostNodeSections...)
	}

	for _, numa := range opts.cpuNumaMapping {
		sections = append(sections, cfgSection{
			name: "numa",
			entries: []cfgEntry{
				{key: "type", value: "cpu"},
				{key: "node-id", value: fmt.Sprintf("%d", numa.socket)},
				{key: "core-id", value: fmt.Sprintf("%d", numa.core)},
				{key: "thread-id", value: fmt.Sprintf("%d", numa.thread)},
			},
		})
	}

	return sections
}

type qemuControlSocketOpts struct {
	path string
}

func qemuControlSocketSections(opts *qemuControlSocketOpts) []cfgSection {
	return []cfgSection{{
		name:    `chardev "monitor"`,
		comment: "Qemu control",
		entries: []cfgEntry{
			{key: "backend", value: "socket"},
			{key: "path", value: opts.path},
			{key: "server", value: "on"},
			{key: "wait", value: "off"},
		},
	}, {
		name: "mon",
		entries: []cfgEntry{
			{key: "chardev", value: "monitor"},
			{key: "mode", value: "control"},
		},
	}}
}

type qemuConsoleOpts struct {
	path string
}

func qemuConsoleSections(opts *qemuConsoleOpts) []cfgSection {
	return []cfgSection{{
		name:    `chardev "console"`,
		comment: "Console",
		entries: []cfgEntry{
			{key: "backend", value: "socket"},
			{key: "path", value: opts.path},
			{key: "server", value: "on"},
			{key: "wait", value: "off"},
		},
	}}
}

type qemuDriveFirmwareOpts struct {
	roPath    string
	nvramPath string
}

func qemuDriveFirmwareSections(opts *qemuDriveFirmwareOpts) []cfgSection {
	return []cfgSection{{
		name:    "drive",
		comment: "Firmware (read only)",
		entries: []cfgEntry{
			{key: "file", value: opts.roPath},
			{key: "if", value: "pflash"},
			{key: "format", value: "raw"},
			{key: "unit", value: "0"},
			{key: "readonly", value: "on"},
		},
	}, {
		name:    "drive",
		comment: "Firmware settings (writable)",
		entries: []cfgEntry{
			{key: "file", value: opts.nvramPath},
			{key: "if", value: "pflash"},
			{key: "format", value: "raw"},
			{key: "unit", value: "1"},
		},
	}}
}

type qemuHostDriveOpts struct {
	dev           qemuDevOpts
	name          string
	nameSuffix    string
	comment       string
	fsdriver      string
	mountTag      string
	securityModel string
	path          string
	sockFd        string
	readonly      bool
	protocol      string
}

func qemuHostDriveSections(opts *qemuHostDriveOpts) []cfgSection {
	var extraDeviceEntries []cfgEntry
	var driveSection cfgSection
	deviceOpts := qemuDevEntriesOpts{dev: opts.dev}

	if opts.protocol == "9p" {
		var readonly string
		if opts.readonly {
			readonly = "on"
		} else {
			readonly = "off"
		}

		driveSection = cfgSection{
			name:    fmt.Sprintf(`fsdev "%s"`, opts.name),
			comment: opts.comment,
			entries: []cfgEntry{
				{key: "fsdriver", value: opts.fsdriver},
				{key: "sock_fd", value: opts.sockFd},
				{key: "security_model", value: opts.securityModel},
				{key: "readonly", value: readonly},
				{key: "path", value: opts.path},
			},
		}

		deviceOpts.pciName = "virtio-9p-pci"
		deviceOpts.ccwName = "virtio-9p-ccw"

		extraDeviceEntries = []cfgEntry{
			{key: "mount_tag", value: opts.mountTag},
			{key: "fsdev", value: opts.name},
		}

	} else if opts.protocol == "virtio-fs" {

		driveSection = cfgSection{
			name:    fmt.Sprintf(`chardev "%s"`, opts.name),
			comment: opts.comment,
			entries: []cfgEntry{
				{key: "backend", value: "socket"},
				{key: "path", value: opts.path},
			},
		}

		deviceOpts.pciName = "vhost-user-fs-pci"
		deviceOpts.ccwName = "vhost-user-fs-ccw"

		extraDeviceEntries = []cfgEntry{
			{key: "tag", value: opts.mountTag},
			{key: "chardev", value: opts.name},
		}
	} else {
		return []cfgSection{}
	}

	return []cfgSection{
		driveSection,
		{
			name:    fmt.Sprintf(`device "dev-%s%s-%s"`, opts.name, opts.nameSuffix, opts.protocol),
			entries: append(qemuDeviceEntries(&deviceOpts), extraDeviceEntries...),
		},
	}
}

type qemuDriveConfigOpts struct {
	dev      qemuDevOpts
	protocol string
	path     string
}

func qemuDriveConfigSections(opts *qemuDriveConfigOpts) []cfgSection {
	return qemuHostDriveSections(&qemuHostDriveOpts{
		dev: opts.dev,
		// Devices use "qemu_" prefix indicating that this is a internally named device.
		name:          "qemu_config",
		nameSuffix:    "-drive",
		comment:       fmt.Sprintf("Config drive (%s)", opts.protocol),
		mountTag:      "config",
		protocol:      opts.protocol,
		fsdriver:      "local",
		readonly:      true,
		securityModel: "none",
		path:          opts.path,
	})
}

type qemuDriveDirOpts struct {
	dev      qemuDevOpts
	devName  string
	mountTag string
	path     string
	protocol string
	proxyFD  int
	readonly bool
}

func qemuDriveDirSections(opts *qemuDriveDirOpts) []cfgSection {
	return qemuHostDriveSections(&qemuHostDriveOpts{
		dev: opts.dev,
		// Devices use "lxd_" prefix indicating that this is a user named device.
		name:     fmt.Sprintf("lxd_%s", opts.devName),
		comment:  fmt.Sprintf("%s drive (%s)", opts.devName, opts.protocol),
		mountTag: opts.mountTag,
		protocol: opts.protocol,
		fsdriver: "proxy",
		readonly: opts.readonly,
		path:     opts.path,
		sockFd:   fmt.Sprintf("%d", opts.proxyFD),
	})
}

type qemuPCIPhysicalOpts struct {
	dev         qemuDevOpts
	devName     string
	pciSlotName string
	bootIndex   int
}

func qemuPCIPhysicalSections(opts *qemuPCIPhysicalOpts) []cfgSection {
	deviceOpts := qemuDevEntriesOpts{
		dev:     opts.dev,
		pciName: "vfio-pci",
		ccwName: "vfio-ccw",
	}

	entries := append(qemuDeviceEntries(&deviceOpts), []cfgEntry{
		{key: "host", value: opts.pciSlotName},
		{key: "bootIndex", value: fmt.Sprintf("%d", opts.bootIndex)},
	}...)

	return []cfgSection{{
		// Devices use "lxd_" prefix indicating that this is a user named device.
		name:    fmt.Sprintf(`device "dev-lxd_%s"`, opts.devName),
		comment: fmt.Sprintf(`PCI card ("%s" device)`, opts.devName),
		entries: entries,
	}}
}

type qemuGPUDevPhysicalOpts struct {
	dev         qemuDevOpts
	devName     string
	pciSlotName string
	vgpu        string
	vga         bool
}

func qemuGPUDevPhysicalSections(opts *qemuGPUDevPhysicalOpts) []cfgSection {
	deviceOpts := qemuDevEntriesOpts{
		dev:     opts.dev,
		pciName: "vfio-pci",
		ccwName: "vfio-ccw",
	}

	entries := qemuDeviceEntries(&deviceOpts)

	if opts.vgpu != "" {
		sysfsdev := fmt.Sprintf("/sys/bus/mdev/devices/%s", opts.vgpu)
		entries = append(entries, cfgEntry{key: "sysfsdev", value: sysfsdev})
	} else {
		entries = append(entries, cfgEntry{key: "host", value: opts.pciSlotName})
	}

	if opts.vga {
		entries = append(entries, cfgEntry{key: "x-vga", value: "on"})
	}

	return []cfgSection{{
		// Devices use "lxd_" prefix indicating that this is a user named device.
		name:    fmt.Sprintf(`device "dev-lxd_%s"`, opts.devName),
		comment: fmt.Sprintf(`GPU card ("%s" device)`, opts.devName),
		entries: entries,
	}}
}

var qemuUSB = template.Must(template.New("qemuUSB").Parse(`
# USB controller
[device "qemu_usb"]
driver = "qemu-xhci"
bus = "{{.devBus}}"
addr = "{{.devAddr}}"
p2 = "{{.ports}}"
p3 = "{{.ports}}"
{{if .multifunction -}}
multifunction = "on"
{{- end }}

[chardev "qemu_spice-usb-chardev1"]
  backend = "spicevmc"
  name = "usbredir"

[chardev "qemu_spice-usb-chardev2"]
  backend = "spicevmc"
  name = "usbredir"

[chardev "qemu_spice-usb-chardev3"]
  backend = "spicevmc"
  name = "usbredir"

[device "qemu_spice-usb1"]
  driver = "usb-redir"
  chardev = "qemu_spice-usb-chardev1"

[device "qemu_spice-usb2"]
  driver = "usb-redir"
  chardev = "qemu_spice-usb-chardev2"

[device "qemu_spice-usb3"]
  driver = "usb-redir"
  chardev = "qemu_spice-usb-chardev3"
`))

var qemuTPM = template.Must(template.New("qemuTPM").Parse(`
[chardev "qemu_tpm-chardev_{{.devName}}"]
backend = "socket"
path = "{{.path}}"

[tpmdev "qemu_tpm-tpmdev_{{.devName}}"]
type = "emulator"
chardev = "qemu_tpm-chardev_{{.devName}}"

[device "dev-lxd_{{.devName}}"]
driver = "tpm-crb"
tpmdev = "qemu_tpm-tpmdev_{{.devName}}"
`))
