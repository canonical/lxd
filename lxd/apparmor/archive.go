package apparmor

import (
	"os"
	"path/filepath"
	"strings"
	"text/template"

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
	profile := filepath.Join(aaPath, "profiles", ArchiveProfileFilename(outputPath))
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

	err = loadProfile(s.OS, ArchiveProfileFilename(outputPath))
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
	projectImagesVolumes := make(map[string]string)
	projectBackupsVolumes := make(map[string]string)

	// First add the daemon storage which can't be used by any of the projects.
	projectImagesVolumes[s.LocalConfig.StorageImagesVolume("")] = ""
	projectBackupsVolumes[s.LocalConfig.StorageBackupsVolume("")] = ""

	// Add all the project storages, keyed by the project volume.
	// As multiple projects can share the volume, we care only about one of such projects,
	// which we'll add to the apparmor profile.
	for key, value := range s.LocalConfig.Dump() {
		if !strings.HasPrefix(key, "storage.project_") {
			continue
		}

		projectName, _ := strings.CutPrefix(key, "storage.project_")
		storageVolume, _ := value.(string)
		if strings.HasSuffix(key, ".images_volume") {
			projectName, _ = strings.CutSuffix(projectName, ".images_volume")
			projectImagesVolumes[storageVolume] = projectName
		}

		if strings.HasSuffix(key, ".backups_volume") {
			projectName, _ = strings.CutSuffix(projectName, ".backups_volume")
			projectBackupsVolumes[storageVolume] = projectName
		}
	}

	imagesPaths := make([]string, 0, len(projectImagesVolumes))
	for _, project := range projectImagesVolumes {
		imagesPaths = append(imagesPaths, s.ImagesStoragePath(project))
	}

	backupsPaths := make([]string, 0, len(projectBackupsVolumes)+1)
	for _, project := range projectBackupsVolumes {
		backupsPaths = append(backupsPaths, s.BackupsStoragePath(project))
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
		"backupsPaths":        backupsPaths,
		"imagesPaths":         imagesPaths,
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
