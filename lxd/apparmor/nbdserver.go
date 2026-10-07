package apparmor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/revert"
)

var nbdServerProfileTpl = template.Must(template.New("nbdServerProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>

  # Allow processes to send us signals by default
  signal (receive),

  /sys/devices/**/block/*/queue/* r,
  /sys/devices/system/node/ r,
  /sys/devices/system/node/** r,

  "{{ .execPath }}" mr,
  "{{ .socketPath }}" rw,

{{- range $index, $element := .readPaths }}
  "{{ $element }}" rk,
{{- end }}

{{- range $index, $element := .writePaths }}
  "{{ $element }}" rwk,
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

// rulePathEscaper escapes the characters that the AppArmor parser interprets in a quoted path.
// Storage pool, instance snapshot and volume names are part of the paths of an NBD server and can contain them.
// The parser interprets ^ only inside [] and @ only before {, so escaping [ and { covers both.
var rulePathEscaper = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`, `]`, `\]`, `{`, `\{`, `}`, `\}`, `"`, `\"`)

// NBDServerWrapper runs the qemu-nbd or qemu-storage-daemon command under a restricted AppArmor profile.
// The profile allows the command to create socketPath, to read readPaths and to read and write writePaths.
// It returns a cleanup function that deletes the AppArmor profile that the command is running in.
func NBDServerWrapper(sysOS *sys.OS, cmd *exec.Cmd, socketPath string, readPaths []string, writePaths []string) (func(), error) {
	if !sysOS.AppArmorAvailable || !sysOS.AppArmorAdmin {
		return func() {}, nil
	}

	revert := revert.New()
	defer revert.Fail()

	// Load the profile.
	profileName, err := nbdServerProfileLoad(sysOS, cmd.Args[0], socketPath, readPaths, writePaths)
	if err != nil {
		return nil, fmt.Errorf("Failed loading %s profile: %w", cmd.Args[0], err)
	}

	cleanup := func() { _ = deleteProfile(sysOS, profileName, profileName) }
	revert.Add(cleanup)

	// Resolve aa-exec.
	execPath, err := exec.LookPath("aa-exec")
	if err != nil {
		return nil, err
	}

	// Override the command.
	newArgs := make([]string, 0, 3+len(cmd.Args))
	newArgs = append(newArgs, "aa-exec", "-p", profileName)
	newArgs = append(newArgs, cmd.Args...)
	cmd.Args = newArgs
	cmd.Path = execPath

	revert.Success()
	return cleanup, nil
}

// nbdServerProfileLoad loads [nbdServerProfileTpl] with the given arguments.
// A name is generated at random for the profile, as every NBD session runs a command of its own.
func nbdServerProfileLoad(sysOS *sys.OS, command string, socketPath string, readPaths []string, writePaths []string) (string, error) {
	revert := revert.New()
	defer revert.Fail()

	// Generate a temporary profile name.
	name := profileName(command, uuid.New().String())
	profilePath := filepath.Join(aaPath, "profiles", name)

	// Generate the profile
	content, err := nbdServerProfile(name, command, socketPath, readPaths, writePaths)
	if err != nil {
		return "", err
	}

	// Write it to disk.
	err = os.WriteFile(profilePath, []byte(content), 0600)
	if err != nil {
		return "", err
	}

	revert.Add(func() { _ = os.Remove(profilePath) })

	// Load it.
	err = loadProfile(sysOS, name)
	if err != nil {
		return "", err
	}

	revert.Success()
	return name, nil
}

// nbdServerProfile generates the AppArmor profile of the given NBD server command.
func nbdServerProfile(name string, command string, socketPath string, readPaths []string, writePaths []string) (string, error) {
	// Fully deref the executable path.
	execPath, err := exec.LookPath(command)
	if err != nil {
		return "", err
	}

	fullPath, err := filepath.EvalSymlinks(execPath)
	if err == nil {
		execPath = fullPath
	}

	// The socket does not exist yet, so only its directory is dereferenced.
	socketDir, err := filepath.EvalSymlinks(filepath.Dir(socketPath))
	if err == nil {
		socketPath = filepath.Join(socketDir, filepath.Base(socketPath))
	}

	// rulePaths returns the given paths dereferenced and escaped.
	rulePaths := func(paths []string) []string {
		escaped := make([]string, 0, len(paths))
		for _, path := range paths {
			fullPath, err := filepath.EvalSymlinks(path)
			if err == nil {
				path = fullPath
			}

			escaped = append(escaped, rulePathEscaper.Replace(path))
		}

		return escaped
	}

	// Only absolute entries are named, as an empty entry renders a rule that allows every path.
	libraryPaths := []string{}
	for entry := range strings.SplitSeq(os.Getenv("LD_LIBRARY_PATH"), ":") {
		if filepath.IsAbs(entry) {
			libraryPaths = append(libraryPaths, rulePathEscaper.Replace(entry))
		}
	}

	sb := &strings.Builder{}
	err = nbdServerProfileTpl.Execute(sb, map[string]any{
		"name":         name,
		"execPath":     rulePathEscaper.Replace(execPath),
		"socketPath":   rulePathEscaper.Replace(socketPath),
		"readPaths":    rulePaths(readPaths),
		"writePaths":   rulePaths(writePaths),
		"snap":         shared.InSnap(),
		"libraryPaths": libraryPaths,
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}
