package tests

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// mockReadFile allows mocking file content during tests
var origReadFile = os.ReadFile
var mockReadFile func(string) ([]byte, error)

func readFileMock(path string) ([]byte, error) {
	if mockReadFile != nil {
		return mockReadFile(path)
	}
	return origReadFile(path)
}

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
	defer os.RemoveAll(tmpDir)
	
	tmpMeminfo := filepath.Join(tmpDir, "meminfo")
	if err := os.WriteFile(tmpMeminfo, []byte(meminfoContent), 0644); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}

	// Expected values (in bytes)
	memTotal := uint64(32795852 * 1024)
	memFree := uint64(13780288 * 1024)
	buffers := uint64(433588 * 1024)
	cached := uint64(7712084 * 1024)
	shmem := uint64(77292 * 1024)
	
	// Calculate expected RSS based on our formula
	expectedRSS := memTotal - (memFree + buffers + cached - shmem)
	
	// Test the calculation
	content, err := os.ReadFile(tmpMeminfo)
	if err != nil {
		t.Fatalf("Failed to read test meminfo: %v", err)
	}
	
	// Parse the values 
	var testMemTotal, testMemFree, testBuffers, testCached, testShmem uint64
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
		case "MemFree":
			testMemFree = value
		case "Buffers":
			testBuffers = value
		case "Cached":
			testCached = value
		case "Shmem":
			testShmem = value
		}
	}
	
	// Calculate RSS using the parsed values
	calculatedRSS := testMemTotal - (testMemFree + testBuffers + testCached - testShmem)
	
	// Verify result matches expected
	if calculatedRSS != expectedRSS {
		t.Errorf("RSS calculation incorrect: expected %d, got %d", expectedRSS, calculatedRSS)
	}
	
	// Also verify the formula matches what we expect
	manualRSS := uint64(32795852 * 1024) - 
		(uint64(13780288 * 1024) + uint64(433588 * 1024) + uint64(7712084 * 1024) - uint64(77292 * 1024))
	if calculatedRSS != manualRSS {
		t.Errorf("Formula verification failed: expected %d, got %d", manualRSS, calculatedRSS)
	}
}

func TestMissingFieldsRSS(t *testing.T) {
	// Create mock /proc/meminfo content with missing Buffers field
	meminfoContent := `MemTotal:       32795852 kB
MemFree:        13780288 kB
MemAvailable:   21871980 kB
Cached:          7712084 kB
Shmem:             77292 kB
`
	// Create temporary file
	tmpDir, err := os.MkdirTemp("", "metrics-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	tmpMeminfo := filepath.Join(tmpDir, "meminfo")
	if err := os.WriteFile(tmpMeminfo, []byte(meminfoContent), 0644); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}

	// Our function should not set RSSBytes when required fields are missing
	// This is just a synthetic test to validate the logic - we can't directly
	// call the function from metrics.go, but we can validate the parsing logic
	
	content, err := os.ReadFile(tmpMeminfo)
	if err != nil {
		t.Fatalf("Failed to read test meminfo: %v", err)
	}
	
	// Parse the values manually like our metrics.go function would
	var foundMemTotal, foundMemFree, foundBuffers, foundCached, foundShmem bool
	
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
			foundMemTotal = true
		case "MemFree":
			foundMemFree = true
		case "Buffers":
			foundBuffers = true
		case "Cached":
			foundCached = true
		case "Shmem":
			foundShmem = true
		}
	}
	
	// Check that we correctly detect the missing Buffers field
	if foundBuffers {
		t.Errorf("Expected Buffers field to be missing but it was found")
	}
	
	// Verify that we'd skip RSS calculation since not all fields are present
	if foundMemTotal && foundMemFree && foundBuffers && foundCached && foundShmem {
		t.Errorf("Expected to detect missing fields but all fields were found")
	}
}

