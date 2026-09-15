package predicate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAnd(t *testing.T) {
	isPositive := func(n int) bool { return n > 0 }
	isEven := func(n int) bool { return n%2 == 0 }

	positiveAndEven := And(isPositive, isEven)
	assert.True(t, positiveAndEven(4))
	assert.False(t, positiveAndEven(-4), "even but not positive")
	assert.False(t, positiveAndEven(3), "positive but not even")
	assert.False(t, positiveAndEven(-3), "neither")

	// And() with no predicates is vacuously true.
	assert.True(t, And[int]()(0))
}

func TestAndShortCircuits(t *testing.T) {
	alwaysFalse := func(int) bool { return false }

	ran := false
	neverCalled := func(int) bool {
		ran = true
		return true
	}

	got := And(alwaysFalse, neverCalled)(0)
	assert.False(t, got)
	assert.False(t, ran, "a predicate after a failing one must never run")
}

func TestOr(t *testing.T) {
	isNegative := func(n int) bool { return n < 0 }
	isEven := func(n int) bool { return n%2 == 0 }

	negativeOrEven := Or(isNegative, isEven)
	assert.True(t, negativeOrEven(-3), "negative")
	assert.True(t, negativeOrEven(4), "even")
	assert.True(t, negativeOrEven(-4), "both")
	assert.False(t, negativeOrEven(3), "neither")

	// Or() with no predicates is vacuously false.
	assert.False(t, Or[int]()(0))
}

func TestOrShortCircuits(t *testing.T) {
	alwaysTrue := func(int) bool { return true }

	ran := false
	neverCalled := func(int) bool {
		ran = true
		return false
	}

	got := Or(alwaysTrue, neverCalled)(0)
	assert.True(t, got)
	assert.False(t, ran, "a predicate after a satisfied one must never run")
}

func TestNot(t *testing.T) {
	isEven := func(n int) bool { return n%2 == 0 }

	isOdd := Not(isEven)
	assert.True(t, isOdd(3))
	assert.False(t, isOdd(4))
}
