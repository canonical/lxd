package request

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/canonical/lxd/shared"
)

// UserAgent represents a LXD user agent.
type UserAgent struct {
	Product  UserAgentProduct
	Host     UserAgentHost
	Storage  map[string]string
	Features []string
}

// UserAgentProduct contains information about the product (first part of user agent).
type UserAgentProduct struct {
	Name    string
	Version string
	LTS     bool
}

// UserAgentHost contains host information stored in the user agent.
type UserAgentHost struct {
	OS            string
	Arch          string
	KernelVersion string
	Distro        string
	DistroVersion string
}

// ParseUserAgent parses a user agent to return a [UserAgent].
func ParseUserAgent(userAgent string) (*UserAgent, error) {
	groups, err := splitUserAgent(userAgent)
	if err != nil {
		return nil, err
	}

	product, err := getProductInfo(groups[0])
	if err != nil {
		return nil, fmt.Errorf("Failed to get product information: %w", err)
	}

	host, err := getHostInfo(groups[1])
	if err != nil {
		return nil, fmt.Errorf("Failed to get host information: %w", err)
	}

	ua := &UserAgent{
		Product: *product,
		Host:    *host,
	}

	if len(groups) == 2 {
		return ua, nil
	}

	storage, isFeatureGroup, err := tryGetStorageInfo(groups[2])
	if err != nil {
		return nil, fmt.Errorf("Failed to get storage information: %w", err)
	}

	if storage != nil {
		ua.Storage = storage
	} else if isFeatureGroup {
		features, err := getFeatureInfo(groups[2])
		if err != nil {
			return nil, fmt.Errorf("Failed to get feature information: %w", err)
		}

		ua.Features = features
	}

	if len(groups) == 3 {
		return ua, nil
	}

	if storage == nil {
		return nil, errors.New("Feature group may not precede storage group")
	}

	features, err := getFeatureInfo(groups[3])
	if err != nil {
		return nil, fmt.Errorf("Failed to get feature information: %w", err)
	}

	ua.Features = features
	return ua, nil
}

// getProductInfo returns the product, product_version and product_lts based on the basic User-Agent information.
func getProductInfo(product string) (*UserAgentProduct, error) {
	fields := strings.Fields(product)
	if fields[0] != "LXD" {
		return nil, errors.New("Only LXD user agents are currently supported")
	}

	if len(fields) < 2 {
		return nil, errors.New("Product does not contain a version")
	}

	name := fields[0]
	version := fields[1]

	if len(fields) > 3 {
		return nil, errors.New(`Product must be of the form "LXD <version> [LTS]"`)
	}

	var lts bool
	if len(fields) == 3 {
		if fields[2] != "LTS" {
			return nil, errors.New(`Malformed product LTS field`)
		}

		lts = true
	}

	if !lts {
		// 4.0 and 5.0 didn't have the LTS flag in the User-Agent string.
		// So derive the LTS status from the major and minor version numbers and any patch level.
		lts = strings.HasPrefix(version, "4.0.") || strings.HasPrefix(version, "5.0.")
	}

	p := UserAgentProduct{
		Name:    name,
		Version: version,
		LTS:     lts,
	}

	return &p, nil
}

// getHostInfo returns the host_os, host_arch, host_kernel, host_distro and host_distro_version based on the User-Agent information.
func getHostInfo(hostGroup string) (h *UserAgentHost, err error) {
	// The host information contains: host_os, host_arch, host_kernel, host_distro, host_distro_version.
	// Among those, only the host_os and host_arch are mandatory.
	parts := shared.SplitNTrimSpace(hostGroup, "; ", 6, true)
	if len(parts) < 2 {
		return nil, errors.New("Host group must contain OS and architecture")
	}

	if len(parts) > 5 {
		return nil, errors.New("Host group cannot contain more than 5 elements")
	}

	// If not enough parts were found, fill in the missing parts with empty strings.
	if len(parts) < 5 {
		parts = append(parts, make([]string, 5-len(parts))...)
	}

	h = &UserAgentHost{
		OS:            parts[0],
		Arch:          parts[1],
		KernelVersion: parts[2],
		Distro:        parts[3],
		DistroVersion: parts[4],
	}

	return h, nil
}

