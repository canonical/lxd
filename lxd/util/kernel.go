package util

import (
	"bufio"
	"context"
	"errors"
	"os"
	"slices"
	"strings"

	"github.com/canonical/lxd/shared"
)

// LoadModule loads the kernel module with the given name, by invoking
// modprobe. This respects any modprobe configuration on the system.
func LoadModule(module string) error {
	if shared.PathExists("/sys/module/" + module) {
		return nil
	}

	_, err := shared.RunCommand(context.TODO(), "modprobe", "-b", module)
	return err
}

// HugepagesPath attempts to locate the mount point of the hugepages filesystem.
func HugepagesPath() (string, error) {
	// Find the source mount of the path
	file, err := os.Open("/proc/mounts")
	if err != nil {
		return "", err
	}

	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	matches := []string{}
	for scanner.Scan() {
		line := scanner.Text()
		cols := strings.Fields(line)
		if len(cols) < 3 {
			continue
		}

		if cols[2] == "hugetlbfs" {
			matches = append(matches, cols[1])
		}
	}

	if len(matches) == 0 {
		return "", errors.New("No hugetlbfs mount found, can't use hugepages")
	}

	if len(matches) > 1 {
		if slices.Contains(matches, "/dev/hugepages") {
			return "/dev/hugepages", nil
		}

		return "", errors.New("More than one hugetlbfs instance found and none at standard /dev/hugepages")
	}

	return matches[0], nil
}
