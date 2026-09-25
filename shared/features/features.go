// Package features provides process-local feature previews.
// Feature previews expose unsupported and unfinished functionality.
// They MUST NOT be enabled in production environments.
//
// The IsEnabled function is used throughout the source code to check whether
// the feature is enabled and activates related code:
//
//	if features.IsEnabled("featureX") {
//		...
//	}
//
// Applications load enabled previews from the environment during startup.
package features

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Feature identifies an unsupported, work-in-progress feature.
type Feature string

// envVar is the environment variable which holds the active features of LXD.
const envVar = "LXD_FEATURES"

// enabledFeaturePrevs holds the status of supported feature previews.
var enabledFeaturePrevs = map[Feature]struct{}{}

// FailureDomainPlacement previews failure-domain-aware placement. Enabling it (via
// LXD_FEATURES=failure_domain_placement) turns on:
//   - the placement-group scope=failure-domain policy
//   - the cluster-wide instances.placement.failure_domain.exclude list
//   - the project-wide limits.placement.hosts.max limit on how many distinct hosts an instance's
//     placement may span
const FailureDomainPlacement Feature = "failure_domain_placement"

func init() {
	err := loadFromEnv(envVar)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// loadFromEnv replaces the set of enabled feature previews from the environment.
func loadFromEnv(envVar string) error {
	// Define a regex accepting feature names as: test_feature_a,test_feature_b
	var namePattern = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

	// Load the environment variable
	value := os.Getenv(envVar)

	// Define a temp variable to save the parsed arguments
	next := make(map[Feature]struct{})

	// If the environment variable is not empty, parse the feature names
	if value != "" {
		for name := range strings.SplitSeq(value, ",") {
			// Check if feature name pattern in acceptable form
			if !namePattern.MatchString(name) {
				return fmt.Errorf("Invalid feature preview %q", name)
			}

			// Save the enabled feature preview
			next[Feature(name)] = struct{}{}
		}
	}

	// Enable features after all checks have passed
	enabledFeaturePrevs = next

	return nil
}

// IsEnabled reports whether the feature preview is enabled.
func IsEnabled(feature Feature) bool {
	_, found := enabledFeaturePrevs[feature]

	return found
}
