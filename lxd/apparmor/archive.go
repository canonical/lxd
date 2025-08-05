package apparmor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/canonical/lxd/lxd/config"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared"
)

var archiveProfileTpl = template.Must(template.New("archiveProfile").Parse(`#include <tunables/global>
profile "{{.name}}" {
  #include <abstractions/base>
  #include <abstractions/nameservice>

{{range $index, $element := .allowedCommandPaths}}
  {{$element}} mixr,
{{- end }}

{{range $index, $element := .imagesPaths}}
  {{$element}}/** r,
{{- end }}

{{range $index, $element := .backupsPaths}}
  {{$element}}/** rw,
{{- end }}

  {{ .outputPath }}/ rw,
  {{ .outputPath }}/** rwl,

  signal (receive) set=("term"),

  # Capabilities
  capability chown,
  capability dac_override,
  capability dac_read_search,
  capability fowner,
  capability fsetid,
  capability mknod,
  capability setfcap,

{{- if .snap }}
  # Snap-specific libraries
  /snap/lxd/*/lib/**.so*                  mr,
{{- end }}
}
`))

// ArchiveLoad ensures that the archive's policy is loaded into the kernel.
func ArchiveLoad(s *state.State, outputPath string, allowedCommandPaths []string) error {
	profileFileName := ArchiveProfileFilename(outputPath)

	// Defend against path traversal attacks.
	if !shared.IsFileName(profileFileName) {
		return fmt.Errorf("Invalid profile name %q", profileFileName)
	}

	profile := filepath.Join(aaPath, "profiles", profileFileName)
	content, err := os.ReadFile(profile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	updated, err := archiveProfile(s, outputPath, allowedCommandPaths)
	if err != nil {
		return err
	}

	if string(content) != string(updated) {
		err = os.WriteFile(profile, []byte(updated), 0600)
		if err != nil {
			return err
		}
	}

	err = loadProfile(s.OS, profileFileName)
	if err != nil {
		return err
	}

	return nil
}

// ArchiveUnload ensures that the archive's policy namespace is unloaded to free kernel memory.
// This does not delete the policy from disk or cache.
func ArchiveUnload(sysOS *sys.OS, outputPath string) error {
	err := unloadProfile(sysOS, ArchiveProfileName(outputPath), ArchiveProfileFilename(outputPath))
	if err != nil {
		return err
	}

	return nil
}

// ArchiveDelete removes the profile from cache/disk.
func ArchiveDelete(sysOS *sys.OS, outputPath string) error {
	return deleteProfile(sysOS, ArchiveProfileName(outputPath), ArchiveProfileFilename(outputPath))
}

// archiveProfile generates the AppArmor profile template from the given destination path.
func archiveProfile(s *state.State, outputPath string, allowedCommandPaths []string) (string, error) {
	rootPath := ""
	if shared.InSnap() {
		rootPath = "/var/lib/snapd/hostfs"
	}

	// Attempt to deref all paths.
	outputPathFull, err := filepath.EvalSymlinks(outputPath)
	if err != nil {
		outputPathFull = outputPath // Use requested path if cannot resolve it.
	}

	// Add all paths configured as daemon storage or project storage.
	// We store the paths in a map[string]bool to ensure uniqueness.
	daemonStorageVolumePaths := make(map[config.DaemonStorageType]map[string]struct{})
	daemonStorageVolumePaths[config.DaemonStorageTypeImages] = make(map[string]struct{})
	daemonStorageVolumePaths[config.DaemonStorageTypeBackups] = make(map[string]struct{})
	projectStoragePathFuncs := map[config.DaemonStorageType]func(projectName string) string{
		config.DaemonStorageTypeImages:  s.ImagesStoragePath,
		config.DaemonStorageTypeBackups: s.BackupsStoragePath,
	}

	// Add the daemon storage which can't be used by any of the projects.
	// The daemon storage volumes might not be configured in the node config, so we add them manually.
	for _, storageType := range []config.DaemonStorageType{config.DaemonStorageTypeImages, config.DaemonStorageTypeBackups} {
		volumePath := projectStoragePathFuncs[storageType]("")
		// Attempt to dereference the symlink, if it fails, use the original path
		volumePathFull, err := filepath.EvalSymlinks(volumePath)
		if err == nil {
			volumePath = volumePathFull
		}

		daemonStorageVolumePaths[storageType][volumePath] = struct{}{}
	}

	// Add all the project storage volumes, which are configured in the node config.
	for key := range s.LocalConfig.Dump() {
		// Skip over any keys other than project storage volume keys.
		projectName, storageType := config.ParseDaemonStorageConfigKey(key)
		if projectName == "" {
			continue
		}

		volumePath := projectStoragePathFuncs[storageType](projectName)
		// Attempt to dereference the symlink, if it fails, use the original path
		volumePathFull, err := filepath.EvalSymlinks(volumePath)
		if err == nil {
			volumePath = volumePathFull
		}

		daemonStorageVolumePaths[storageType][volumePath] = struct{}{}
	}

	// Convert the maps to slices for the template.
	daemonStorageVolumePathsSlices := make(map[config.DaemonStorageType][]string)
	daemonStorageVolumePathsSlices[config.DaemonStorageTypeImages] = make([]string, 0, len(daemonStorageVolumePaths[config.DaemonStorageTypeImages]))
	daemonStorageVolumePathsSlices[config.DaemonStorageTypeBackups] = make([]string, 0, len(daemonStorageVolumePaths[config.DaemonStorageTypeBackups]))
	for path := range daemonStorageVolumePaths[config.DaemonStorageTypeImages] {
		daemonStorageVolumePathsSlices[config.DaemonStorageTypeImages] = append(daemonStorageVolumePathsSlices[config.DaemonStorageTypeImages], path)
	}

	for path := range daemonStorageVolumePaths[config.DaemonStorageTypeBackups] {
		daemonStorageVolumePathsSlices[config.DaemonStorageTypeBackups] = append(daemonStorageVolumePathsSlices[config.DaemonStorageTypeBackups], path)
	}

	derefCommandPaths := make([]string, len(allowedCommandPaths))
	for i, cmd := range allowedCommandPaths {
		cmdFull, err := filepath.EvalSymlinks(cmd)
		if err == nil {
			derefCommandPaths[i] = cmdFull
		} else {
			derefCommandPaths[i] = cmd
		}
	}

	// Render the profile.
	sb := &strings.Builder{}
	err = archiveProfileTpl.Execute(sb, map[string]any{
		"name":                ArchiveProfileName(outputPath), // Use non-deferenced outputPath for name.
		"outputPath":          outputPathFull,                 // Use deferenced path in AppArmor profile.
		"rootPath":            rootPath,
		"backupsPaths":        daemonStorageVolumePathsSlices[config.DaemonStorageTypeBackups],
		"imagesPaths":         daemonStorageVolumePathsSlices[config.DaemonStorageTypeImages],
		"allowedCommandPaths": derefCommandPaths,
		"snap":                shared.InSnap(),
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}

// ArchiveProfileName returns the AppArmor profile name.
func ArchiveProfileName(outputPath string) string {
	name := strings.ReplaceAll(strings.Trim(outputPath, "/"), "/", "-")
	return profileName("archive", name)
}

// ArchiveProfileFilename returns the name of the on-disk profile name.
func ArchiveProfileFilename(outputPath string) string {
	name := strings.ReplaceAll(strings.Trim(outputPath, "/"), "/", "-")
	return profileName("archive", name)
}
