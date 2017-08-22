package sys

import (
	"os"
	"os/exec"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

// Initialize AppArmor-specific attributes.
func (s *OS) initAppArmor() {
	/* Detect AppArmor availability */
	_, err := exec.LookPath("apparmor_parser")
	if os.Getenv("LXD_SECURITY_APPARMOR") == "false" {
		logger.Warnf("AppArmor support has been manually disabled")
	} else if !shared.IsDir("/sys/kernel/security/apparmor") {
		logger.Warnf("AppArmor support has been disabled because of lack of kernel support")
	} else if err != nil {
		logger.Warnf("AppArmor support has been disabled because 'apparmor_parser' couldn't be found")
	} else {
		s.AppArmorAvailable = true
	}
}
