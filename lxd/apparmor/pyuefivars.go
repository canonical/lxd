package apparmor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/canonical/lxd/lxd/subprocess"
	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared"
)

var pythonUEFIVarsProfileTpl = template.Must(template.New("pythonUEFIVarsProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>

  # Allow processes to send us signals by default
  signal (receive),

  # Python locations
  /usr/bin/python* mixr,
  /bin**/*.py r,
  /usr/lib/python** r,
  /lib/python** r,
  /**/pyvenv.cfg r,

{{- if .snap }}
  # Snap-specific libraries
  /snap/lxd/*/lib/**.so* mr,

  # Snap-specific Python locations
  /snap/lxd/*/bin**/ r,
  /snap/lxd/*/bin**/*.py r,
  /snap/lxd/*/usr/lib/python** r,
  /snap/lxd/*/lib/python** r,
{{- end }}

{{if .libraryPath -}}
  # Entries from LD_LIBRARY_PATH
{{range $index, $element := .libraryPath}}
  {{$element}}/** mr,
{{- end }}
{{- end }}
}
`))

type nopWriteCloser struct {
	io.Writer
}

// Close provides a nop implementation for io.WriteCloser.
func (*nopWriteCloser) Close() error {
	return nil
}

// PythonUEFIVars runs pyuefivars with an AppArmor profile based on the efiVarsPath supplied.
func PythonUEFIVars(sysOS *sys.OS, stdin io.Reader, stdout io.Writer, efiVarsPath string) error {
	var input io.ReadCloser
	var output io.WriteCloser

	cmd := []string{"uefivars.py"}
	if stdin == nil {
		f, err := os.OpenFile(efiVarsPath, os.O_RDONLY, 0)
		if err != nil {
			return err
		}

		defer func() { _ = f.Close() }()

		// read from file
		input = f
		output = &nopWriteCloser{stdout}
		cmd = append(cmd, "-o", "json", "-i", "edk2")
	} else {
		f, err := os.OpenFile(efiVarsPath, os.O_WRONLY, 0)
		if err != nil {
			return err
		}

		defer func() { _ = f.Close() }()

		input = io.NopCloser(stdin)
		// write to file
		output = f
		cmd = append(cmd, "-i", "json", "-o", "edk2")
	}

	profileName, err := pythonUEFIVarsProfileLoad(sysOS, efiVarsPath)
	if err != nil {
		return fmt.Errorf("Failed to load pyuefivars profile: %w", err)
	}

	defer func() {
		_ = deleteProfile(sysOS, profileName, profileName)
	}()

	var buffer bytes.Buffer
	p := subprocess.NewProcessWithFds(cmd[0], cmd[1:], input, output, &nullWriteCloser{&buffer})

	// Drop privileges
	p.SetCreds(sysOS.UnprivUID, sysOS.UnprivGID)

	// Set AppArmor profile to run with
	p.SetApparmor(profileName)

	err = p.Start(context.TODO())
	if err != nil {
		return fmt.Errorf("Failed running pyuefivars: %w", err)
	}

	_, err = p.Wait(context.TODO())
	if err != nil {
		return shared.NewRunError(cmd[0], cmd[1:], err, nil, &buffer)
	}

	return nil
}

// pythonUEFIVarsProfileLoad ensures that the pyuefivars's policy is loaded into the kernel.
func pythonUEFIVarsProfileLoad(sysOS *sys.OS, efiVarsPath string) (string, error) {
	name := fmt.Sprintf("<%s>", strings.ReplaceAll(strings.Trim(efiVarsPath, "/"), "/", "-"))
	profileName := profileName("pyuefivars", name)
	profilePath := filepath.Join(aaPath, "profiles", profileName)
	content, err := os.ReadFile(profilePath)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	updated, err := pythonUEFIVarsProfile(profileName)
	if err != nil {
		return "", err
	}

	if string(content) != string(updated) {
		err = os.WriteFile(profilePath, []byte(updated), 0600)
		if err != nil {
			return "", err
		}
	}

	err = loadProfile(sysOS, profileName)
	if err != nil {
		return "", err
	}

	return profileName, nil
}

// pythonUEFIVarsProfile generates the AppArmor profile.
func pythonUEFIVarsProfile(profileName string) (string, error) {
	// Render the profile.
	sb := &strings.Builder{}
	err := pythonUEFIVarsProfileTpl.Execute(sb, map[string]any{
		"name":        profileName,
		"snap":        shared.InSnap(),
		"libraryPath": strings.Split(os.Getenv("LD_LIBRARY_PATH"), ":"),
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}
