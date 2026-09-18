package failuredomain

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"

	"github.com/canonical/lxd/lxd/internal/datastructure/sets"
)

// domainPool is deliberately small so generated subsets overlap often — the point of these
// properties is to exercise real narrowing/intersection behavior, not near-certainly-disjoint
// random sets.
var domainPool = []string{"fd1", "fd2", "fd3", "fd4", "fd5"}

// genDomainSubset draws a random subset of pool, preserving pool's order (and therefore
// distinctness), by independently flipping a coin for each element.
func genDomainSubset(t *rapid.T, pool []string, label string) []string {
	var subset []string
	for _, name := range pool {
		if rapid.Bool().Draw(t, label+"-"+name) {
			subset = append(subset, name)
		}
	}

	return subset
}

// TestResolveCalculatedFailureDomainsNarrowingProperty asserts the core contract: the result is
// always a subset of known, and additionally a subset of override whenever it's non-empty — the
// override can never widen what known already allowed.
func TestResolveCalculatedFailureDomainsNarrowingProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		known := genDomainSubset(t, domainPool, "known")
		override := genDomainSubset(t, domainPool, "override")

		calculated := ResolveCalculatedFailureDomains(sets.New(known...), override)

		for name := range calculated {
			if !slices.Contains(known, name) {
				t.Fatalf("calculated domain %q is not in known %v", name, known)
			}

			if len(override) > 0 && !slices.Contains(override, name) {
				t.Fatalf("calculated domain %q is not in the non-empty override %v", name, override)
			}
		}
	})
}

// TestResolveCalculatedFailureDomainsIdentityProperty asserts that with the override empty, the
// result is exactly known (as a set) — an absent override must never narrow anything on its own.
func TestResolveCalculatedFailureDomainsIdentityProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		known := genDomainSubset(t, domainPool, "known")

		calculated := ResolveCalculatedFailureDomains(sets.New(known...), nil)

		assert.ElementsMatch(t, known, calculated.Slice())
	})
}

// TestValidateFailureDomainsProperty asserts the fail-fast contract: any name not present in known
// is always rejected, every name present in known is always accepted, and an empty names is
// always valid regardless of known (it means "no restriction," not "restrict to nothing").
func TestValidateFailureDomainsProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		known := genDomainSubset(t, domainPool, "known")
		names := genDomainSubset(t, domainPool, "names")

		err := ValidateFailureDomains(sets.New(known...), names)

		allKnown := true
		for _, name := range names {
			if !slices.Contains(known, name) {
				allKnown = false
				break
			}
		}

		if allKnown {
			assert.NoError(t, err)
		} else {
			assert.Error(t, err)
		}
	})
}

func TestValidateFailureDomainsEmptyNamesAlwaysValid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		known := genDomainSubset(t, domainPool, "known")

		assert.NoError(t, ValidateFailureDomains(sets.New(known...), nil))
	})
}

// TestValidateFailureDomainsReportsAllInvalidNames asserts that every unknown name is identified
// in the returned error, not just the first one encountered — so a caller correcting a typo'd list
// sees every problem at once rather than fixing and resubmitting one name at a time.
func TestValidateFailureDomainsReportsAllInvalidNames(t *testing.T) {
	err := ValidateFailureDomains(sets.New("fd1", "fd2"), []string{"fd1", "bogus1", "fd2", "bogus2"})
	if !assert.Error(t, err) {
		return
	}

	assert.Contains(t, err.Error(), "bogus1")
	assert.Contains(t, err.Error(), "bogus2")
	assert.NotContains(t, err.Error(), "fd1", "a known name should never be reported as invalid")
	assert.NotContains(t, err.Error(), "fd2", "a known name should never be reported as invalid")
}

// TestResolveCalculatedFailureDomainsNilAndEmptyInputs explicitly covers nil vs. empty
// ([]string{}) override inputs, and a nil vs. empty known Set — distinct Go values that rapid's
// generators (which only ever produce nil for "no elements", via genDomainSubset's
// unappended-to var slice) never exercise on their own.
func TestResolveCalculatedFailureDomainsNilAndEmptyInputs(t *testing.T) {
	for _, known := range []sets.Set[string]{nil, sets.New[string]()} {
		for _, override := range [][]string{nil, {}} {
			assert.Empty(t, ResolveCalculatedFailureDomains(known, override))
		}

		// A non-empty override against a nil/empty known can never widen the empty result.
		assert.Empty(t, ResolveCalculatedFailureDomains(known, []string{"fd1"}))
	}

	// A nil/empty override against a non-empty known leaves known untouched.
	known := sets.New("fd1", "fd2")
	for _, override := range [][]string{nil, {}} {
		assert.ElementsMatch(t, []string{"fd1", "fd2"}, ResolveCalculatedFailureDomains(known, override).Slice())
	}
}

// TestValidateFailureDomainsNilAndEmptyInputs explicitly covers nil vs. empty ([]string{}) names,
// and a nil vs. empty known Set.
func TestValidateFailureDomainsNilAndEmptyInputs(t *testing.T) {
	for _, names := range [][]string{nil, {}} {
		for _, known := range []sets.Set[string]{nil, sets.New[string]()} {
			assert.NoError(t, ValidateFailureDomains(known, names), "empty names is always valid, even against nil/empty known")
		}
	}

	// A non-empty names against a nil/empty known is always rejected — there's nothing to match.
	for _, known := range []sets.Set[string]{nil, sets.New[string]()} {
		assert.Error(t, ValidateFailureDomains(known, []string{"fd1"}))
	}
}
