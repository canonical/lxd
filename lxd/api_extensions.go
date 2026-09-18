package main

import (
	"slices"

	"github.com/canonical/lxd/lxd/internal/func/iterutil"
	"github.com/canonical/lxd/lxd/internal/func/predicate"
	"github.com/canonical/lxd/shared/features"
)

// Hides API extensions that are gated behind feature previews.
var gatedAPIExtensions = map[string]features.Feature{}

// visibleAPIExtensions returns extensions minus whichever gatedAPIExtensions entries have their
// own gating feature currently disabled.
func visibleAPIExtensions(extensions []string) []string {
	isGated := func(ext string) bool {
		feature, ok := gatedAPIExtensions[ext]
		return ok && !features.IsEnabled(feature)
	}

	visible := make([]string, 0, len(extensions))
	return slices.AppendSeq(visible, iterutil.Filter(slices.Values(extensions), predicate.Not(isGated)))
}
