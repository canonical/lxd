package apparmor

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared"
)

var swtpmProfileTpl = template.Must(template.New("swtpmProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>

  # Allow processes to send us signals by default
  signal (receive),

  "{{ .execPath }}" mr,
  {{ .rootPath }}/etc/ssl/openssl.cnf r,

  # TPM state
  "{{ .statePath }}/" r,
  "{{ .statePath }}/**" rwk,

{{- if .container }}

  # Traverse the instance directory, which belongs to the container's root and has mode 0100
  capability dac_read_search,

  # vTPM proxy, whose device creation requires CAP_SYS_ADMIN
  capability sys_admin,
  /dev/vtpmx rw,
  "{{ .logPath }}" w,
{{- else }}

  # Control channel to QEMU, passed in as an already listening socket
  unix (accept, send, receive, getattr, getopt, setopt, shutdown) type=stream,
{{- end }}

{{- if .snap }}

  # Snap-specific libraries
  /snap/lxd/*/lib/**.so* mr,
{{- end }}

{{- if .libraryPaths }}

  # Entries from LD_LIBRARY_PATH
{{- range $index, $element := .libraryPaths }}
  "{{ $element }}/**" mr,
{{- end }}
{{- end }}
}
`))

// swtpmProfileID returns an identifier for the swtpm profile of the given device that is unique within LXD
// and usable as a file name.
func swtpmProfileID(inst instance, dev device) string {
	// Neither project nor instance names can contain "_", so the device name is all that follows the second one.
	id := inst.Project().Name + "_" + inst.Name() + "_" + filesystem.PathNameEncode(dev.Name())

	// Hash IDs that deleteProfile would refuse or with characters that the kernel drops, which hasProfile can't find.
	if strings.Contains(id, "..") || strings.ContainsFunc(id, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("._-", r)
	}) {
		sum := sha256.Sum256([]byte(id))
		id = hex.EncodeToString(sum[:])
	}

	return id
}

// SwtpmProfileName returns the AppArmor profile name.
func SwtpmProfileName(inst instance, dev device) string {
	return profileName("swtpm", swtpmProfileID(inst, dev)+"_<"+shared.VarPath("")+">")
}

// swtpmProfileFilename returns the name of the on-disk profile.
func swtpmProfileFilename(inst instance, dev device) string {
	return profileName("swtpm", swtpmProfileID(inst, dev))
}

// SwtpmLoad ensures that the swtpm profile of the given device is loaded into the kernel.
// The profile lets swtpm use the TPM state in statePath and, for containers, write to logPath.
// It returns the name of the profile to run swtpm under, or an empty string if AppArmor cannot be used.
func SwtpmLoad(sysOS *sys.OS, inst instance, dev device, statePath string, logPath string) (string, error) {
	if !sysOS.AppArmorAvailable || !sysOS.AppArmorAdmin {
		return "", nil
	}

	profilePath := filepath.Join(aaPath, "profiles", swtpmProfileFilename(inst, dev))
	content, err := os.ReadFile(profilePath)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	updated, err := swtpmProfile(SwtpmProfileName(inst, dev), inst.Type() == instancetype.Container, statePath, logPath)
	if err != nil {
		return "", err
	}

	if string(content) != updated {
		err = os.WriteFile(profilePath, []byte(updated), 0600)
		if err != nil {
			return "", err
		}
	}

	err = loadProfile(sysOS, swtpmProfileFilename(inst, dev))
	if err != nil {
		return "", err
	}

	return SwtpmProfileName(inst, dev), nil
}

// SwtpmUnload unloads the swtpm profile of the given device from the kernel.
// This does not delete the profile from disk or cache.
func SwtpmUnload(sysOS *sys.OS, inst instance, dev device) error {
	return unloadProfile(sysOS, SwtpmProfileName(inst, dev), swtpmProfileFilename(inst, dev))
}

// SwtpmDelete removes the swtpm profile of the given device from cache/disk.
func SwtpmDelete(sysOS *sys.OS, inst instance, dev device) error {
	return deleteProfile(sysOS, SwtpmProfileName(inst, dev), swtpmProfileFilename(inst, dev))
}

// swtpmProfile generates the AppArmor profile of swtpm.
func swtpmProfile(name string, container bool, statePath string, logPath string) (string, error) {
	execPath, err := exec.LookPath("swtpm")
	if err != nil {
		return "", err
	}

	// AppArmor requires deref of all paths.
	fullPath, err := filepath.EvalSymlinks(execPath)
	if err == nil {
		execPath = fullPath
	}

	fullPath, err = filepath.EvalSymlinks(statePath)
	if err == nil {
		statePath = fullPath
	}

	// The log file may not exist yet, so only its directory is dereferenced.
	if logPath != "" {
		logDir, err := filepath.EvalSymlinks(filepath.Dir(logPath))
		if err == nil {
			logPath = filepath.Join(logDir, filepath.Base(logPath))
		}
	}

	rootPath := ""
	if shared.InSnap() {
		rootPath = "/var/lib/snapd/hostfs"
	}

	sb := &strings.Builder{}
	err = swtpmProfileTpl.Execute(sb, map[string]any{
		"name":         name,
		"rootPath":     rootPath,
		"container":    container,
		"execPath":     rulePathEscaper.Replace(execPath),
		"statePath":    rulePathEscaper.Replace(statePath),
		"logPath":      rulePathEscaper.Replace(logPath),
		"snap":         shared.InSnap(),
		"libraryPaths": escapeRulePaths(ldLibraryPaths()),
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}
