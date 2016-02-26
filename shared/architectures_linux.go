// +build linux

package shared

import (
	"syscall"
)

func ArchitectureGetLocal() (string, error) {
	uname := syscall.Utsname{}
	if err := syscall.Uname(&uname); err != nil {
		return ArchitectureDefault, err
	}

	architectureName := ""
	for _, c := range uname.Machine {
		if c == 0 {
			break
		}
		architectureName += string(byte(c))
	}

	return architectureName, nil
}
