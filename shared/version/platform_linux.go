// +build linux

package version

import (
	"strings"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/osarch"
)

func getPlatformVersionStrings() []string {
	versions := []string{}

	// add kernel version
	uname, err := shared.Uname()
	if err == nil {
		versions = append(versions, strings.Split(uname.Release, "-")[0])
	}

	// add distribution info
	lsbRelease, err := osarch.GetLSBRelease()
	if err == nil {
		for _, key := range []string{"NAME", "VERSION_ID"} {
			value, ok := lsbRelease[key]
			if ok {
				versions = append(versions, value)
			}
		}
	}
	return versions
}
