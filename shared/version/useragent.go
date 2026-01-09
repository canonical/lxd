package version

import (
	"errors"
	"runtime"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/canonical/lxd/shared/osarch"
)

// UserAgent contains a string suitable as a user-agent.
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

	versionInfo := getPlatformVersionStrings()
	osTokens := make([]string, 0, 2+len(versionInfo))
	osTokens = append(osTokens, cases.Title(language.English).String(runtime.GOOS), arch)
	osTokens = append(osTokens, versionInfo...)
	// Initial version string
	agent := "LXD " + Version
	if IsLTSVersion {
		agent = agent + " LTS"
	}

	// OS information
	agent = agent + " (" + strings.Join(osTokens, "; ") + ")"

	// Storage information
	if len(userAgentStorageBackends) > 0 {
		agent = agent + " (" + strings.Join(userAgentStorageBackends, "; ") + ")"
	}

	// Feature information
	if len(userAgentFeatures) > 0 {
		agent = agent + " (" + strings.Join(userAgentFeatures, "; ") + ")"
	}

	return agent
}

// UserAgentStorageBackends updates the list of storage backends to include in the user-agent.
func UserAgentStorageBackends(backends []string) {
	userAgentStorageBackends = backends
	UserAgent = getUserAgent()
}

// UserAgentFeatures updates the list of advertised features.
func UserAgentFeatures(features []string) error {
	hasWhiteSpace := func(s string) bool {
		return strings.ContainsFunc(s, unicode.IsSpace)
	}

	if slices.ContainsFunc(features, hasWhiteSpace) {
		return errors.New("User agent features may not contain whitespace")
	}

	userAgentFeatures = features
	UserAgent = getUserAgent()
	return nil
}
