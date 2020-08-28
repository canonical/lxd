package apparmor

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/version"
)

const (
	cmdLoad   = "r"
	cmdUnload = "R"
	cmdParse  = "Q"
)

var aaPath = shared.VarPath("security", "apparmor")

// runApparmor runs the relevant AppArmor command.
func runApparmor(state *state.State, command string, name string) error {
	if !state.OS.AppArmorAvailable {
		return nil
	}

	_, err := shared.RunCommand("apparmor_parser", []string{
		fmt.Sprintf("-%sWL", command),
		filepath.Join(aaPath, "cache"),
		filepath.Join(aaPath, "profiles", name),
	}...)

	if err != nil {
		return err
	}

	return nil
}

// createNamespace creates a new AppArmor namespace.
func createNamespace(state *state.State, name string) error {
	if !state.OS.AppArmorAvailable {
		return nil
	}

	if !state.OS.AppArmorStacking || state.OS.AppArmorStacked {
		return nil
	}

	p := filepath.Join("/sys/kernel/security/apparmor/policy/namespaces", name)
	err := os.Mkdir(p, 0755)
	if err != nil && !os.IsExist(err) {
		return err
	}

	return nil
}

// deleteNamespace destroys an AppArmor namespace.
func deleteNamespace(state *state.State, name string) error {
	if !state.OS.AppArmorAvailable {
		return nil
	}

	if !state.OS.AppArmorStacking || state.OS.AppArmorStacked {
		return nil
	}

	p := filepath.Join("/sys/kernel/security/apparmor/policy/namespaces", name)
	err := os.Remove(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// hasProfile checks if the profile is already loaded.
func hasProfile(state *state.State, name string) (bool, error) {
	mangled := strings.Replace(name, "/", ".", -1)

	profilesPath := "/sys/kernel/security/apaprmor/policy/profiles"
	if shared.PathExists(profilesPath) {
		entries, err := ioutil.ReadDir(profilesPath)
		if err != nil {
			return false, err
		}

		for _, entry := range entries {
			fields := strings.Split(entry.Name(), ".")
			if mangled == strings.Join(fields[0:len(fields)-2], ".") {
				return true, nil
			}
		}
	}

	return false, nil
}

// parseProfile parses the profile without loading it into the kernel.
func parseProfile(state *state.State, name string) error {
	if !state.OS.AppArmorAvailable {
		return nil
	}

	return runApparmor(state, cmdParse, name)
}

// loadProfile loads the AppArmor profile into the kernel.
func loadProfile(state *state.State, name string) error {
	if !state.OS.AppArmorAdmin {
		return nil
	}

	return runApparmor(state, cmdLoad, name)
}

// unloadProfile removes the profile from the kernel.
func unloadProfile(state *state.State, name string) error {
	if !state.OS.AppArmorAvailable {
		return nil
	}

	ok, err := hasProfile(state, name)
	if err != nil {
		return err
	}

	if !ok {
		return nil
	}

	return runApparmor(state, cmdUnload, name)
}

// deleteProfile unloads and delete profile and cache for a profile.
func deleteProfile(state *state.State, name string) error {
	if !state.OS.AppArmorAdmin {
		return nil
	}

	cacheDir, err := getCacheDir(state)
	if err != nil {
		return err
	}

	err = unloadProfile(state, name)
	if err != nil {
		return err
	}

	err = os.Remove(filepath.Join(cacheDir, name))
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrapf(err, "Failed to remove: %s", filepath.Join(cacheDir, name))
	}

	err = os.Remove(filepath.Join(aaPath, "profiles", name))
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrapf(err, "Failed to remove: %s", filepath.Join(aaPath, "profiles", name))
	}

	return nil
}

// parserSupports checks if the parser supports a particular feature.
func parserSupports(state *state.State, feature string) (bool, error) {
	if !state.OS.AppArmorAvailable {
		return false, nil
	}

	ver, err := getVersion(state)
	if err != nil {
		return false, err
	}

	if feature == "unix" {
		minVer, err := version.NewDottedVersion("2.10.95")
		if err != nil {
			return false, err
		}

		return ver.Compare(minVer) >= 0, nil
	}

	return false, nil
}

// getVersion reads and parses the AppArmor version.
func getVersion(state *state.State) (*version.DottedVersion, error) {
	if !state.OS.AppArmorAvailable {
		return version.NewDottedVersion("0.0")
	}

	out, err := shared.RunCommand("apparmor_parser", "--version")
	if err != nil {
		return nil, err
	}

	fields := strings.Fields(strings.Split(out, "\n")[0])
	return version.NewDottedVersion(fields[len(fields)-1])
}

// getCacheDir returns the applicable AppArmor cache directory.
func getCacheDir(state *state.State) (string, error) {
	basePath := filepath.Join(aaPath, "cache")

	if !state.OS.AppArmorAvailable {
		return basePath, nil
	}

	ver, err := getVersion(state)
	if err != nil {
		return "", err
	}

	// Multiple policy cache directories were only added in v2.13.
	minVer, err := version.NewDottedVersion("2.13")
	if err != nil {
		return "", err
	}

	if ver.Compare(minVer) < 0 {
		return basePath, nil
	}

	output, err := shared.RunCommand("apparmor_parser", "-L", basePath, "--print-cache-dir")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(output), nil
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
		io.WriteString(hash, name)
		name = fmt.Sprintf("%x", hash.Sum(nil))
	}

	if len(prefix) > 0 {
		return fmt.Sprintf("lxd_%s-%s", prefix, name)
	}

	return fmt.Sprintf("lxd-%s", name)
}
