package apparmor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNBDServerProfile(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	device := filepath.Join(dir, "device")
	err = os.WriteFile(device, nil, 0600)
	if err != nil {
		t.Fatal(err)
	}

	deviceLink := filepath.Join(dir, "device-link")
	err = os.Symlink(device, deviceLink)
	if err != nil {
		t.Fatal(err)
	}

	image := filepath.Join(dir, `snap{1,2}?[a]^"@`)
	err = os.WriteFile(image, nil, 0600)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("LD_LIBRARY_PATH", ":"+dir+":relative")

	profile, err := nbdServerProfile("lxd_test", "sh", filepath.Join(dir, "nbd.sock"), []string{image}, []string{deviceLink})
	if err != nil {
		t.Fatal(err)
	}

	// The paths are dereferenced and the characters that the parser interprets are escaped.
	for _, rule := range []string{
		`"` + filepath.Join(dir, "nbd.sock") + `" rw,`,
		`"` + filepath.Join(dir, `snap\{1,2\}\?\[a\]^\"@`) + `" rk,`,
		`"` + device + `" rwk,`,
		`"` + dir + `/**" mr,`,
	} {
		if !strings.Contains(profile, "\n  "+rule+"\n") {
			t.Errorf("Profile has no rule %q:\n%s", rule, profile)
		}
	}

	// An empty or relative LD_LIBRARY_PATH entry adds no rule.
	for _, rule := range []string{`"/**" mr,`, `"relative/**" mr,`, deviceLink} {
		if strings.Contains(profile, rule) {
			t.Errorf("Profile has rule %q:\n%s", rule, profile)
		}
	}
}
