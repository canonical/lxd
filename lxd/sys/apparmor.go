//go:build linux && cgo && !agent

package sys

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/moby/sys/capability"

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/warningtype"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

// Initialize AppArmor-specific attributes.
func (s *OS) initAppArmor() []cluster.Warning {
	var dbWarnings []cluster.Warning

	/* Detect AppArmor availability */
	_, err := exec.LookPath("apparmor_parser")
	if shared.IsFalse(os.Getenv("LXD_SECURITY_APPARMOR")) {
		logger.Warn("AppArmor support has been manually disabled")
		dbWarnings = append(dbWarnings, cluster.Warning{
			TypeCode:    warningtype.AppArmorNotAvailable,
			LastMessage: "Manually disabled",
		})
	} else if !shared.IsDir("/sys/kernel/security/apparmor") {
		logger.Warn("AppArmor support has been disabled because of lack of kernel support")
		dbWarnings = append(dbWarnings, cluster.Warning{
			TypeCode:    warningtype.AppArmorNotAvailable,
			LastMessage: "Disabled because of lack of kernel support",
		})
	} else if err != nil {
		logger.Warn("AppArmor support has been disabled because 'apparmor_parser' couldn't be found")
		dbWarnings = append(dbWarnings, cluster.Warning{
			TypeCode:    warningtype.AppArmorNotAvailable,
			LastMessage: "Disabled because 'apparmor_parser' couldn't be found",
		})
	} else {
		/* Detect AppArmor version */
		s.AppArmorVersion, err = appArmorGetVersion()
		if err != nil {
			logger.Warn("AppArmor support has been disabled because the version couldn't be determined", logger.Ctx{"err": err})
			dbWarnings = append(dbWarnings, cluster.Warning{
				TypeCode:    warningtype.AppArmorNotAvailable,
				LastMessage: "Disabled because the version couldn't be determined",
			})
		} else {
			logger.Info("AppArmor support is enabled", logger.Ctx{"version": s.AppArmorVersion})
			s.AppArmorAvailable = true
		}
	}

	/* Detect AppArmor stacking support */
	s.AppArmorStacking = appArmorCanStack()

	// Detect AppArmor cache directories.
	s.AppArmorCacheLoc = shared.VarPath("security", "apparmor", "cache")
	if s.AppArmorAvailable {
		s.AppArmorCacheDir, err = appArmorGetCacheDir(s.AppArmorVersion, s.AppArmorCacheLoc)
		if err != nil {
			logger.Warn("AppArmor feature cache directory detection failed", logger.Ctx{"err": err})
		} else {
			logger.Debug("AppArmor feature cache directory detected", logger.Ctx{"cache_dir": s.AppArmorCacheDir})
		}
	} else {
		// If AppArmor is not available, use the base cache location that doesn't need apparmor_parser to determine.
		s.AppArmorCacheDir = s.AppArmorCacheLoc
	}

	/* Detect existing AppArmor stack */
	if shared.PathExists("/sys/kernel/security/apparmor/.ns_stacked") {
		contentBytes, err := os.ReadFile("/sys/kernel/security/apparmor/.ns_stacked")
		if err == nil && string(contentBytes) == "yes\n" {
			s.AppArmorStacked = true
		}
	}

	/* Detect AppArmor admin support */
	if !haveMacAdmin() {
		if s.AppArmorAvailable {
			logger.Warn("Per-container AppArmor profiles are disabled because the mac_admin capability is missing")
		}
	} else if s.RunningInUserNS && !s.AppArmorStacked {
		if s.AppArmorAvailable {
			logger.Warn("Per-container AppArmor profiles are disabled because LXD is running in an unprivileged container without stacking")
		}
	} else {
		s.AppArmorAdmin = true
	}

	/* Detect AppArmor confinment */
	profile := util.AppArmorProfile()
	/* if AppArmor is enabled on the system but there is no profile then
	   the application has the profile name "unconfined", or if AppArmor
	   is not supported then the label will be empty and finally newer
	   AppArmor releases support a profile mode "unconfined" where an
	   application can have a profile defined for it which effectively
	   is equivalent to the traditional "unconfined" label - in this case
	   the label will have the suffix " (unconfined)" */
	if profile != "unconfined" && profile != "" && !strings.HasSuffix(profile, "(unconfined)") {
		if s.AppArmorAvailable {
			logger.Warnf("Per-container AppArmor profiles are disabled because LXD is already protected by AppArmor via profile %q", profile)
		}

		s.AppArmorConfined = true
	}

	s.AppArmorFeatures.Map = map[string]bool{}

	return dbWarnings
}

func haveMacAdmin() bool {
	c, err := capability.NewPid2(0)
	if err != nil {
		return false
	}

	err = c.Load()
	if err != nil {
		return false
	}

	return c.Get(capability.EFFECTIVE, capability.CAP_MAC_ADMIN)
}

// getVersion reads and parses the AppArmor version.
func appArmorGetVersion() (*version.DottedVersion, error) {
	out, err := shared.RunCommandContext(context.TODO(), "apparmor_parser", "--version")
	if err != nil {
		return nil, err
	}

	fields := strings.Fields(strings.SplitN(out, "\n", 2)[0])
	return version.Parse(fields[len(fields)-1])
}

// appArmorGetCacheDir returns the AppArmor cache directory based on the cache location.
// If appArmor version is less than 2.13, it returns the base cache location directory.
func appArmorGetCacheDir(ver *version.DottedVersion, cacheLoc string) (string, error) {
	// Multiple policy cache directories were only added in v2.13.
	minVer, err := version.NewDottedVersion("2.13")
	if err != nil {
		return "", err
	}

	if ver.Compare(minVer) < 0 {
		return cacheLoc, nil
	}

	// `--print-cache-dir` returns a subdirectory under `--cache-loc`.
	// The subdirectory used will be influenced by the features available and enabled.
	out, err := shared.RunCommandContext(context.TODO(), "apparmor_parser", "--cache-loc", cacheLoc, "--print-cache-dir")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

// Returns true if AppArmor stacking support is available.
func appArmorCanStack() bool {
	contentBytes, err := os.ReadFile("/sys/kernel/security/apparmor/features/domain/stack")
	if err != nil {
		return false
	}

	if string(contentBytes) != "yes\n" {
		return false
	}

	contentBytes, err = os.ReadFile("/sys/kernel/security/apparmor/features/domain/version")
	if err != nil {
		return false
	}

	content := string(contentBytes)

	parts := strings.SplitN(strings.TrimSpace(content), ".", 3)
	if len(parts) < 2 {
		logger.Warn("Unknown apparmor domain version", logger.Ctx{"version": content})
		return false
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		logger.Warn("Unknown apparmor domain version", logger.Ctx{"version": content})
		return false
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		logger.Warn("Unknown apparmor domain version", logger.Ctx{"version": content})
		return false
	}

	return major >= 1 && minor >= 2
}
