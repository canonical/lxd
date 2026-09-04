// Package features provides process-local feature previews.
// Feature previews expose unsupported and unfinished functionality. They MUST NOT
// be enabled in production environments.
//
// Applications load enabled previews from the environment during startup:
//
//	err := features.LoadFromEnv(features.EnvVar)
//
// A feature declares a typed name and checks it before exposing preview code:
//
//	const experimentalFeature features.Feature = "experimental_feature"
//	if features.IsEnabled(experimentalFeature) {
//		registerExperimentalFeature()
//	}
//
// Previews are disabled by default. Add a feature constant only with a real consumer,
// and remove its preview check when the feature is ready for general use.
package features

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
)

// Feature identifies an unsupported, work-in-progress feature.
type Feature string

// MicroVM gates the microvm instance type, which boots a container image under a
// lightweight hypervisor.
const MicroVM Feature = "microvm"

// EnvVar is the environment variable which holds the active features of LXD.
const EnvVar = "LXD_FEATURES"

var (
	mu      sync.RWMutex
	enabled = map[Feature]struct{}{}
)

// LoadFromEnv replaces the set of enabled feature previews from the environment.
func LoadFromEnv(envVar string) error {
	var namePattern = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

	value := os.Getenv(envVar)

	next := make(map[Feature]struct{})
	if value != "" {
		for _, name := range strings.Split(value, ",") {
			if !namePattern.MatchString(name) {
				return fmt.Errorf("Invalid feature preview %q", name)
			}

			next[Feature(name)] = struct{}{}
		}
	}

	mu.Lock()
	enabled = next
	mu.Unlock()

	return nil
}

// IsEnabled reports whether the feature preview is enabled.
func IsEnabled(feature Feature) bool {
	mu.RLock()
	_, found := enabled[feature]
	mu.RUnlock()

	return found
}
