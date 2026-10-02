package drivers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/shared/osarch"
)

func TestQemuArchBinary(t *testing.T) {
	tests := []struct {
		name       string
		hostArch   int
		guestArch  int
		wantBinary string
		wantBus    string
		wantTCG    bool
		wantErr    bool
	}{
		{
			name:       "native x86_64",
			hostArch:   osarch.ARCH_64BIT_INTEL_X86,
			guestArch:  osarch.ARCH_64BIT_INTEL_X86,
			wantBinary: "qemu-system-x86_64",
			wantBus:    "pcie",
		},
		{
			name:       "native aarch64",
			hostArch:   osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN,
			guestArch:  osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN,
			wantBinary: "qemu-system-aarch64",
			wantBus:    "pcie",
		},
		{
			name:       "armv7l on aarch64 host keeps aarch64 binary and KVM",
			hostArch:   osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN,
			guestArch:  osarch.ARCH_32BIT_ARMV7_LITTLE_ENDIAN,
			wantBinary: "qemu-system-aarch64",
			wantBus:    "pcie",
		},
		{
			name:       "native riscv64",
			hostArch:   osarch.ARCH_64BIT_RISCV_LITTLE_ENDIAN,
			guestArch:  osarch.ARCH_64BIT_RISCV_LITTLE_ENDIAN,
			wantBinary: "qemu-system-riscv64",
			wantBus:    "pcie",
		},
		{
			name:       "riscv64 on x86_64 host is emulated",
			hostArch:   osarch.ARCH_64BIT_INTEL_X86,
			guestArch:  osarch.ARCH_64BIT_RISCV_LITTLE_ENDIAN,
			wantBinary: "qemu-system-riscv64",
			wantBus:    "pcie",
			wantTCG:    true,
		},
		{
			name:       "armv7l on x86_64 host is emulated",
			hostArch:   osarch.ARCH_64BIT_INTEL_X86,
			guestArch:  osarch.ARCH_32BIT_ARMV7_LITTLE_ENDIAN,
			wantBinary: "qemu-system-arm",
			wantBus:    "pcie",
			wantTCG:    true,
		},
		{
			name:       "aarch64 on x86_64 host is not emulated",
			hostArch:   osarch.ARCH_64BIT_INTEL_X86,
			guestArch:  osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN,
			wantBinary: "qemu-system-aarch64",
			wantBus:    "pcie",
		},
		{
			name:       "riscv64 on aarch64 host is not emulated",
			hostArch:   osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN,
			guestArch:  osarch.ARCH_64BIT_RISCV_LITTLE_ENDIAN,
			wantBinary: "qemu-system-riscv64",
			wantBus:    "pcie",
		},
		{
			name:       "ppc64le uses pci bus",
			hostArch:   osarch.ARCH_64BIT_POWERPC_LITTLE_ENDIAN,
			guestArch:  osarch.ARCH_64BIT_POWERPC_LITTLE_ENDIAN,
			wantBinary: "qemu-system-ppc64",
			wantBus:    "pci",
		},
		{
			name:       "s390x uses ccw bus",
			hostArch:   osarch.ARCH_64BIT_S390_BIG_ENDIAN,
			guestArch:  osarch.ARCH_64BIT_S390_BIG_ENDIAN,
			wantBinary: "qemu-system-s390x",
			wantBus:    "ccw",
		},
		{
			name:      "i686 is not supported",
			hostArch:  osarch.ARCH_64BIT_INTEL_X86,
			guestArch: osarch.ARCH_32BIT_INTEL_X86,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			binary, bus, err := qemuArchBinary(tc.hostArch, tc.guestArch)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tc.wantBinary, binary)
			assert.Equal(t, tc.wantBus, bus)
			assert.Equal(t, tc.wantTCG, qemuUseTCG(tc.hostArch, tc.guestArch))
		})
	}
}
