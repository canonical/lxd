package util

import (
	"io/ioutil"
	"strconv"
	"strings"

	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/shared/logger"
)

// AppArmorCanStack returns true if AppArmor stacking support is available.
func AppArmorCanStack() bool {
	contentBytes, err := ioutil.ReadFile("/sys/kernel/security/apparmor/features/domain/stack")
	if err != nil {
		return false
	}

	if string(contentBytes) != "yes\n" {
		return false
	}

	contentBytes, err = ioutil.ReadFile("/sys/kernel/security/apparmor/features/domain/version")
	if err != nil {
		return false
	}

	content := string(contentBytes)

	parts := strings.Split(strings.TrimSpace(content), ".")

	if len(parts) == 0 {
		logger.Warn("unknown apparmor domain version", log.Ctx{"version": content})
		return false
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		logger.Warn("unknown apparmor domain version", log.Ctx{"version": content})
		return false
	}

	minor := 0
	if len(parts) == 2 {
		minor, err = strconv.Atoi(parts[1])
		if err != nil {
			logger.Warn("unknown apparmor domain version", log.Ctx{"version": content})
			return false
		}
	}

	return major >= 1 && minor >= 2
}

// AppArmorProfile returns the current apparmor profile.
func AppArmorProfile() string {
	contents, err := ioutil.ReadFile("/proc/self/attr/current")
	if err == nil {
		return strings.TrimSpace(string(contents))
	}

	return ""
}
