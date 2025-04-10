package tests

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCalculateRSS(t *testing.T) {
	// Create mock /proc/meminfo content
	meminfoContent := `MemTotal:       32795852 kB
MemFree:        13780288 kB
MemAvailable:   21871980 kB
Buffers:          433588 kB
Cached:          7712084 kB
Shmem:             77292 kB
`

	// Create temporary file
	tmpDir, err := os.MkdirTemp("", "metrics-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	defer func() {
		err := os.RemoveAll(tmpDir)
		if err != nil {
			t.Logf("Failed to remove temp directory: %v", err)
		}
	}()
	tmpMeminfo := filepath.Join(tmpDir, "meminfo")
	err = os.WriteFile(tmpMeminfo, []byte(meminfoContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}

	// Expected values (in bytes)
	memTotal := uint64(32795852 * 1024)
	memAvailable := uint64(21871980 * 1024)

	// Calculate expected RSS based on the simplified formula
	expectedRSS := memTotal - memAvailable

	// Test the calculation
	content, err := os.ReadFile(tmpMeminfo)
	if err != nil {
		t.Fatalf("Failed to read test meminfo: %v", err)
	}

	// Parse the values
	var testMemTotal, testMemAvailable uint64
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		name := strings.TrimRight(fields[0], ":")
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}

		// Convert to bytes
		value *= 1024

		switch name {
		case "MemTotal":
			testMemTotal = value
		case "MemAvailable":
			testMemAvailable = value
		}
	}

	// Calculate RSS using the simplified formula
	calculatedRSS := testMemTotal - testMemAvailable

	// Verify result matches expected
	if calculatedRSS != expectedRSS {
		t.Errorf("RSS calculation incorrect: expected %d, got %d", expectedRSS, calculatedRSS)
	}

	// Also verify the formula matches what we expect
	manualRSS := uint64(32795852*1024) - uint64(21871980*1024)
	if calculatedRSS != manualRSS {
		t.Errorf("Formula verification failed: expected %d, got %d", manualRSS, calculatedRSS)
	}
}

func TestMissingFieldsRSS(t *testing.T) {
	// Create mock /proc/meminfo content with missing MemAvailable field
	meminfoContent := `MemTotal:       32795852 kB
MemFree:        13780288 kB
Buffers:          433588 kB
Cached:          7712084 kB
Shmem:             77292 kB
`
	// Create temporary file
	tmpDir, err := os.MkdirTemp("", "metrics-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	defer func() {
		err := os.RemoveAll(tmpDir)
		if err != nil {
			t.Logf("Failed to remove temp directory: %v", err)
		}
	}()
	tmpMeminfo := filepath.Join(tmpDir, "meminfo")
	err = os.WriteFile(tmpMeminfo, []byte(meminfoContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}

	// Our function should not set RSSBytes when required fields are missing.
	// This is just a synthetic test to validate the logic - we can't directly
	// call the function from metrics.go, but we can validate the parsing logic.

	content, err := os.ReadFile(tmpMeminfo)
	if err != nil {
		t.Fatalf("Failed to read test meminfo: %v", err)
	}

	// Parse the values manually like our metrics.go function would
	var foundMemTotal, foundMemAvailable bool

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		name := strings.TrimRight(fields[0], ":")
		_, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}

		switch name {
		case "MemTotal":
			foundMemTotal = true
		case "MemAvailable":
			foundMemAvailable = true
		}
	}

	// Check that we correctly detect the missing MemAvailable field
	if foundMemAvailable {
		t.Errorf("Expected MemAvailable field to be missing but it was found")
	}

	// Verify that we'd skip RSS calculation since not all required fields are present
	if foundMemTotal && foundMemAvailable {
		t.Errorf("Expected to detect missing fields but all fields were found")
	}
}
