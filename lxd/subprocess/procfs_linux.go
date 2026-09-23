//go:build linux

package subprocess

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

var bootID = sync.OnceValues(func() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(data)), nil
})

func currentBootID() (string, error) {
	return bootID()
}

func processStartTime(pid int) (int64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}

	_, after, ok := bytes.CutLast(data, []byte{')'})
	if !ok {
		return 0, fmt.Errorf("Malformed /proc/%d/stat", pid)
	}

	fields := strings.Fields(string(after))
	if len(fields) < 20 {
		return 0, fmt.Errorf("Malformed /proc/%d/stat", pid)
	}

	return strconv.ParseInt(fields[19], 10, 64)
}
