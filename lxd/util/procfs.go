//go:build linux

package util

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

// CurrentBootID returns the system's boot_id UUID. Uses a wrapper instead of exporting
// bootID as BootID to prevent reassignment.
func CurrentBootID() (string, error) {
	return bootID()
}

// ProcStatFields takes in a path to a /proc/.*/stat file, such as /proc/<pid>/stat, and returns
// a slice of strings that correspond to the fields of the statfile, appropriately handling whitespace
// and/or parentheses in the (comm) field.
func ProcStatFields(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	s := string(bytes.TrimSpace(data))

	// (comm), the second field of proc/<pid>/stat and /proc/<pid>/task/<tid>/stat, is the only field
	// with a string format specifier (%s) and is surrounded by parentheses. All other fields are numbers
	// or characters from a subset that does not include [() ]. We intentionally return comm less the
	// surrounding parentheses.
	commStart := strings.IndexByte(s, '(')
	commEnd := strings.LastIndexByte(s, ')')

	if commStart < 1 || commEnd < commStart || commEnd+2 > len(s) {
		return nil, fmt.Errorf("Malformed stat file: %s", path)
	}

	fields := make([]string, 0, 52)

	fields = append(fields, s[:commStart-1])
	fields = append(fields, s[commStart+1:commEnd])

	s = s[commEnd+2:]

	for s != "" {
		var field string
		field, s, _ = strings.Cut(s, " ")
		fields = append(fields, field)
	}

	return fields, nil
}

// ProcessStartTime is a helper function that does range-checking and integer conversion for the common
// case of reading /proc/<pid>/stat to obtain a process/thread's starttime.
func ProcessStartTime(pid int) (int64, error) {
	statPath := "/proc/" + strconv.Itoa(pid) + "/stat"
	fields, err := ProcStatFields(statPath)
	if err != nil {
		return 0, err
	}

	if len(fields) < 22 {
		return 0, fmt.Errorf("Malformed stat file: %s", statPath)
	}

	return strconv.ParseInt(fields[21], 10, 64)
}
