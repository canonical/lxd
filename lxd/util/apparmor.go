package util

import (
	"io/ioutil"
	"strings"
)

// AppArmorProfile returns the current apparmor profile.
func AppArmorProfile() string {
	contents, err := ioutil.ReadFile("/proc/self/attr/current")
	if err == nil {
		return strings.TrimSpace(string(contents))
	}

	return ""
}
