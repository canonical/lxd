package apparmor

import (
	"strings"
	"testing"
)

func TestFSToolProfile(t *testing.T) {
	profile, err := fsToolProfile("lxd_test", []string{"/usr/sbin/mke2fs"}, []string{`/dev/v[1]{a,b}`, "/dev/dm-1"}, []string{"/mnt/v1"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, rule := range []string{
		`"/usr/sbin/mke2fs" mixr,`,
		`"/dev/v\[1\]\{a,b\}" rwk,`,
		`"/dev/dm-1" rwk,`,
		`"/mnt/v1" r,`,
		`"/mnt/v1/" r,`,
	} {
		if !strings.Contains(profile, "\n  "+rule+"\n") {
			t.Errorf("Profile has no rule %q:\n%s", rule, profile)
		}
	}

	// Nothing else under /dev or the host's data may be made accessible.
	for _, rule := range []string{"/dev/** ", "/etc/** ", "/** "} {
		if strings.Contains(profile, "\n  "+rule) {
			t.Errorf("Profile has unexpected rule %q:\n%s", rule, profile)
		}
	}
}
