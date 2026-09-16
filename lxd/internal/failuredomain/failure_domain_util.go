package failuredomain

import (
	"fmt"
	"slices"

	"github.com/canonical/lxd/lxd/internal/datastructure/iterutil"
	"github.com/canonical/lxd/lxd/internal/datastructure/sets"
	. "github.com/canonical/lxd/lxd/internal/func/predicate"
	"github.com/canonical/lxd/shared"
)

// ParseFailureDomains splits a cluster.failure_domains config value into its individual domain
// names, trimming whitespace around each and dropping empty entries.
func ParseFailureDomains(raw string) []string {
	return shared.SplitNTrimSpace(raw, ",", -1, true)
}

// ResolveCalculatedFailureDomains computes a placement group's calculated failure-domain set:
// known, narrowed by the cluster-wide override if one is set. An empty override means "inherit
// known unchanged" — known is never treated as an unrestricted wildcard when empty, since it is
// the base universe the override can only narrow.
func ResolveCalculatedFailureDomains(known sets.Set[string], override []string) sets.Set[string] {
	if len(override) == 0 {
		return known
	}

	return known.Intersect(sets.New(override...))
}

// ValidateFailureDomains returns an error identifying every entry in names not present in known,
// not just the first one encountered — so a caller correcting a typo'd list doesn't have to fix
// and resubmit it one name at a time. An empty names is always valid — it means "no restriction,"
// not "restrict to nothing" — so callers should call this only when a group or cluster override is
// actually being set.
func ValidateFailureDomains(known sets.Set[string], names []string) error {
	unknown := slices.Collect(iterutil.Filter(slices.Values(names), Not(known.Contains)))
	if len(unknown) > 0 {
		return fmt.Errorf("%q are not known failure domains", unknown)
	}

	return nil
}
