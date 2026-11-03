package apparmor

import (
	"strings"
	"testing"

	"github.com/canonical/lxd/lxd/sys"
)

func TestRsyncProfileEscapesPaths(t *testing.T) {
	profile, err := rsyncProfile(&sys.OS{ExecPath: "/usr/bin/lxd"}, "lxd_test", `/src/v[1]{a,b}*?`, `/dst/v"1",x`)
	if err != nil {
		t.Fatal(err)
	}

	for _, rule := range []string{
		`"/src/v\[1\]\{a,b\}\*\?/**" r,`,
		`"/src/v\[1\]\{a,b\}\*\?" r,`,
		`"/dst/v\"1\",x/**" rwkl,`,
		`"/dst/v\"1\",x" rwkl,`,
		`"/dst/v\"1\",x/" rwkl,`,
	} {
		if !strings.Contains(profile, "\n  "+rule+"\n") {
			t.Errorf("Profile has no rule %q:\n%s", rule, profile)
		}
	}
}

func TestRsyncProfileEscapesExecPath(t *testing.T) {
	profile, err := rsyncProfile(&sys.OS{ExecPath: `/opt/my lxd[1]/lxd`}, "lxd_test", "/src", "/dst")
	if err != nil {
		t.Fatal(err)
	}

	rule := `"/opt/my lxd\[1\]/lxd" mixr,`
	if !strings.Contains(profile, "\n  "+rule+"\n") {
		t.Errorf("Profile has no rule %q:\n%s", rule, profile)
	}
}