// tryGetStorageInfo inspects the given group to return storage information. If the first element in the group does not
// contain any whitespace, a boolean is returned to indicate that this not a storage group and should be parsed as a
// feature group.
func tryGetStorageInfo(group string) (map[string]string, bool, error) {
	// The storage information is optional and may not be present in the User-Agent string.
	if group == "" {
		return nil, false, nil
	}

	parts := shared.SplitNTrimSpace(group, "; ", -1, true)
	drivers := make(map[string]string, len(parts))
	for i, part := range parts {
		name, version, found := strings.Cut(part, " ")
		if !found {
			if i > 0 {
				return nil, false, errors.New("Cannot mix storage drivers and features")
			}

			return nil, true, nil
		}

		_, ok := drivers[name]
		if ok {
			return nil, false, fmt.Errorf("Repeated driver %q found in storage details", name)
		}

		// We cut the string on the first space, trim any remaining spaces before the version.
		drivers[name] = strings.TrimLeftFunc(version, unicode.IsSpace)
	}

	return drivers, false, nil
}

// extractParenthesesGroups returns groups of the User-Agent string.
// Only consider the outermost parentheses groups and strip the outermost
// parentheses and any leading or trailing spaces like this:
//
// Input: "LXD 5.20 (Linux; x86_64; 5.15.0; Ubuntu; 22.04) (ceph 17.2.6; zfs 2.1.5-1ubuntu6) (cluster)"
// Output: []string{"Linux; x86_64; 5.15.0; Ubuntu; 22.04", "ceph 17.2.6; zfs 2.1.5-1ubuntu6", "cluster"}
//
// Returns an error if the parentheses are unbalanced or if no groups are found.
func extractParenthesesGroups(ua string) (groups []string, err error) {
	groups = make([]string, 0, 3)
	start := 0
	depth := 0
	for i, r := range ua {
		switch r {
		case '(':
			if depth == 0 {
				start = i
			}

			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, errors.New("User agent contains unbalanced parentheses")
			} else if depth == 0 && start < i {
				groups = append(groups, ua[start+1:i])
			}
		}
	}

	if depth != 0 {
		return nil, errors.New("User agent contains unbalanced parentheses")
	}

	return groups, nil
}

// splitUserAgent splits the User-Agent string into product, host, storage, and feature groups.
func splitUserAgent(ua string) ([]string, error) {
	division := strings.Index(ua, " (")
	if division == -1 || ua[:division] == "" {
		return nil, errors.New(`User agent string must start with "<product>" and contain host information in parentheses`)
	}

	// Product info, host info, storage info, feature info.
	groups := make([]string, 0, 4)
	product := ua[:division]

	// Product info and host info are mandatory.
	groups = append(groups, product)

	// The host information is mandatory and always the first group.
	infoGroups, err := extractParenthesesGroups(ua[division+1:])
	if err != nil {
		return nil, err
	}

	// We don't need to check if the host group is present (`len(infoGroups) > 1`) because we know that there is at least
	// one opening parenthesis (see "division" above) and that all parentheses are balanced (see "extractParenthesisGroups").
	if len(infoGroups) > 3 {
		return nil, errors.New("User agent may contain at most two extra optional groups containing storage driver and feature details")
	}

	return append(groups, infoGroups...), nil
}

// getFeatureInfo returns a list of features from a group and enforces that no feature contains whitespace.
func getFeatureInfo(featureGroup string) ([]string, error) {
	hasWhitespace := func(s string) bool {
		return strings.ContainsFunc(s, unicode.IsSpace)
	}

	features := shared.SplitNTrimSpace(featureGroup, "; ", -1, true)
	if slices.ContainsFunc(features, hasWhitespace) {
		return nil, errors.New("Features may not contain whitespace")
	}

	return features, nil
}
