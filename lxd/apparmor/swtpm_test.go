package apparmor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/shared/api"
)

type swtpmTestInstance struct {
	instance

	project string
	name    string
}

func (i swtpmTestInstance) Project() api.Project {
	return api.Project{Name: i.project}
}

func (i swtpmTestInstance) Name() string {
	return i.name
}

type swtpmTestDevice struct {
	name string
}

func (d swtpmTestDevice) Config() deviceConfig.Device {
	return nil
}

func (d swtpmTestDevice) Name() string {
	return d.name
}

func TestSwtpmProfile(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, "swtpm"), nil, 0700)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", dir)
	t.Setenv("LD_LIBRARY_PATH", "")

	statePath := filepath.Join(dir, `tpm.dev{1}`)
	err = os.Mkdir(statePath, 0700)
	if err != nil {
		t.Fatal(err)
	}

	stateLink := filepath.Join(dir, "state-link")
	err = os.Symlink(statePath, stateLink)
	if err != nil {
		t.Fatal(err)
	}

	container, err := swtpmProfile("lxd_test", true, stateLink, filepath.Join(dir, "tpm.dev.log"))
	if err != nil {
		t.Fatal(err)
	}

	vm, err := swtpmProfile("lxd_test", false, stateLink, "")
	if err != nil {
		t.Fatal(err)
	}

	// The state path is dereferenced and escaped.
	escapedStatePath := filepath.Join(dir, `tpm.dev\{1\}`)
	for _, profile := range []string{container, vm} {
		for _, rule := range []string{
			`"` + filepath.Join(dir, "swtpm") + `" mr,`,
			`"` + escapedStatePath + `/" r,`,
			`"` + escapedStatePath + `/**" rwk,`,
		} {
			if !strings.Contains(profile, "\n  "+rule+"\n") {
				t.Errorf("Profile has no rule %q:\n%s", rule, profile)
			}
		}

		if strings.Contains(profile, stateLink) || strings.Contains(profile, "Entries from LD_LIBRARY_PATH") {
			t.Errorf("Profile has unexpected rules:\n%s", profile)
		}
	}

	for _, rule := range []string{"capability dac_read_search,", "capability sys_admin,", "/dev/vtpmx rw,", `"` + filepath.Join(dir, "tpm.dev.log") + `" w,`} {
		if !strings.Contains(container, "\n  "+rule+"\n") {
			t.Errorf("Container profile has no rule %q:\n%s", rule, container)
		}

		if strings.Contains(vm, rule) {
			t.Errorf("VM profile has rule %q:\n%s", rule, vm)
		}
	}

	if strings.Contains(container, "unix (") || !strings.Contains(vm, "\n  unix (") {
		t.Errorf("Only the VM profile should allow unix sockets:\ncontainer:\n%s\nvm:\n%s", container, vm)
	}
}

func TestSwtpmProfileLibraryPaths(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, "swtpm"), nil, 0700)
	if err != nil {
		t.Fatal(err)
	}

	statePath := filepath.Join(dir, "state")
	err = os.Mkdir(statePath, 0700)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", dir)
	t.Setenv("LD_LIBRARY_PATH", `/opt/lib{1}:relative::/:/usr/lib`)

	profile, err := swtpmProfile("lxd_test", false, statePath, "")
	if err != nil {
		t.Fatal(err)
	}

	for _, rule := range []string{`"/opt/lib\{1\}/**" mr,`, `"/usr/lib/**" mr,`} {
		if !strings.Contains(profile, "\n  "+rule+"\n") {
			t.Errorf("Profile has no rule %q:\n%s", rule, profile)
		}
	}

	// Neither the root directory nor relative or empty entries may render a rule.
	for _, rule := range []string{`"//**"`, `"/**"`, `"relative`} {
		if strings.Contains(profile, "\n  "+rule) {
			t.Errorf("Profile has unexpected rule %q:\n%s", rule, profile)
		}
	}
}

func TestSwtpmProfileID(t *testing.T) {
	ids := map[string]bool{}
	for _, tc := range []struct {
		project string
		inst    string
		dev     string
	}{
		{"default", "c1", "tpm"},
		{"default", "c1", "tpm/a"},
		{"default", "c1", "tpm-a"},
		{"default", "p", "c1_tpm"},
		{"p", "c1", "tpm"},
		{"default", "c1", "a..b"},
		{"default", "c1", "a:b"},
		{"p..q", "c1", "tpm"},
	} {
		id := swtpmProfileID(swtpmTestInstance{project: tc.project, name: tc.inst}, swtpmTestDevice{name: tc.dev})
		if ids[id] {
			t.Errorf("Duplicate profile ID %q for %+v", id, tc)
		}

		ids[id] = true

		if strings.Contains(id, "..") || strings.ContainsAny(id, "/:") {
			t.Errorf("Profile ID %q for %+v is not usable as a profile file name", id, tc)
		}
	}
}
