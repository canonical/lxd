package resources

import (
	"encoding/hex"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var sysBusPci = "/sys/bus/pci/devices"

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

func stringInSlice(key string, list []string) bool {
	for _, entry := range list {
		if entry == key {
			return true
		}
	}
	return false
}

func int64InSlice(key int64, list []int64) bool {
	for _, entry := range list {
		if entry == key {
			return true
		}
	}
	return false
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

func hasBit(n uint32, pos uint) bool {
	val := n & (1 << pos)
	return (val > 0)
}

func hasBitField(n []uint32, bit uint) bool {
	return (n[bit/32] & (1 << (bit % 32))) != 0
}

func udevDecode(s string) (string, error) {
	// Inverse of https://github.com/systemd/systemd/blob/master/src/basic/device-nodes.c#L22
	ret := ""
	for i := 0; i < len(s); i++ {
		// udev converts non-devnode supported chars to four byte encode hex strings.
		if s[i] == '\\' && i+4 <= len(s) && s[i+1] == 'x' {
			hexValue := s[i+2 : i+4]
			strValue, err := hex.DecodeString(hexValue)
			if err == nil {
				ret += string(strValue)
				i += 3
			} else {
				return ret, err
			}
		} else {
			ret += s[i : i+1]
		}
	}

	return ret, nil
}
