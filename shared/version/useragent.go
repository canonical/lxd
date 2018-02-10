package version

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/lxc/lxd/shared/osarch"
)

// UserAgent contains a string suitable as a user-agent
var UserAgent = getUserAgent(nil)

func getUserAgent(storageTokens []string) string {
	archID, err := osarch.ArchitectureId(runtime.GOARCH)
	if err != nil {
		panic(err)
	}

	arch, err := osarch.ArchitectureName(archID)
	if err != nil {
		panic(err)
	}

	osTokens := []string{strings.Title(runtime.GOOS), arch}
	osTokens = append(osTokens, getPlatformVersionStrings()...)

	agent := fmt.Sprintf("LXD %s", Version)
	if len(osTokens) > 0 {
		agent = fmt.Sprintf("%s (%s)", agent, strings.Join(osTokens, "; "))
	}

	if len(storageTokens) > 0 {
		agent = fmt.Sprintf("%s (%s)", agent, strings.Join(storageTokens, "; "))
	}

	return agent
}

// UserAgentStorageBackends updates the list of storage backends to include in the user-agent
func UserAgentStorageBackends(backends []string) {
	UserAgent = getUserAgent(backends)
}
