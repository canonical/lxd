package version

import (
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/canonical/lxd/shared/osarch"
)

// UserAgent contains a string suitable as a user-agent.
var UserAgent = getUserAgent()
var userAgentStorageBackends []string
var userAgentFeatures []string

func getUserAgent() string {
	// Initial version string
	agent := "LXD " + Version
	if IsLTSVersion {
		agent = agent + " LTS"
	}

	// OS information
	agent = agent + " (" + strings.Join(GetOSTokens(), "; ") + ")"

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
func UserAgentFeatures(features []string) {
	userAgentFeatures = features
	UserAgent = getUserAgent()
}

// GetOSTokens gets a list of operating system details for use in a user agent header.
func GetOSTokens() []string {
	archID, err := osarch.ArchitectureId(runtime.GOARCH)
	if err != nil {
		panic(err)
	}

	arch, err := osarch.ArchitectureName(archID)
	if err != nil {
		panic(err)
	}

	osTokens := []string{cases.Title(language.English).String(runtime.GOOS), arch}
	osTokens = append(osTokens, getPlatformVersionStrings()...)
	return osTokens
}

// ClientUserAgent contains pertinent information about a client connecting to LXD.
type ClientUserAgent struct {
	Name         string
	Version      string
	IsLTS        bool
	OS           []string
	Capabilities []string
}

// String implements [fmt.Stringer] for [ClientUserAgent] and should be used as the User-Agent header in client requests.
func (c ClientUserAgent) String() string {
	var b strings.Builder
	b.WriteString(c.Name + "/" + c.Version + " (")
	b.WriteString("LTS(" + strconv.FormatBool(c.IsLTS) + ");")
	b.WriteString("OS(" + strings.Join(c.OS, ";") + ");")
	b.WriteString("capabilities(" + strings.Join(c.Capabilities, ";") + "))")
	return b.String()
}

// ParseClientUserAgent parses a client's user agent string to return a [ClientUserAgent].
func ParseClientUserAgent(userAgent string) (*ClientUserAgent, error) {
	r := regexp.MustCompile(`([^/]+)/([^\s]*)\s+\(LTS\((\w+)\);OS\((.+)\);capabilities\((.+)\)\)`)
	matches := r.FindAllStringSubmatch(userAgent, -1)
	if len(matches) != 1 {
		return nil, fmt.Errorf("Invalid client user agent string %q", userAgent)
	}

	if len(matches[0]) != 6 {
		return nil, fmt.Errorf("Invalid client user agent string %q", userAgent)
	}

	isLTS, err := strconv.ParseBool(matches[0][3])
	if err != nil {
		return nil, fmt.Errorf("Invalid client user agent string %q: %w", userAgent, err)
	}

	return &ClientUserAgent{
		Name:         matches[0][1],
		Version:      matches[0][2],
		IsLTS:        isLTS,
		OS:           strings.Split(matches[0][4], ";"),
		Capabilities: strings.Split(matches[0][5], ";"),
	}, nil
}
