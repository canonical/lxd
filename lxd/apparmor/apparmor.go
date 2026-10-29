package apparmor

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
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

	_, err := shared.RunCommand(context.TODO(), "apparmor_parser", []string{
		command,
		"--write-cache", "--cache-loc", sysOS.AppArmorCacheLoc,
		filepath.Join(aaPath, "profiles", name),
	}...)

	if err != nil {
		return err
	}

	return nil
}

// escapeRulePaths returns the given paths escaped for use inside a quoted AppArmor rule.
func escapeRulePaths(paths []string) []string {
	escaped := make([]string, 0, len(paths))
	for _, path := range paths {
		escaped = append(escaped, rulePathEscaper.Replace(path))
	}

	return escaped
}

// originToken matches $ORIGIN like the dynamic loader, which ignores it when followed by an identifier character.
var originToken = regexp.MustCompile(`\$(ORIGIN\b|\{ORIGIN\})`)

// libraryPathEntries returns the entries of a dynamic loader search path that
// are safe to render as profile rules: absolute, not the root directory and
// without a dynamic loader token.
// A non-empty origin expands the $ORIGIN token to that directory. Every entry is
// then normalized with filepath.Clean, as AppArmor matches ".." literally and a
// rule that keeps it never applies to the path the loader opens. That
// normalization is lexical, so an entry that reaches a directory through a
// symlink keeps the symlink rather than being dereferenced.
func libraryPathEntries(value string, origin string) []string {
	entries := []string{}
	for entry := range strings.SplitSeq(value, ":") {
		if origin != "" {
			entry = originToken.ReplaceAllLiteralString(entry, origin)
		}

		// Normalize the entry, so that a rule matches the path the loader opens
		// rather than one that keeps a ".." or a trailing slash.
		entry = filepath.Clean(entry)

		// Empty and relative entries are not valid in a profile, the root
		// directory would render a rule matching every path, and other loader
		// tokens (e.g. $LIB) are not expanded here.
		if !filepath.IsAbs(entry) || entry == "/" || strings.Contains(entry, "$") {
			continue
		}

		if slices.Contains(entries, entry) {
			continue
		}

		entries = append(entries, entry)
	}

	return entries
}

// ldLibraryPaths returns the absolute entries of LD_LIBRARY_PATH, except the root directory.
// Empty entries and the root directory would render a rule matching every path and relative entries are not valid in a profile.
func ldLibraryPaths() []string {
	return libraryPathEntries(os.Getenv("LD_LIBRARY_PATH"), "")
}

// execLibraryPaths returns the entries of the given binary's DT_RPATH and
// DT_RUNPATH, except the root directory and with $ORIGIN expanded.
// The dynamic loader searches DT_RPATH before LD_LIBRARY_PATH and DT_RUNPATH
// after it, and ignores DT_RPATH when DT_RUNPATH is set, so these are allowed
// in addition to LD_LIBRARY_PATH rather than as a fallback for it.
func execLibraryPaths(exe string) []string {
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		logger.Warn("Failed resolving the executable for AppArmor library rules", logger.Ctx{"path": exe, "err": err})

		return nil
	}

	paths, err := exeLibraryPaths(resolved)
	if err != nil {
		logger.Warn("Failed reading the library paths of the executable for AppArmor profiles", logger.Ctx{"path": resolved, "err": err})

		return nil
	}

	return paths
}

// exeLibraryPaths returns the DT_RPATH and DT_RUNPATH entries of the given binary.
// A binary that cannot be read as an ELF file is reported as an error, and one
// with neither entry yields no paths.
func exeLibraryPaths(exe string) ([]string, error) {
	f, err := elf.Open(exe)
	if err != nil {
		return nil, fmt.Errorf("Failed opening %q: %w", exe, err)
	}

	defer func() { _ = f.Close() }()

	// DT_RPATH and DT_RUNPATH each hold a colon-separated list of directories.
	// Both are collected into one list, which libraryPathEntries deduplicates.
	values := []string{}
	for _, tag := range []elf.DynTag{elf.DT_RPATH, elf.DT_RUNPATH} {
		tagValues, err := f.DynString(tag)
		if err != nil {
			continue
		}

		values = append(values, tagValues...)
	}

	return libraryPathEntries(strings.Join(values, ":"), filepath.Dir(exe)), nil
}

// profileLibraryPaths returns the library directories to allow in the profile of
// the given binary, which must be the path its exePath rule names.
// It combines LD_LIBRARY_PATH with the binary's own RPATH/RUNPATH, so that a
// build which finds its libraries through RPATH alone can still load them when
// confined, even though sudo and service managers strip LD_LIBRARY_PATH.
func profileLibraryPaths(exe string) []string {
	paths := ldLibraryPaths()
	for _, entry := range execLibraryPaths(exe) {
		if !slices.Contains(paths, entry) {
			paths = append(paths, entry)
		}
	}

	return paths
}

// createNamespace creates a new AppArmor namespace.
// It reports whether the namespace was created, as opposed to already existing.
func createNamespace(sysOS *sys.OS, name string) (bool, error) {
	if !sysOS.AppArmorAvailable {
		return false, nil
	}

	if !sysOS.AppArmorStacking || sysOS.AppArmorStacked {
		return false, nil
	}

	p := filepath.Join("/sys/kernel/security/apparmor/policy/namespaces", name)
	err := os.Mkdir(p, 0755)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return true, nil
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
	entries, err := os.ReadDir(profilesPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}

		return false, err
	}

	mangled := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(name, "/", "."), "<", ""), ">", "")
	for _, entry := range entries {
		entryName := entry.Name()
		idx := strings.LastIndex(entryName, ".")
		if idx >= 0 && mangled == entryName[:idx] {
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
		return fmt.Errorf("Failed removing %q: %w", cachePath, err)
	}

	profilePath := filepath.Join(aaPath, "profiles", name)
	err = os.Remove(profilePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("Failed removing %q: %w", profilePath, err)
	}

	return nil
}

// parserSupports checks if the parser supports a particular feature.
func parserSupports(sysOS *sys.OS, feature string) (bool, error) {
	if !sysOS.AppArmorAvailable {
		return false, nil
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

// goCoverDir returns the escaped directory that Go coverage data is written to by this binary and the ones it spawns.
// It returns an empty string unless GOCOVERDIR is an absolute path.
func goCoverDir() string {
	dir := os.Getenv("GOCOVERDIR")
	if !filepath.IsAbs(dir) {
		return ""
	}

	// AppArmor requires deref of all paths.
	fullPath, err := filepath.EvalSymlinks(dir)
	if err == nil {
		dir = fullPath
	}

	return rulePathEscaper.Replace(dir)
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
