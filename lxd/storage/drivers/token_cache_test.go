package drivers

import (
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestKeyedMutex_SerialExecution(t *testing.T) {
	const iterations = 5
	km := &keyedMutex{}
	output := ""

	subWriteRoutine := func(wg *sync.WaitGroup, val string) {
		defer wg.Done()
		km.Lock("k")
		defer km.Unlock("k")
		output += val
	}
	write := func() {
		wg := &sync.WaitGroup{}
		defer wg.Wait()
		km.Lock("k")
		defer km.Unlock("k")

		wg.Add(1)
		go subWriteRoutine(wg, "A")

		output += "0"
	}
	for range iterations {
		write()
	}

	if want := strings.Repeat("0A", iterations); want != output {
		t.Fatalf("invalid output (want %q, got %q)", want, output)
	}
}

func TestKeyedMutex_ResourceCleanup(t *testing.T) {
	km := &keyedMutex{}

	km.Lock("a")
	if want, got := 1, len(km.locks); want != got {
		t.Fatalf("expected %d key(s), got %d", want, got)
	}

	km.Lock("b")
	if want, got := 2, len(km.locks); want != got {
		t.Fatalf("expected %d key(s), got %d", want, got)
	}

	km.Unlock("a")
	if want, got := 1, len(km.locks); want != got {
		t.Fatalf("expected %d key(s), got %d", want, got)
	}

	km.Unlock("b")
	if want, got := 0, len(km.locks); want != got {
		t.Fatalf("expected %d key(s), got %d", want, got)
	}
}

func TestKeyedMutex_Race_SingleKey(t *testing.T) {
	const goroutines = 50
	const iterations = 1000
	km := &keyedMutex{}
	output := "" // shared variable to detect potential races

	stressRoutine := func(wg *sync.WaitGroup) {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			km.Lock("k")
			output += "x" //nolint:perfsprint // This is race test - concat-loop is intended.
			runtime.Gosched()
			km.Unlock("k")
		}
	}

	wg := &sync.WaitGroup{}
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go stressRoutine(wg)
	}
	wg.Wait()

	if len(km.locks) != 0 {
		t.Fatalf("expected no remaining locks")
	}
	if want := strings.Repeat("x", goroutines*iterations); want != output {
		t.Fatalf("invalid output (want len %d, got len %d)", len(want), len(output))
	}
}

func TestKeyedMutex_Race_MultipleKeys(t *testing.T) {
	const goroutines = 50
	const iterations = 1000
	km := &keyedMutex{}
	keys := []string{"a", "b", "c", "d", "e"}
	output := []string{"", "", "", "", ""}

	stressRoutine := func(wg *sync.WaitGroup, id int) {
		defer wg.Done()
		r := rand.New(rand.NewSource(int64(id)))
		for i := 0; i < iterations; i++ {
			idx := r.Intn(len(keys))
			km.Lock(keys[idx])
			output[idx] += "x"
			runtime.Gosched()
			km.Unlock(keys[idx])
		}
	}

	wg := &sync.WaitGroup{}
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go stressRoutine(wg, i)
	}
	wg.Wait()

	if len(km.locks) != 0 {
		t.Fatalf("expected no remaining locks")
	}
	if want, got := strings.Repeat("x", goroutines*iterations), strings.Join(output, ""); want != got {
		t.Fatalf("invalid output (want len %d, got len %d)", len(want), len(got))
	}
}

func TestKeyedMutex_Race_CrossGoroutineUnlock(t *testing.T) {
	const iterations = 1000
	km := &keyedMutex{}
	output := ""

	lockRoutine := func(wg *sync.WaitGroup, done chan struct{}) {
		defer wg.Done()
		km.Lock("k")
		output += "x"
		close(done)
	}
	unlockRoutine := func(wg *sync.WaitGroup, done chan struct{}) {
		defer wg.Done()
		<-done
		output += "x"
		km.Unlock("k")
	}

	wg := &sync.WaitGroup{}
	wg.Add(iterations * 2)
	for i := 0; i < iterations; i++ {
		done := make(chan struct{})
		go lockRoutine(wg, done)
		go unlockRoutine(wg, done)
	}
	wg.Wait()

	if len(km.locks) != 0 {
		t.Fatalf("expected no remaining locks")
	}
	if want := strings.Repeat("x", iterations*2); want != output {
		t.Fatalf("invalid output (want len %d, got len %d)", len(want), len(output))
	}
}

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

func requireTokenCacheToEqual(t *testing.T, tc *tokenCache[string], want ...string) {
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

func requireTokenCacheLoadToEqual(t *testing.T, tc *tokenCache[string], key string, want *string) {
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

func requireTokenCacheReplaceToEqual(t *testing.T, tc *tokenCache[string], key string, replaceFunc func(*string) (*string, error), wantValue *string, wantErr error) {
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
	tc := &tokenCache[string]{}
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
	tc := &tokenCache[string]{}
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
