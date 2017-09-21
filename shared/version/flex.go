package version

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/lxc/lxd/shared/osarch"
)

// Version contains the LXD version number
var Version = "2.18"

// UserAgent contains a string suitable as a user-agent
var UserAgent = getUserAgent()

// APIVersion contains the API base version. Only bumped for backward incompatible changes.
var APIVersion = "1.0"

func getUserAgent() string {
	archID, err := osarch.ArchitectureId(runtime.GOARCH)
	if err != nil {
		panic(err)
	}
	arch, err := osarch.ArchitectureName(archID)
	if err != nil {
		panic(err)
	}

	tokens := []string{strings.Title(runtime.GOOS), arch}
	tokens = append(tokens, getPlatformVersionStrings()...)
	return fmt.Sprintf("LXD %s (%s)", Version, strings.Join(tokens, "; "))
}
