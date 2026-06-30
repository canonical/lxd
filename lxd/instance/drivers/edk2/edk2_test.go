package edk2

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/canonical/lxd/shared/osarch"
)

func TestGetenvEdk2Paths(t *testing.T) {
	tests := []struct {
		name        string
		envValue    string
		defaultPath string
		want        []string
	}{
		{
			name:        "empty environment falls back to the default path",
			envValue:    "",
			defaultPath: "/usr/share/OVMF",
			want:        []string{"/usr/share/OVMF"},
		},
		{
			name:        "environment paths are prefixed before the default path",
			envValue:    "/snap/lxd/current/share/qemu:/opt/fw",
			defaultPath: "/usr/share/OVMF",
			want:        []string{"/snap/lxd/current/share/qemu", "/opt/fw", "/usr/share/OVMF"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LXD_QEMU_FW_PATH", tc.envValue)

			got := GetenvEdk2Paths(tc.defaultPath)
			if !slices.Equal(got, tc.want) {
				t.Errorf("GetenvEdk2Paths(%q) = %v, want %v", tc.defaultPath, got, tc.want)
			}
		})
	}
}

func TestGetArchitectureFirmwareVarsCandidates(t *testing.T) {
	x86Candidates := GetArchitectureFirmwareVarsCandidates(osarch.ARCH_64BIT_INTEL_X86)

	// The returned list must not contain duplicates.
	seen := make(map[string]struct{}, len(x86Candidates))
	for _, name := range x86Candidates {
		_, duplicate := seen[name]
		if duplicate {
			t.Errorf("Duplicate vars candidate %q", name)
		}

		seen[name] = struct{}{}
	}

	// x86_64 must offer the current, transitional and legacy (deprecated) vars names
	// so that orphaned files from any previous naming convention can be cleaned up.
	for _, want := range []string{
		"OVMF_VARS_4M.fd",
		"OVMF_VARS_4M.ms.fd",
		"OVMF_VARS.4MB.fd",
		"OVMF_VARS.4MB.ms.fd",
		"OVMF_VARS.4MB.CSM.fd",
		"OVMF_VARS.2MB.CSM.fd",
		"OVMF_VARS.CSM.fd",
	} {
		if !slices.Contains(x86Candidates, want) {
			t.Errorf("x86_64 vars candidates missing %q", want)
		}
	}

	// The legacy CSM vars files were only ever created on x86_64 so they must not be
	// offered for cleanup on arm64.
	armCandidates := GetArchitectureFirmwareVarsCandidates(osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN)
	for _, legacy := range []string{
		"OVMF_VARS.4MB.CSM.fd",
		"OVMF_VARS.2MB.CSM.fd",
		"OVMF_VARS.CSM.fd",
	} {
		if slices.Contains(armCandidates, legacy) {
			t.Errorf("arm64 vars candidates should not include legacy x86_64 file %q", legacy)
		}
	}

	if !slices.Contains(armCandidates, "AAVMF_VARS.fd") {
		t.Error("arm64 vars candidates missing AAVMF_VARS.fd")
	}
}

