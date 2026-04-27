package edk2

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/osarch"
)

// FirmwarePair represents a combination of firmware code (Code) and storage (Vars).
type FirmwarePair struct {
	Code string
	Vars string
}

// Installation represents a set of available firmware at a given location on the system.
type Installation struct {
	Paths []string
	Usage map[FirmwareUsage][]FirmwarePair
}

// FirmwareUsage represents the situation in which a given firmware file will be used.
type FirmwareUsage int

const (
	// GENERIC is a generic EDK2 firmware.
	GENERIC FirmwareUsage = iota

	// SECUREBOOT is a UEFI Secure Boot enabled firmware.
	SECUREBOOT

	// CSM is a SeaBIOS firmware used for booting legacy BIOS-only operating systems.
	CSM
)

// legacyFirmwareVarsCandidates lists firmware vars file names that were previously used but have since been
// deprecated. They are still returned by GetAchitectureFirmwareVarsCandidates to allow cleanup of any
// orphaned files left over on existing VM instances.
// These are OVMF EDK2-based CSM firmware vars files, which were replaced by SeaBIOS for BIOS mode.
var legacyFirmwareVarsCandidates = []string{
	"OVMF_VARS.4MB.CSM.fd",
	"OVMF_VARS.2MB.CSM.fd",
	"OVMF_VARS.CSM.fd",
}

var architectureInstallations = map[int][]Installation{
	osarch.ARCH_64BIT_INTEL_X86: {{
		Paths: GetenvEdk2Paths("/usr/share/OVMF"),
		Usage: map[FirmwareUsage][]FirmwarePair{
			GENERIC: {
				{Code: "OVMF_CODE_4M.fd", Vars: "OVMF_VARS_4M.fd"},
				{Code: "OVMF_CODE.4MB.fd", Vars: "OVMF_VARS.4MB.fd"},
				{Code: "OVMF_CODE.4m.fd", Vars: "OVMF_VARS.4m.fd"},
				{Code: "OVMF_CODE.2MB.fd", Vars: "OVMF_VARS.2MB.fd"},
				{Code: "OVMF_CODE.fd", Vars: "OVMF_VARS.fd"},
				{Code: "OVMF_CODE.fd", Vars: "qemu.nvram"},
			},
			SECUREBOOT: {
				{Code: "OVMF_CODE_4M.ms.fd", Vars: "OVMF_VARS_4M.ms.fd"},
				{Code: "OVMF_CODE.4MB.fd", Vars: "OVMF_VARS.4MB.ms.fd"},
				{Code: "OVMF_CODE_4M.secboot.fd", Vars: "OVMF_VARS_4M.secboot.fd"},
				{Code: "OVMF_CODE.secboot.4m.fd", Vars: "OVMF_VARS.4m.fd"},
				{Code: "OVMF_CODE.secboot.fd", Vars: "OVMF_VARS.secboot.fd"},
				{Code: "OVMF_CODE.secboot.fd", Vars: "OVMF_VARS.fd"},
				{Code: "OVMF_CODE.2MB.fd", Vars: "OVMF_VARS.2MB.ms.fd"},
				{Code: "OVMF_CODE.fd", Vars: "OVMF_VARS.ms.fd"},
				{Code: "OVMF_CODE.fd", Vars: "qemu.nvram"},
			},
			CSM: {
				{Code: "bios-256k.bin", Vars: "bios-256k.bin"},
				{Code: "seabios.bin", Vars: "seabios.bin"},
			},
		},
	}, {
		Paths: GetenvEdk2Paths("/usr/share/qemu"),
		Usage: map[FirmwareUsage][]FirmwarePair{
			GENERIC: {
				{Code: "ovmf-x86_64-4m-code.bin", Vars: "ovmf-x86_64-4m-vars.bin"},
				{Code: "ovmf-x86_64.bin", Vars: "ovmf-x86_64-code.bin"},
			},
			SECUREBOOT: {
				{Code: "ovmf-x86_64-ms-4m-vars.bin", Vars: "ovmf-x86_64-ms-4m-code.bin"},
				{Code: "ovmf-x86_64-ms-code.bin", Vars: "ovmf-x86_64-ms-vars.bin"},
			},
			CSM: {
				{Code: "bios-256k.bin", Vars: "bios-256k.bin"},
				{Code: "seabios.bin", Vars: "seabios.bin"},
			},
		},
	}, {
		Paths: GetenvEdk2Paths("/usr/share/edk2/x64"),
		Usage: map[FirmwareUsage][]FirmwarePair{
			GENERIC: {
				{Code: "OVMF_CODE.4m.fd", Vars: "OVMF_VARS.4m.fd"},
				{Code: "OVMF_CODE.fd", Vars: "OVMF_VARS.fd"},
			},
			SECUREBOOT: {
				{Code: "OVMF_CODE.secure.4m.fd", Vars: "OVMF_VARS.4m.fd"},
				{Code: "OVMF_CODE.secure.fd", Vars: "OVMF_VARS.fd"},
			},
		},
	}, {
		Paths: GetenvEdk2Paths("/usr/share/OVMF/x64"),
		Usage: map[FirmwareUsage][]FirmwarePair{
			GENERIC: {
				{Code: "OVMF_CODE.4m.fd", Vars: "OVMF_VARS.4m.fd"},
				{Code: "OVMF_CODE.fd", Vars: "OVMF_VARS.fd"},
			},
			SECUREBOOT: {
				{Code: "OVMF_CODE.secboot.4m.fd", Vars: "OVMF_VARS.4m.fd"},
				{Code: "OVMF_CODE.secboot.fd", Vars: "OVMF_VARS.fd"},
			},
		},
	}, {
		Paths: GetenvEdk2Paths("/usr/share/seabios"),
		Usage: map[FirmwareUsage][]FirmwarePair{
			CSM: {
				{Code: "bios-256k.bin", Vars: "bios-256k.bin"},
				{Code: "seabios.bin", Vars: "seabios.bin"},
			},
		},
	}},
	osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN: {{
		Paths: GetenvEdk2Paths("/usr/share/AAVMF"),
		Usage: map[FirmwareUsage][]FirmwarePair{
			GENERIC: {
				{Code: "AAVMF_CODE.fd", Vars: "AAVMF_VARS.fd"},
				{Code: "OVMF_CODE_4M.fd", Vars: "OVMF_VARS_4M.fd"},
				{Code: "OVMF_CODE.4MB.fd", Vars: "OVMF_VARS.4MB.fd"},
				{Code: "OVMF_CODE.4m.fd", Vars: "OVMF_VARS.4m.fd"},
				{Code: "OVMF_CODE.2MB.fd", Vars: "OVMF_VARS.2MB.fd"},
				{Code: "OVMF_CODE.fd", Vars: "OVMF_VARS.fd"},
				{Code: "OVMF_CODE.fd", Vars: "qemu.nvram"},
			},
			SECUREBOOT: {
				{Code: "AAVMF_CODE.ms.fd", Vars: "AAVMF_VARS.ms.fd"},
				{Code: "OVMF_CODE_4M.ms.fd", Vars: "OVMF_VARS_4M.ms.fd"},
				{Code: "OVMF_CODE.4MB.fd", Vars: "OVMF_VARS.4MB.ms.fd"},
				{Code: "OVMF_CODE_4M.secboot.fd", Vars: "OVMF_VARS_4M.secboot.fd"},
				{Code: "OVMF_CODE.secboot.4m.fd", Vars: "OVMF_VARS.4m.fd"},
				{Code: "OVMF_CODE.secboot.fd", Vars: "OVMF_VARS.secboot.fd"},
				{Code: "OVMF_CODE.secboot.fd", Vars: "OVMF_VARS.fd"},
				{Code: "OVMF_CODE.2MB.fd", Vars: "OVMF_VARS.2MB.ms.fd"},
				{Code: "OVMF_CODE.fd", Vars: "OVMF_VARS.ms.fd"},
				{Code: "OVMF_CODE.fd", Vars: "qemu.nvram"},
			},
		},
	}},
}

