package version

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/lxc/lxd/shared/osarch"
)

// UserAgent contains a string suitable as a user-agent
var UserAgent = getUserAgent()

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
