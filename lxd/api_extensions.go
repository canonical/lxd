package main

import (
	"slices"

	"github.com/canonical/lxd/lxd/internal/datastructure/iterutil"
	"github.com/canonical/lxd/lxd/internal/datastructure/sets"
	"github.com/canonical/lxd/lxd/internal/func/predicate"
	"github.com/canonical/lxd/shared/features"
	"github.com/canonical/lxd/shared/version"
)

// gatedAPIExtensions are always present in version.APIExtensions (this server build genuinely
// supports them), but only advertised to clients while their gating feature preview is enabled --
// hidden otherwise, so a client has no way to discover, and therefore no reason to attempt using,
// functionality this build isn't ready to expose to users yet.
//
// Empty at this point in history -- this commit adds only the mechanism, not any specific gated
// extension. Each one registers itself here (via gatedAPIExtensions.Add) in the commit that
// introduces it.
var gatedAPIExtensions = sets.New[string]()

// visibleAPIExtensions returns the API extensions to advertise to clients: normally all of
// version.APIExtensions, but with gatedAPIExtensions stripped out while
// features.FailureDomainPlacement is disabled.
func visibleAPIExtensions() []string {
	if features.IsEnabled(features.FailureDomainPlacement) {
		return version.APIExtensions
	}

	visible := make([]string, 0, len(version.APIExtensions))
	return slices.AppendSeq(visible, iterutil.Filter(slices.Values(version.APIExtensions), predicate.Not(gatedAPIExtensions.Contains)))
}