// GetAchitectureFirmwareVarsCandidates returns a unique list of candidate vars names for hostArch for all usages.
// It does not check whether the associated firmware files are present on the host now.
// This can be used to check for the existence of previously used firmware vars files in an existing VM instance.
// Legacy (deprecated) vars file names are also included to allow cleanup of orphaned files.
func GetAchitectureFirmwareVarsCandidates(hostArch int) (varsNames []string) {
	for _, installation := range architectureInstallations[hostArch] {
		for _, usage := range installation.Usage {
			for _, fwPair := range usage {
				if !slices.Contains(varsNames, fwPair.Vars) {
					varsNames = append(varsNames, fwPair.Vars)
				}
			}
		}
	}

	// The legacy OVMF and CSM vars files were only ever created on x86-64.
	if hostArch == osarch.ARCH_64BIT_INTEL_X86 {
		for _, legacyVars := range legacyFirmwareVarsCandidates {
			if !slices.Contains(varsNames, legacyVars) {
				varsNames = append(varsNames, legacyVars)
			}
		}
	}

	return varsNames
}

// GetArchitectureFirmwarePairsForUsage returns FirmwarePair slice for a host architecture and usage combination.
// It only includes FirmwarePairs where both the firmware and its vars file are found on the host.
func GetArchitectureFirmwarePairsForUsage(hostArch int, usage FirmwareUsage) []FirmwarePair {
	firmwares := make([]FirmwarePair, 0)

	for _, installation := range architectureInstallations[hostArch] {
		usage, found := installation.Usage[usage]
		if found {
			for _, firmwarePair := range usage {
				for _, searchPath := range installation.Paths {
					codePath := filepath.Join(searchPath, firmwarePair.Code)
					varsPath := filepath.Join(searchPath, firmwarePair.Vars)

					// Check both firmware code and vars paths exist - otherwise skip pair.
					if !shared.PathExists(codePath) || !shared.PathExists(varsPath) {
						continue
					}

					firmwares = append(firmwares, FirmwarePair{
						Code: codePath,
						Vars: varsPath,
					})
				}
			}
		}
	}

	return firmwares
}

// GetenvEdk2Paths returns a list of paths to search for VM firmwares.
// If LXD_QEMU_FW_PATH env variable is set then its value is split on ":" and
// prefixed to the returned slice of paths.
// The defaultPath argument is returned as the last element in the slice.
func GetenvEdk2Paths(defaultPath string) []string {
	var qemuFwPaths []string

	searchPaths := os.Getenv("LXD_QEMU_FW_PATH")
	if searchPaths != "" {
		qemuFwPaths = append(qemuFwPaths, strings.Split(searchPaths, ":")...)
	}

	return append(qemuFwPaths, defaultPath)
}
