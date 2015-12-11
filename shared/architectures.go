package shared

import (
	"fmt"
)

const (
	ARCH_UNKNOWN                     = 0
	ARCH_32BIT_INTEL_X86             = 1
	ARCH_64BIT_INTEL_X86             = 2
	ARCH_32BIT_ARMV7_LITTLE_ENDIAN   = 3
	ARCH_64BIT_ARMV8_LITTLE_ENDIAN   = 4
	ARCH_32BIT_POWERPC_BIG_ENDIAN    = 5
	ARCH_64BIT_POWERPC_BIG_ENDIAN    = 6
	ARCH_64BIT_POWERPC_LITTLE_ENDIAN = 7
	ARCH_64BIT_S390_BIG_ENDIAN       = 8
)

var architectureNames = map[int]string{
	ARCH_32BIT_INTEL_X86:             "i686",
	ARCH_64BIT_INTEL_X86:             "x86_64",
	ARCH_32BIT_ARMV7_LITTLE_ENDIAN:   "armv7l",
	ARCH_64BIT_ARMV8_LITTLE_ENDIAN:   "aarch64",
	ARCH_32BIT_POWERPC_BIG_ENDIAN:    "ppc",
	ARCH_64BIT_POWERPC_BIG_ENDIAN:    "ppc64",
	ARCH_64BIT_POWERPC_LITTLE_ENDIAN: "ppc64le",
	ARCH_64BIT_S390_BIG_ENDIAN:       "s390x",
}

var architecturePersonalities = map[int]string{
	ARCH_32BIT_INTEL_X86:             "linux32",
	ARCH_64BIT_INTEL_X86:             "linux64",
	ARCH_32BIT_ARMV7_LITTLE_ENDIAN:   "linux32",
	ARCH_64BIT_ARMV8_LITTLE_ENDIAN:   "linux64",
	ARCH_32BIT_POWERPC_BIG_ENDIAN:    "linux32",
	ARCH_64BIT_POWERPC_BIG_ENDIAN:    "linux64",
	ARCH_64BIT_POWERPC_LITTLE_ENDIAN: "linux64",
	ARCH_64BIT_S390_BIG_ENDIAN:       "linux64",
}

var architectureSupportedPersonalities = map[int][]int{
	ARCH_32BIT_INTEL_X86:             []int{},
	ARCH_64BIT_INTEL_X86:             []int{ARCH_32BIT_INTEL_X86},
	ARCH_32BIT_ARMV7_LITTLE_ENDIAN:   []int{},
	ARCH_64BIT_ARMV8_LITTLE_ENDIAN:   []int{ARCH_32BIT_ARMV7_LITTLE_ENDIAN},
	ARCH_32BIT_POWERPC_BIG_ENDIAN:    []int{},
	ARCH_64BIT_POWERPC_BIG_ENDIAN:    []int{ARCH_32BIT_POWERPC_BIG_ENDIAN},
	ARCH_64BIT_POWERPC_LITTLE_ENDIAN: []int{},
	ARCH_64BIT_S390_BIG_ENDIAN:       []int{},
}

func ArchitectureName(arch int) (string, error) {
	arch_name, exists := architectureNames[arch]
	if exists {
		return arch_name, nil
	}

	return "unknown", fmt.Errorf("Architecture isn't supported: %d", arch)
}

func ArchitectureId(arch string) (int, error) {
	for arch_id, arch_name := range architectureNames {
		if arch_name == arch {
			return arch_id, nil
		}
	}

	return 0, fmt.Errorf("Architecture isn't supported: %s", arch)
}

func ArchitecturePersonality(arch int) (string, error) {
	arch_personality, exists := architecturePersonalities[arch]
	if exists {
		return arch_personality, nil
	}

	return "", fmt.Errorf("Architecture isn't supported: %d", arch)
}

func ArchitecturePersonalities(arch int) ([]int, error) {
	personalities, exists := architectureSupportedPersonalities[arch]
	if exists {
		return personalities, nil
	}

	return []int{}, fmt.Errorf("Architecture isn't supported: %d", arch)
}
