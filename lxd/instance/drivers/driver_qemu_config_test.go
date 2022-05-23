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
}
