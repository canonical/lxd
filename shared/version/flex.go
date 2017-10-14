package version

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/lxc/lxd/shared/osarch"
)

// Version contains the LXD version number
var Version = "2.0.11"

// UserAgent contains a string suitable as a user-agent
var UserAgent = getUserAgent()

// APIVersion contains the API base version. Only bumped for backward incompatible changes.
var APIVersion = "1.0"

// APIExtensions is the list of all API extensions in the order they were added.
//
// The following kind of changes come with a new extensions:
//
// - New configuration key
// - New valid values for a configuration key
// - New REST API endpoint
// - New argument inside an existing REST API call
// - New HTTPs authentication mechanisms or protocols
//
// This list is used mainly by the LXD server code, but it's in the shared
// package as well for reference.
var APIExtensions = []string{
	"id_map",
	"id_map_base",
	"resource_limits",
}

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
