package version

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/lxc/lxd/shared/osarch"
)

// UserAgent contains a string suitable as a user-agent
var UserAgent = getUserAgent()
var userAgentStorageBackends []string
var userAgentFeatures []string

func getUserAgent() string {
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

	// Initial version string
	agent := fmt.Sprintf("LXD %s", Version)

	// OS information
	agent = fmt.Sprintf("%s (%s)", agent, strings.Join(osTokens, "; "))

	// Storage information
	if len(userAgentStorageBackends) > 0 {
		agent = fmt.Sprintf("%s (%s)", agent, strings.Join(userAgentStorageBackends, "; "))
	}

	// Feature information
	if len(userAgentFeatures) > 0 {
		agent = fmt.Sprintf("%s (%s)", agent, strings.Join(userAgentFeatures, "; "))
	}

	return agent
}

// UserAgentStorageBackends updates the list of storage backends to include in the user-agent
func UserAgentStorageBackends(backends []string) {
	userAgentStorageBackends = backends
	UserAgent = getUserAgent()
}

// UserAgentFeatures updates the list of advertised features
func UserAgentFeatures(features []string) {
	userAgentFeatures = features
	UserAgent = getUserAgent()
}
