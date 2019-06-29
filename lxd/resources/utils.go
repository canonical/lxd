package resources

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func readUint(path string) (uint64, error) {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return 0, err
	}

	value, err := strconv.ParseUint(strings.TrimSpace(string(content)), 10, 64)
	if err != nil {
		return 0, err
	}

	return value, nil
}

func readInt(path string) (int64, error) {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return -1, err
	}

	value, err := strconv.ParseInt(strings.TrimSpace(string(content)), 10, 64)
	if err != nil {
		return -1, err
	}

	return value, nil
}

func sysfsExists(path string) bool {
	_, err := os.Lstat(path)
	if err == nil {
		return true
	}

	return false
}

func sysfsNumaNode(path string) (uint64, error) {
	// List all the directory entries
	entries, err := ioutil.ReadDir(path)
	if err != nil {
		return 0, err
	}

	// Iterate and look for NUMA
	for _, entry := range entries {
		entryName := entry.Name()

		if strings.HasPrefix(entryName, "node") && sysfsExists(filepath.Join(path, entryName, "numastat")) {
			node := strings.TrimPrefix(entryName, "node")

			nodeNumber, err := strconv.ParseUint(node, 10, 64)
			if err != nil {
				return 0, err
			}

			// Return the node we found
			return nodeNumber, nil
		}
	}

	// Didn't find a NUMA node for the device, assume single-node
	return 0, nil
}
