package drivers

import (
	"regexp"
	"strings"
	"testing"
)

func TestQemuConfigTemplates(t *testing.T) {
	indent := regexp.MustCompile(`(?m)^[ \t]+`)

	stringifySections := func(sections ...cfgSection) string {
		sb := &strings.Builder{}
		qemuAppendSections(sb, sections...)

		return sb.String()
	}

	normalize := func(s string) string {
		return strings.TrimSpace(indent.ReplaceAllString(s, "$1"))
	}

	t.Run("qemu_base", func(t *testing.T) {
		type args struct{ architecture string }
		testCases := []struct {
			args     args
			expected string
		}{{
			args{"x86_64"},
			`# Machine
			[machine]
			graphics = "off"
			type = "q35"
			accel = "kvm"
			usb = "off"

			[global]
			driver = "ICH9-LPC"
			property = "disable_s3"
			value = "1"

			[global]
			driver = "ICH9-LPC"
			property = "disable_s4"
			value = "1"

			[boot-opts]
			strict = "on"`,
		}, {
			args{"aarch64"},
			`# Machine
			[machine]
			graphics = "off"
			type = "virt"
			gic-version = "max"
			accel = "kvm"
			usb = "off"

			[boot-opts]
			strict = "on"`,
		}, {
			args{"ppc64le"},
			`# Machine
			[machine]
			graphics = "off"
			type = "pseries"
			cap-large-decr = "off"
			accel = "kvm"
			usb = "off"

			[boot-opts]
			strict = "on"`,
		}, {
			args{"s390x"},
			`# Machine
			[machine]
			graphics = "off"
			type = "s390-ccw-virtio"
			accel = "kvm"
			usb = "off"

			[boot-opts]
			strict = "on"`,
		}}

		for _, tc := range testCases {
			t.Run(tc.expected, func(t *testing.T) {
				sections := qemuBaseSections(tc.args.architecture)
				actual := normalize(stringifySections(sections...))
				expected := normalize(tc.expected)
				if actual != expected {
					t.Errorf("Expected: %s. Got: %s", expected, actual)
				}
			})
		}
	})

	t.Run("qemu_memory", func(t *testing.T) {
		testCases := []struct {
			opts     qemuMemoryOpts
			expected string
		}{{
			qemuMemoryOpts{4096},
			`# Memory
			[memory]
			size = "4096M"`,
		}, {
			qemuMemoryOpts{8192},
			`# Memory
			[memory]
			size = "8192M"`,
		}}
		for _, tc := range testCases {
			t.Run(tc.expected, func(t *testing.T) {
				sections := qemuMemorySections(&tc.opts)
				actual := normalize(stringifySections(sections...))
				expected := normalize(tc.expected)
				if actual != expected {
					t.Errorf("Expected: %s. Got: %s", expected, actual)
				}
			})
		}
	})

	t.Run("qemu_serial", func(t *testing.T) {
		testCases := []struct {
			opts     qemuSerialOpts
			expected string
		}{{
			qemuSerialOpts{qemuDevOpts{"pci", "qemu_pcie0", "00.5", false}, "qemu_serial-chardev", 32},
			`# Virtual serial bus
			[device "dev-qemu_serial"]
			driver = "virtio-serial-pci"
			bus = "qemu_pcie0"
			addr = "00.5"

			# LXD serial identifier
			[chardev "qemu_serial-chardev"]
			backend = "ringbuf"
			size = "32B"

			[device "qemu_serial"]
			driver = "virtserialport"
			name = "org.linuxcontainers.lxd"
			chardev = "qemu_serial-chardev"
			bus = "dev-qemu_serial.0"

			# Spice agent
			[chardev "qemu_spice-chardev"]
			backend = "spicevmc"
			name = "vdagent"

			[device "qemu_spice"]
			driver = "virtserialport"
			name = "com.redhat.spice.0"
			chardev = "qemu_spice-chardev"
			bus = "dev-qemu_serial.0"

			# Spice folder
			[chardev "qemu_spicedir-chardev"]
			backend = "spiceport"
			name = "org.spice-space.webdav.0"

			[device "qemu_spicedir"]
			driver = "virtserialport"
			name = "org.spice-space.webdav.0"
			chardev = "qemu_spicedir-chardev"
			bus = "dev-qemu_serial.0"
			`,
		}}
		for _, tc := range testCases {
			t.Run(tc.expected, func(t *testing.T) {
				sections := qemuSerialSections(&tc.opts)
				actual := normalize(stringifySections(sections...))
				expected := normalize(tc.expected)
				if actual != expected {
					t.Errorf("Expected: %s. Got: %s", expected, actual)
				}
			})
		}
	})

	t.Run("qemu_pcie", func(t *testing.T) {
		testCases := []struct {
			opts     qemuPCIeOpts
			expected string
		}{{
			qemuPCIeOpts{"qemu_pcie0", 0, "1.0", true},
			`[device "qemu_pcie0"]
			driver = "pcie-root-port"
			bus = "pcie.0"
			addr = "1.0"
			chassis = "0"
			multifunction = "on"
			`,
		}, {
			qemuPCIeOpts{"qemu_pcie2", 3, "2.0", false},
			`[device "qemu_pcie2"]
			driver = "pcie-root-port"
			bus = "pcie.0"
			addr = "2.0"
			chassis = "3"
			`,
		}}
		for _, tc := range testCases {
			t.Run(tc.expected, func(t *testing.T) {
				sections := qemuPCIeSections(&tc.opts)
				actual := normalize(stringifySections(sections...))
				expected := normalize(tc.expected)
				if actual != expected {
					t.Errorf("Expected: %s. Got: %s", expected, actual)
				}
			})
		}
	})

	t.Run("qemu_scsi", func(t *testing.T) {
		testCases := []struct {
			opts     qemuDevOpts
			expected string
		}{{
			qemuDevOpts{"pci", "qemu_pcie1", "00.0", false},
			`# SCSI controller
			[device "qemu_scsi"]
			driver = "virtio-scsi-pci"
			bus = "qemu_pcie1"
			addr = "00.0"
			`,
		}, {
			qemuDevOpts{"ccw", "qemu_pcie2", "00.2", true},
			`# SCSI controller
			[device "qemu_scsi"]
			driver = "virtio-scsi-ccw"
			multifunction = "on"
			`,
		}}
		for _, tc := range testCases {
			t.Run(tc.expected, func(t *testing.T) {
				sections := qemuSCSISections(&tc.opts)
				actual := normalize(stringifySections(sections...))
				expected := normalize(tc.expected)
				if actual != expected {
					t.Errorf("Expected: %s. Got: %s", expected, actual)
				}
			})
		}
	})

	t.Run("qemu_balloon", func(t *testing.T) {
		testCases := []struct {
			opts     qemuDevOpts
			expected string
		}{{
			qemuDevOpts{"pcie", "qemu_pcie0", "00.0", true},
			`# Balloon driver
			[device "qemu_balloon"]
			driver = "virtio-balloon-pci"
			bus = "qemu_pcie0"
			addr = "00.0"
			multifunction = "on"
			`,
		}, {
			qemuDevOpts{"ccw", "qemu_pcie0", "00.0", false},
			`# Balloon driver
			[device "qemu_balloon"]
			driver = "virtio-balloon-ccw"
			`,
		}}
		for _, tc := range testCases {
			t.Run(tc.expected, func(t *testing.T) {
				sections := qemuBalloonSections(&tc.opts)
				actual := normalize(stringifySections(sections...))
				expected := normalize(tc.expected)
				if actual != expected {
					t.Errorf("Expected: %s. Got: %s", expected, actual)
				}
			})
		}
	})

	t.Run("qemu_rng", func(t *testing.T) {
		testCases := []struct {
			opts     qemuDevOpts
			expected string
		}{{
			qemuDevOpts{"pci", "qemu_pcie0", "00.1", false},
			`# Random number generator
			[object "qemu_rng"]
			qom-type = "rng-random"
			filename = "/dev/urandom"

			[device "dev-qemu_rng"]
			driver = "virtio-rng-pci"
			bus = "qemu_pcie0"
			addr = "00.1"
			rng = "qemu_rng"
			`,
		}, {
			qemuDevOpts{"ccw", "qemu_pcie0", "00.1", true},
			`# Random number generator
			[object "qemu_rng"]
			qom-type = "rng-random"
			filename = "/dev/urandom"

			[device "dev-qemu_rng"]
			driver = "virtio-rng-ccw"
			multifunction = "on"
			rng = "qemu_rng"
			`,
		}}
		for _, tc := range testCases {
			t.Run(tc.expected, func(t *testing.T) {
				sections := qemuRNGSections(&tc.opts)
				actual := normalize(stringifySections(sections...))
				expected := normalize(tc.expected)
				if actual != expected {
					t.Errorf("Expected: %s. Got: %s", expected, actual)
				}
			})
		}
	})

	t.Run("qemu_vsock", func(t *testing.T) {
		testCases := []struct {
			opts     qemuVsockOpts
			expected string
		}{{
			qemuVsockOpts{qemuDevOpts{"pcie", "qemu_pcie0", "00.4", true}, 14},
			`# Vsock
			[device "qemu_vsock"]
			driver = "vhost-vsock-pci"
			bus = "qemu_pcie0"
			addr = "00.4"
			multifunction = "on"
			guest-cid = "14"
			`,
		}, {
			qemuVsockOpts{qemuDevOpts{"ccw", "qemu_pcie0", "00.4", false}, 3},
			`# Vsock
			[device "qemu_vsock"]
			driver = "vhost-vsock-ccw"
			guest-cid = "3"
			`,
		}}
		for _, tc := range testCases {
			t.Run(tc.expected, func(t *testing.T) {
				sections := qemuVsockSections(&tc.opts)
				actual := normalize(stringifySections(sections...))
				expected := normalize(tc.expected)
				if actual != expected {
					t.Errorf("Expected: %s. Got: %s", expected, actual)
				}
			})
		}
	})

	t.Run("qemu_gpu", func(t *testing.T) {
		testCases := []struct {
			opts     qemuGpuOpts
			expected string
		}{{
			qemuGpuOpts{qemuDevOpts{"pci", "qemu_pcie3", "00.0", true}, "x86_64"},
			`# GPU
			[device "qemu_gpu"]
			driver = "virtio-vga"
			bus = "qemu_pcie3"
			addr = "00.0"
			multifunction = "on"`,
		}, {
			qemuGpuOpts{qemuDevOpts{"pci", "qemu_pci3", "00.1", false}, "otherArch"},
			`# GPU
			[device "qemu_gpu"]
			driver = "virtio-gpu-pci"
			bus = "qemu_pci3"
			addr = "00.1"`,
		}, {
			qemuGpuOpts{qemuDevOpts{"ccw", "devBus", "busAddr", true}, "arch"},
			`# GPU
			[device "qemu_gpu"]
			driver = "virtio-gpu-ccw"
			multifunction = "on"`,
		}, {
			qemuGpuOpts{qemuDevOpts{"ccw", "devBus", "busAddr", false}, "x86_64"},
			`# GPU
			[device "qemu_gpu"]
			driver = "virtio-gpu-ccw"`,
		}}
		for _, tc := range testCases {
			t.Run(tc.expected, func(t *testing.T) {
				sections := qemuGPUSections(&tc.opts)
				actual := normalize(stringifySections(sections...))
				expected := normalize(tc.expected)
				if actual != expected {
					t.Errorf("Expected: %s. Got: %s", expected, actual)
				}
			})
		}
	})

	t.Run("qemu_keyboard", func(t *testing.T) {
		testCases := []struct {
			opts     qemuDevOpts
			expected string
		}{{
			qemuDevOpts{"pci", "qemu_pcie3", "00.0", false},
			`# Input
			[device "qemu_keyboard"]
			driver = "virtio-keyboard-pci"
			bus = "qemu_pcie3"
			addr = "00.0"`,
		}, {
			qemuDevOpts{"pcie", "qemu_pcie3", "00.0", true},
			`# Input
			[device "qemu_keyboard"]
			driver = "virtio-keyboard-pci"
			bus = "qemu_pcie3"
			addr = "00.0"
			multifunction = "on"`,
		}, {
			qemuDevOpts{"ccw", "qemu_pcie3", "00.0", false},
			`# Input
			[device "qemu_keyboard"]
			driver = "virtio-keyboard-ccw"`,
		}, {
			qemuDevOpts{"ccw", "qemu_pcie3", "00.0", true},
			`# Input
			[device "qemu_keyboard"]
			driver = "virtio-keyboard-ccw"
			multifunction = "on"`,
		}}
		for _, tc := range testCases {
			t.Run(tc.expected, func(t *testing.T) {
				sections := qemuKeyboardSections(&tc.opts)
				actual := normalize(stringifySections(sections...))
				expected := normalize(tc.expected)
				if actual != expected {
					t.Errorf("Expected: %s. Got: %s", expected, actual)
				}
			})
		}
	})
}
