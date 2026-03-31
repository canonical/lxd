package tokencache

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

func ptr[T any](v T) *T {
	return &v
}

func tokenCacheReplaceFunc(t *testing.T, want, replaceWith *string, err error) func(*string) (*string, error) {
	t.Helper()
	return func(got *string) (*string, error) {
		t.Helper()
		switch {
		case want == nil && got != nil:
			t.Errorf("replace func got unexpected source value %q, expected nil", *got)
		case want != nil && got == nil:
			t.Errorf("replace func got unexpected nil source value, expected %q", *want)
		case want != nil && got != nil && *want != *got:
			t.Errorf("replace func got unexpected source value %q, expected %q", *got, *want)
		}

		return replaceWith, err
	}
}

func requireTokenCacheToEqual(t *testing.T, tc *TokenCache[string], want ...string) {
	t.Helper()
	var got []string
	for key, ptr := range tc.Range {
		if ptr == nil {
			t.Fatalf("cache reported nil value for key %q", key)
		} else {
			got = append(got, fmt.Sprintf("%s=%s", key, *ptr))
		}
	}
	slices.Sort(got) // sort to get stable output during tests
	if !slices.Equal(got, want) {
		t.Fatalf("invalid cache state\n  want: %v\n  got: %v", want, got)
	}
}

func requireTokenCacheLoadToEqual(t *testing.T, tc *TokenCache[string], key string, want *string) {
	t.Helper()
	got := tc.Load(key)
	switch {
	case want == nil && got != nil:
		t.Fatalf("unexpected cache load value %q, expected nil", *got)
	case want != nil && got == nil:
		t.Fatalf("unexpected cache load nil value, expected %q", *want)
	case want != nil && got != nil && *want != *got:
		t.Fatalf("unexpected cache load value %q, expected %q", *got, *want)
	}
}

func requireTokenCacheReplaceToEqual(t *testing.T, tc *TokenCache[string], key string, replaceFunc func(*string) (*string, error), wantValue *string, wantErr error) {
	t.Helper()
	gotValue, gotErr := tc.Replace(key, replaceFunc)
	switch {
	case wantErr == nil && gotErr != nil:
		t.Fatalf("unexpected cache replace error %q, expected nil", gotErr.Error())
	case wantErr != nil && gotErr == nil:
		t.Fatalf("unexpected cache replace nil error, expected %q", wantErr.Error())
	case wantErr != nil && gotErr != nil && wantErr.Error() != gotErr.Error():
		t.Fatalf("unexpected cache replace error %q, expected %q", gotErr.Error(), wantErr.Error())
	}

	switch {
	case wantValue == nil && gotValue != nil:
		t.Fatalf("unexpected cache replace value %q, expected nil", *gotValue)
	case wantValue != nil && gotValue == nil:
		t.Fatalf("unexpected cache replace nil value, expected %q", *wantValue)
	case wantValue != nil && gotValue != nil && *wantValue != *gotValue:
		t.Fatalf("unexpected cache replace value %q, expected %q", *gotValue, *wantValue)
	}
}

