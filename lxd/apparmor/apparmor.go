package apparmor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/version"
)

const (
	cmdLoad   = "--replace"
	cmdUnload = "--remove"
	cmdParse  = "--skip-kernel-load"
)

var aaPath = shared.VarPath("security", "apparmor")

// runApparmor runs the relevant AppArmor command.
func runApparmor(sysOS *sys.OS, command string, name string) error {
	if !sysOS.AppArmorAvailable {
		return nil
	}

	_, err := shared.RunCommandContext(context.TODO(), "apparmor_parser", []string{
		command,
		"--write-cache", "--cache-loc", sysOS.AppArmorCacheLoc,
		filepath.Join(aaPath, "profiles", name),
	}...)

	if err != nil {
		return err
	}

	return nil
}

// createNamespace creates a new AppArmor namespace.
func createNamespace(sysOS *sys.OS, name string) error {
	if !sysOS.AppArmorAvailable {
		return nil
	}

	if !sysOS.AppArmorStacking || sysOS.AppArmorStacked {
		return nil
	}

	p := filepath.Join("/sys/kernel/security/apparmor/policy/namespaces", name)
	err := os.Mkdir(p, 0755)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}

	return nil
}

// deleteNamespace destroys an AppArmor namespace.
func deleteNamespace(sysOS *sys.OS, name string) error {
	if !sysOS.AppArmorAvailable {
		return nil
	}

	if !sysOS.AppArmorStacking || sysOS.AppArmorStacked {
		return nil
	}

	p := filepath.Join("/sys/kernel/security/apparmor/policy/namespaces", name)
	err := os.Remove(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return nil
}

// hasProfile checks if the profile is already loaded.
func hasProfile(name string) (bool, error) {
	profilesPath := "/sys/kernel/security/apparmor/policy/profiles"
	if !shared.PathExists(profilesPath) {
		return false, nil
	}

	entries, err := os.ReadDir(profilesPath)
	if err != nil {
		return false, err
	}

	mangled := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(name, "/", "."), "<", ""), ">", "")
	for _, entry := range entries {
		fields := strings.Split(entry.Name(), ".")
		if mangled == strings.Join(fields[0:len(fields)-1], ".") {
			return true, nil
		}
	}

	return false, nil
}

// parseProfile parses the profile without loading it into the kernel.
func parseProfile(sysOS *sys.OS, name string) error {
	return runApparmor(sysOS, cmdParse, name)
}

// loadProfile loads the AppArmor profile into the kernel.
func loadProfile(sysOS *sys.OS, name string) error {
	if !sysOS.AppArmorAdmin {
		return nil
	}

	return runApparmor(sysOS, cmdLoad, name)
}

// unloadProfile removes the profile from the kernel.
func unloadProfile(sysOS *sys.OS, fullName string, name string) error {
	if !sysOS.AppArmorAvailable {
		return nil
	}

	ok, err := hasProfile(fullName)
	if err != nil {
		return err
	}

	if !ok {
		return nil
	}

	return runApparmor(sysOS, cmdUnload, name)
}

// deleteProfile unloads and delete profile and cache for a profile.
func deleteProfile(sysOS *sys.OS, fullName string, name string) error {
	if !sysOS.AppArmorAdmin {
		return nil
	}

	// Defend against path traversal attacks.
	if !shared.IsFileName(name) {
		return fmt.Errorf("Invalid profile name %q", name)
	}

	err := unloadProfile(sysOS, fullName, name)
	if err != nil {
		return err
	}

	cachePath := filepath.Join(sysOS.AppArmorCacheDir, name)
	err = os.Remove(cachePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("Failed to remove %s: %w", cachePath, err)
	}

	profilePath := filepath.Join(aaPath, "profiles", name)
	err = os.Remove(profilePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("Failed to remove %s: %w", profilePath, err)
	}

	return nil
}

// parserSupports checks if the parser supports a particular feature.
func parserSupports(sysOS *sys.OS, feature string) (bool, error) {
	if !sysOS.AppArmorAvailable {
		return false, nil
	}

	if feature == "unix" {
		minVer, err := version.NewDottedVersion("2.10.95")
		if err != nil {
			return false, err
		}

		return sysOS.AppArmorVersion.Compare(minVer) >= 0, nil
	}

	if feature == "mount_nosymfollow" || feature == "userns_rule" {
		sysOS.AppArmorFeatures.Lock()
		defer sysOS.AppArmorFeatures.Unlock()
		supported, ok := sysOS.AppArmorFeatures.Map[feature]
		if !ok {
			var err error
			supported, err = FeatureCheck(sysOS, feature)
			if err != nil {
				return false, nil
			}

			sysOS.AppArmorFeatures.Map[feature] = supported
		}

		return supported, nil
	}

	return false, nil
}

// profileName handles generating valid profile names.
func profileName(prefix string, name string) string {
	separators := 1
	if len(prefix) > 0 {
		separators = 2
	}

	// Max length in AppArmor is 253 chars.
	if len(name)+len(prefix)+3+separators >= 253 {
		hash := sha256.New()
		_, _ = io.WriteString(hash, name)
		name = hex.EncodeToString(hash.Sum(nil))
	}

	if len(prefix) > 0 {
		return "lxd_" + prefix + "-" + name
	}

	return "lxd-" + name
}
