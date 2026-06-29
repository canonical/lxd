package apparmor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
)

// compressProfileTpl is a template for a highly restricted apparmor profile for use when compressing instances or
// storage volumes with a user supplied compression algorithm. Commands running under this profile should expect input
// via stdin. The template only grants write access to an output file if requested (`hasDstPath` template argument),
// otherwise it is expected that the command should write to stdout. Only the given set of command paths may be executed.
var compressProfileTpl = template.Must(template.New("compressProfile").Parse(`#include <tunables/global>
profile "{{.name}}" {
  #include <abstractions/base>
{{range $index, $element := .allowedCommandPaths}}
  {{$element}} mixr,
{{- end }}

{{- if .hasDstPath }}
	{{ .dstPath }} rw,
{{- end }}

  signal (receive) set=("term"),

{{- if .snap }}
  # Snap-specific libraries
  /snap/lxd/*/lib/**.so*                  mr,
{{- end }}
}
`))

// CompressWrapper should be used when applying a user specified compression algorithm.
// The given command is directly modified such that it runs under `aa-exec` with a restricted profile (see [compressProfileTpl]).
// It returns a cleanup function that deletes the AppArmor profile that the command is running in.
func CompressWrapper(s *state.State, cmd *exec.Cmd, dstPath string, extraAllowedCommands []string) (func(), error) {
	if !s.OS.AppArmorAvailable {
		return func() {}, nil
	}

	if dstPath != "" {
		fullPath, err := filepath.EvalSymlinks(dstPath)
		if err == nil {
			dstPath = fullPath
		}
	}

	cmds := append(extraAllowedCommands, cmd.Args[0])
	allowedCmdPaths := make([]string, 0, len(cmds))
	for _, c := range cmds {
		cmdPath, err := exec.LookPath(c)
		if err != nil {
			return nil, fmt.Errorf("Failed starting compression: Failed finding executable: %w", err)
		}

		allowedCmdPaths = append(allowedCmdPaths, cmdPath)
	}

	// Load the profile.
	profileName, err := compressProfileLoad(s, allowedCmdPaths, dstPath)
	if err != nil {
		return nil, fmt.Errorf("Failed loading compression profile: %w", err)
	}

	cleanup := func() { _ = deleteProfile(s, profileName, profileName) }
	fail := true
	defer func() {
		if fail {
			cleanup()
		}
	}()

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

	fail = false
	return cleanup, nil
}

// compressProfileLoad loads [compressProfileTpl] with the given arguments. A name is generated at random for the profile
// because it should be loaded and unloaded as necessary using [CompressWrapper].
func compressProfileLoad(s *state.State, allowedCommandPaths []string, dstPath string) (string, error) {
	// Generate a temporary profile name.
	name := profileName("compress", uuid.New().String())
	profilePath := filepath.Join(aaPath, "profiles", name)

	// Generate the profile
	content, err := compressProfile(name, allowedCommandPaths, dstPath)
	if err != nil {
		return "", err
	}

	// Write it to disk.
	err = os.WriteFile(profilePath, []byte(content), 0600)
	if err != nil {
		return "", err
	}

	fail := true
	defer func() {
		if fail {
			_ = os.Remove(profilePath)
		}
	}()

	// Load it.
	err = loadProfile(s, name)
	if err != nil {
		return "", err
	}

	fail = false
	return name, nil
}

// compressProfile generates the AppArmor profile template from the given destination path.
func compressProfile(name string, allowedCommandPaths []string, dstPath string) (string, error) {
	sb := &strings.Builder{}
	err := compressProfileTpl.Execute(sb, map[string]any{
		"allowedCommandPaths": allowedCommandPaths,
		"name":                name,
		"hasDstPath":          dstPath != "",
		"dstPath":             dstPath,
		"snap":                shared.InSnap(),
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}
