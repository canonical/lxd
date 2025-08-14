package osarch

import (
	"testing"
)

func Test_ArchitectureName(t *testing.T) {
	tests := []struct {
		arch    int
		want    string
		wantErr bool
	}{
		{ARCH_32BIT_INTEL_X86, "i686", false},
		{ARCH_64BIT_INTEL_X86, "x86_64", false},
		{ARCH_32BIT_ARMV6_LITTLE_ENDIAN, "armv6l", false},
		{ARCH_32BIT_ARMV7_LITTLE_ENDIAN, "armv7l", false},
		{ARCH_32BIT_ARMV8_LITTLE_ENDIAN, "armv8l", false},
		{ARCH_64BIT_ARMV8_LITTLE_ENDIAN, "aarch64", false},
		{ARCH_32BIT_POWERPC_BIG_ENDIAN, "ppc", false},
		{ARCH_64BIT_POWERPC_BIG_ENDIAN, "ppc64", false},
		{ARCH_64BIT_POWERPC_LITTLE_ENDIAN, "ppc64le", false},
		{ARCH_32BIT_MIPS, "mips", false},
		{ARCH_64BIT_MIPS, "mips64", false},
		{ARCH_32BIT_RISCV_LITTLE_ENDIAN, "riscv32", false},
		{ARCH_64BIT_RISCV_LITTLE_ENDIAN, "riscv64", false},
		{ARCH_64BIT_LOONGARCH, "loongarch64", false},
		{ARCH_UNKNOWN, "unknown", true},
		{999, "unknown", true}, // Invalid architecture
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got, err := ArchitectureName(tt.arch)
			if (err != nil) != tt.wantErr {
				t.Errorf("ArchitectureName() error = %v, wantErr %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("ArchitectureName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_ArchitectureId(t *testing.T) {
	tests := []struct {
		name    string
		arch    string
		want    int
		wantErr bool
	}{
		{"i686", "i686", ARCH_32BIT_INTEL_X86, false},
		{"x86_64", "x86_64", ARCH_64BIT_INTEL_X86, false},
		{"armv6l", "armv6l", ARCH_32BIT_ARMV6_LITTLE_ENDIAN, false},
		{"armv7l", "armv7l", ARCH_32BIT_ARMV7_LITTLE_ENDIAN, false},
		{"armv8l", "armv8l", ARCH_32BIT_ARMV8_LITTLE_ENDIAN, false},
		{"aarch64", "aarch64", ARCH_64BIT_ARMV8_LITTLE_ENDIAN, false},
		{"ppc", "ppc", ARCH_32BIT_POWERPC_BIG_ENDIAN, false},
		{"ppc64", "ppc64", ARCH_64BIT_POWERPC_BIG_ENDIAN, false},
		{"ppc64le", "ppc64le", ARCH_64BIT_POWERPC_LITTLE_ENDIAN, false},
		{"mips", "mips", ARCH_32BIT_MIPS, false},
		{"mips64", "mips64", ARCH_64BIT_MIPS, false},
		{"riscv32", "riscv32", ARCH_32BIT_RISCV_LITTLE_ENDIAN, false},
		{"riscv64", "riscv64", ARCH_64BIT_RISCV_LITTLE_ENDIAN, false},
		{"loongarch64", "loongarch64", ARCH_64BIT_LOONGARCH, false},
		{"unknown_architecture", "unknown_architecture", ARCH_UNKNOWN, true}, // Invalid architecture
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ArchitectureId(tt.arch)
			if (err != nil) != tt.wantErr {
				t.Errorf("ArchitectureId() error = %v, wantErr %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("ArchitectureId() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_ArchitecturePersonality(t *testing.T) {
	tests := []struct {
		arch    int
		want    string
		wantErr bool
	}{
		{ARCH_32BIT_INTEL_X86, "linux32", false},
		{ARCH_64BIT_INTEL_X86, "linux64", false},
		{ARCH_32BIT_ARMV6_LITTLE_ENDIAN, "linux32", false},
		{ARCH_32BIT_ARMV7_LITTLE_ENDIAN, "linux32", false},
		{ARCH_32BIT_ARMV8_LITTLE_ENDIAN, "linux32", false},
		{ARCH_64BIT_ARMV8_LITTLE_ENDIAN, "linux64", false},
		{ARCH_32BIT_POWERPC_BIG_ENDIAN, "linux32", false},
		{ARCH_64BIT_POWERPC_BIG_ENDIAN, "linux64", false},
		{ARCH_64BIT_POWERPC_LITTLE_ENDIAN, "linux64", false},
		{ARCH_64BIT_S390_BIG_ENDIAN, "linux64", false},
		{ARCH_32BIT_MIPS, "linux32", false},
		{ARCH_64BIT_MIPS, "linux64", false},
		{ARCH_32BIT_RISCV_LITTLE_ENDIAN, "linux32", false},
		{ARCH_64BIT_RISCV_LITTLE_ENDIAN, "linux64", false},
		{ARCH_64BIT_LOONGARCH, "linux64", false},
		{ARCH_UNKNOWN, "", true}, // Invalid architecture
		{999, "", true},          // Invalid architecture
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got, err := ArchitecturePersonality(tt.arch)
			if (err != nil) != tt.wantErr {
				t.Errorf("ArchitecturePersonality() error = %v, wantErr %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("ArchitecturePersonality() = %v, want %v", got, tt.want)
			}
		})
	}
}
