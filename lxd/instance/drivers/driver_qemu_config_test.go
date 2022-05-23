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
}