// TestArchitectureInstallationsPreferenceOrder asserts that the firmware catalog lists the
// preferred (new) firmware names ahead of the legacy ones. This ordering determines which
// firmware a VM ends up using and, for existing VMs, the target name its NVRAM vars file is
// renamed to on start.
func TestArchitectureInstallationsPreferenceOrder(t *testing.T) {
	assertVarsBefore := func(t *testing.T, pairs []FirmwarePair, preferredVars string, legacyVars string) {
		t.Helper()

		preferredIdx := slices.IndexFunc(pairs, func(p FirmwarePair) bool { return p.Vars == preferredVars })
		legacyIdx := slices.IndexFunc(pairs, func(p FirmwarePair) bool { return p.Vars == legacyVars })

		if preferredIdx == -1 {
			t.Errorf("Preferred vars %q not found", preferredVars)
			return
		}

		if legacyIdx == -1 {
			t.Errorf("Legacy vars %q not found", legacyVars)
			return
		}

		if preferredIdx > legacyIdx {
			t.Errorf("Expected %q (index %d) to be preferred over %q (index %d)", preferredVars, preferredIdx, legacyVars, legacyIdx)
		}
	}

	// On x86_64 the _4M names must rank before the legacy 4MB names.
	x86Usage := architectureInstallations[osarch.ARCH_64BIT_INTEL_X86][0].Usage
	assertVarsBefore(t, x86Usage[GENERIC], "OVMF_VARS_4M.fd", "OVMF_VARS.4MB.fd")
	assertVarsBefore(t, x86Usage[SECUREBOOT], "OVMF_VARS_4M.ms.fd", "OVMF_VARS.4MB.ms.fd")

	// On arm64 the AAVMF firmware must be the preferred (first) pair for both usages.
	armUsage := architectureInstallations[osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN][0].Usage
	wantFirst := map[FirmwareUsage]FirmwarePair{
		GENERIC:    {Code: "AAVMF_CODE.fd", Vars: "AAVMF_VARS.fd"},
		SECUREBOOT: {Code: "AAVMF_CODE.ms.fd", Vars: "AAVMF_VARS.ms.fd"},
	}

	for usage, want := range wantFirst {
		got := armUsage[usage][0]
		if got != want {
			t.Errorf("arm64 usage %d: expected first pair %+v, got %+v", usage, want, got)
		}
	}
}

// TestGetArchitectureFirmwarePairsForUsage drives the selection logic against a controlled
// firmware catalog backed by a temporary directory. The package catalog resolves its search
// paths at init time, so it is swapped out here to make the test deterministic and independent
// of any firmware installed on the host.
func TestGetArchitectureFirmwarePairsForUsage(t *testing.T) {
	fwDir := t.TempDir()

	// emptyDir is searched first but contains no firmware, exercising path precedence.
	emptyDir := t.TempDir()

	const testArch = osarch.ARCH_64BIT_INTEL_X86

	original := architectureInstallations
	t.Cleanup(func() { architectureInstallations = original })
	architectureInstallations = map[int][]Installation{
		testArch: {{
			Paths: []string{emptyDir, fwDir},
			Usage: map[FirmwareUsage][]FirmwarePair{
				GENERIC: {
					{Code: "OVMF_CODE_4M.fd", Vars: "OVMF_VARS_4M.fd"},
					{Code: "OVMF_CODE.4MB.fd", Vars: "OVMF_VARS.4MB.fd"},
					// Only the code file is created below, so this pair must be skipped.
					{Code: "OVMF_CODE.2MB.fd", Vars: "OVMF_VARS.2MB.fd"},
				},
			},
		}},
	}

	for _, name := range []string{
		"OVMF_CODE_4M.fd", "OVMF_VARS_4M.fd",
		"OVMF_CODE.4MB.fd", "OVMF_VARS.4MB.fd",
		"OVMF_CODE.2MB.fd", // intentionally without its matching vars file
	} {
		err := os.WriteFile(filepath.Join(fwDir, name), []byte("firmware"), 0o600)
		if err != nil {
			t.Fatalf("Failed writing fake firmware %q: %v", name, err)
		}
	}

	got := GetArchitectureFirmwarePairsForUsage(testArch, GENERIC)

	// Only pairs whose code and vars both exist are returned, preferred name first, with full
	// paths resolved against the matching search directory.
	want := []FirmwarePair{
		{Code: filepath.Join(fwDir, "OVMF_CODE_4M.fd"), Vars: filepath.Join(fwDir, "OVMF_VARS_4M.fd")},
		{Code: filepath.Join(fwDir, "OVMF_CODE.4MB.fd"), Vars: filepath.Join(fwDir, "OVMF_VARS.4MB.fd")},
	}

	if !slices.Equal(got, want) {
		t.Errorf("GetArchitectureFirmwarePairsForUsage() = %+v, want %+v", got, want)
	}
}