func TestTokenCache_SingleKey(t *testing.T) {
	tc := &TokenCache[string]{}
	requireTokenCacheToEqual(t, tc)
	requireTokenCacheLoadToEqual(t, tc, "000", nil)

	// add key "000" with value "AAA"
	fromNilToAAA := tokenCacheReplaceFunc(t, nil, ptr("AAA"), nil)
	requireTokenCacheReplaceToEqual(t, tc, "000", fromNilToAAA, ptr("AAA"), nil)
	requireTokenCacheToEqual(t, tc, "000=AAA")
	requireTokenCacheLoadToEqual(t, tc, "000", ptr("AAA"))

	// fail to replace key "000" with value "BBB"
	fromAAAToBBBFailed := tokenCacheReplaceFunc(t, ptr("AAA"), ptr("BBB"), errors.New("replace failure"))
	requireTokenCacheReplaceToEqual(t, tc, "000", fromAAAToBBBFailed, ptr("BBB"), errors.New("replace failure"))
	requireTokenCacheToEqual(t, tc, "000=AAA") // replace func returned an error - value should not change
	requireTokenCacheLoadToEqual(t, tc, "000", ptr("AAA"))

	// replace key "000" with value "CCC"
	fromAAAToCCC := tokenCacheReplaceFunc(t, ptr("AAA"), ptr("CCC"), nil)
	requireTokenCacheReplaceToEqual(t, tc, "000", fromAAAToCCC, ptr("CCC"), nil)
	requireTokenCacheToEqual(t, tc, "000=CCC")
	requireTokenCacheLoadToEqual(t, tc, "000", ptr("CCC"))

	// remove key "000"
	fromCCCToNil := tokenCacheReplaceFunc(t, ptr("CCC"), nil, nil)
	requireTokenCacheReplaceToEqual(t, tc, "000", fromCCCToNil, nil, nil)
	requireTokenCacheToEqual(t, tc)
	requireTokenCacheLoadToEqual(t, tc, "000", nil)
}

func TestTokenCache_ConfusionWithMultipleKeys(t *testing.T) {
	tc := &TokenCache[string]{}
	requireTokenCacheToEqual(t, tc)
	requireTokenCacheLoadToEqual(t, tc, "000", nil)
	requireTokenCacheLoadToEqual(t, tc, "111", nil)

	// add key "000" with value "AAA"
	fromNilToAAA := tokenCacheReplaceFunc(t, nil, ptr("AAA"), nil)
	requireTokenCacheReplaceToEqual(t, tc, "000", fromNilToAAA, ptr("AAA"), nil)
	requireTokenCacheToEqual(t, tc, "000=AAA")
	requireTokenCacheLoadToEqual(t, tc, "000", ptr("AAA"))
	requireTokenCacheLoadToEqual(t, tc, "111", nil)

	// add key "111" with value "BBB"
	fromNilToBBB := tokenCacheReplaceFunc(t, nil, ptr("BBB"), nil)
	requireTokenCacheReplaceToEqual(t, tc, "111", fromNilToBBB, ptr("BBB"), nil)
	requireTokenCacheToEqual(t, tc, "000=AAA", "111=BBB")
	requireTokenCacheLoadToEqual(t, tc, "000", ptr("AAA"))
	requireTokenCacheLoadToEqual(t, tc, "111", ptr("BBB"))

	// replace key "000" with value "BBB"
	fromAAAToBBB := tokenCacheReplaceFunc(t, ptr("AAA"), ptr("BBB"), nil)
	requireTokenCacheReplaceToEqual(t, tc, "000", fromAAAToBBB, ptr("BBB"), nil)
	requireTokenCacheToEqual(t, tc, "000=BBB", "111=BBB")
	requireTokenCacheLoadToEqual(t, tc, "000", ptr("BBB"))
	requireTokenCacheLoadToEqual(t, tc, "111", ptr("BBB"))

	// replace key "111" with value "CCC"
	fromBBBToCCC := tokenCacheReplaceFunc(t, ptr("BBB"), ptr("CCC"), nil)
	requireTokenCacheReplaceToEqual(t, tc, "111", fromBBBToCCC, ptr("CCC"), nil)
	requireTokenCacheToEqual(t, tc, "000=BBB", "111=CCC")
	requireTokenCacheLoadToEqual(t, tc, "000", ptr("BBB"))
	requireTokenCacheLoadToEqual(t, tc, "111", ptr("CCC"))

	// remove key "000"
	fromBBBToNil := tokenCacheReplaceFunc(t, ptr("BBB"), nil, nil)
	requireTokenCacheReplaceToEqual(t, tc, "000", fromBBBToNil, nil, nil)
	requireTokenCacheToEqual(t, tc, "111=CCC")
	requireTokenCacheLoadToEqual(t, tc, "000", nil)
	requireTokenCacheLoadToEqual(t, tc, "111", ptr("CCC"))
}
