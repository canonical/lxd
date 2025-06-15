package apparmor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
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

	projectImagesVolumes := make(map[string]bool)
	projectBackupsVolumes := make(map[string]bool)
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projects, err := dbCluster.GetProjects(ctx, tx.Tx())
		if err != nil {
			return err
		}

		for _, project := range projects {
			config, err := dbCluster.GetProjectConfig(ctx, tx.Tx(), project.Name)
			if err != nil {
				return fmt.Errorf("Failed to fetch project config: %w", err)
			}

			projectImagesVolumes[config["storage.images_volume"]] = true
			projectBackupsVolumes[config["storage.backups_volume"]] = true
		}

		return nil
	})
	if err != nil {
		return "", err
	}

	projectImagesVolumes[s.LocalConfig.StorageImagesVolume()] = true
	imagesPaths := make([]string, 0, len(projectImagesVolumes)+1)
	for projectImageVolume := range projectImagesVolumes {
		imagesPaths = append(imagesPaths, s.ImagesStoragePath(projectImageVolume))
	}

	projectBackupsVolumes[s.LocalConfig.StorageBackupsVolume()] = true
	backupsPaths := make([]string, 0, len(projectBackupsVolumes)+1)
	for projectBackupsVolume := range projectBackupsVolumes {
		backupsPaths = append(backupsPaths, s.BackupsStoragePath(projectBackupsVolume))
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
