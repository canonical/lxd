package apparmor

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/lxc/lxd/lxd/sys"
	"github.com/lxc/lxd/shared"
)

var archiveProfileTpl = template.Must(template.New("archiveProfile").Parse(`#include <tunables/global>
profile "{{.name}}" {
  #include <abstractions/base>
  #include <abstractions/nameservice>

{{range $index, $element := .allowedCommandPaths}}
  {{$element}} mixr,
{{- end }}

  {{ .outputPath }}/ rw,
  {{ .outputPath }}/** rwl,
  {{ .varPath }}/backups/** rw,
  {{ .varPath }}/images/** r,

  signal (receive) set=("term") peer=unconfined,

  # Capabilities
  capability dac_read_search,
  capability dac_override,
  capability chown,
  capability fsetid,
  capability fowner,
  capability setfcap,

{{- if .snap }}
  # Snap-specific libraries
  /snap/lxd/*/lib/**.so*                  mr,
{{- end }}
}
`))

// ArchiveLoad ensures that the archive's policy is loaded into the kernel.
func ArchiveLoad(sysOS *sys.OS, outputPath string, allowedCommandPaths []string) error {
	profile := filepath.Join(aaPath, "profiles", ArchiveProfileFilename(outputPath))
	content, err := ioutil.ReadFile(profile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	updated, err := archiveProfile(outputPath, allowedCommandPaths)
	if err != nil {
		return err
	}

	if string(content) != string(updated) {
		err = ioutil.WriteFile(profile, []byte(updated), 0600)
		if err != nil {
			return err
		}
	}

	err = loadProfile(sysOS, ArchiveProfileFilename(outputPath))
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

// ArchiveDelete removes the profile from cache/disk
func ArchiveDelete(sysOS *sys.OS, outputPath string) error {
	return deleteProfile(sysOS, ArchiveProfileName(outputPath), ArchiveProfileFilename(outputPath))
}

// archiveProfile generates the AppArmor profile template from the given destination path.
func archiveProfile(outputPath string, allowedCommandPaths []string) (string, error) {
	rootPath := ""
	if shared.InSnap() {
		rootPath = "/var/lib/snapd/hostfs"
	}

	// Render the profile.
	var sb *strings.Builder = &strings.Builder{}
	err := archiveProfileTpl.Execute(sb, map[string]interface{}{
		"name":                ArchiveProfileName(outputPath),
		"outputPath":          outputPath,
		"rootPath":            rootPath,
		"varPath":             shared.VarPath(""),
		"allowedCommandPaths": allowedCommandPaths,
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}

// ArchiveProfileName returns the AppArmor profile name.
func ArchiveProfileName(outputPath string) string {
	name := strings.Replace(strings.Trim(outputPath, "/"), "/", "-", -1)
	return profileName("archive", name)
}

// ArchiveProfileFilename returns the name of the on-disk profile name.
func ArchiveProfileFilename(outputPath string) string {
	name := strings.Replace(strings.Trim(outputPath, "/"), "/", "-", -1)
	return profileName("archive", name)
}
