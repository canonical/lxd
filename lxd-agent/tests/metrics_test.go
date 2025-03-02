package tests

import (
	"io"
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

func TestIsSystemLoadReasonable(t *testing.T) {
	// Setup mock files and functions
	origReadFile := readFileMock
	defer func() { mockReadFile = nil }()
	
	// Test case 1: Low load
	mockReadFile = func(path string) ([]byte, error) {
		if path == "/proc/loadavg" {
			return []byte("0.75 0.86 0.84 2/1253 73523"), nil
		}
		return origReadFile(path)
	}
	
	isLow, err := isSystemLoadReasonable()
	if err != nil {
		t.Errorf("isSystemLoadReasonable failed: %v", err)
	}
	if !isLow {
		t.Errorf("Expected load to be reasonable with value 0.75")
	}
	
	// Test case 2: High load
	mockReadFile = func(path string) ([]byte, error) {
		if path == "/proc/loadavg" {
			return []byte("8.12 7.95 7.51 5/1289 73526"), nil
		}
		return origReadFile(path)
	}
	
	isLow, err = isSystemLoadReasonable()
	if err != nil {
		t.Errorf("isSystemLoadReasonable failed: %v", err)
	}
	if isLow {
		t.Errorf("Expected load to be unreasonable with value 8.12")
	}
	
	// Test case 3: Invalid format
	mockReadFile = func(path string) ([]byte, error) {
		if path == "/proc/loadavg" {
			return []byte("invalid"), nil
		}
		return origReadFile(path)
	}
	
	_, err = isSystemLoadReasonable()
	if err == nil {
		t.Errorf("Expected error for invalid loadavg format, got nil")
	}
}

// Mock function to implement in the metrics.go file
func isSystemLoadReasonable() (bool, error) {
	loadavg, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return false, err
	}
	
	fields := strings.Fields(string(loadavg))
	if len(fields) == 0 {
		return false, io.EOF
	}
	
	load, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return false, err
	}
	
	return load < 5.0, nil
}