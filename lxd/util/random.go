package util

import (
	"fmt"
	"hash/fnv"
	"io"
	"math/rand"

	"github.com/pkg/errors"
)

// GetStableRandomGenerator returns a stable random generator. Uses the FNV-1a hash algorithm to convert the seed
// string into an int64 for use as seed to the non-cryptographic random number generator.
func GetStableRandomGenerator(seed string) (*rand.Rand, error) {
	hash := fnv.New64a()

	_, err := io.WriteString(hash, seed)
	if err != nil {
		return nil, err
	}

	return rand.New(rand.NewSource(int64(hash.Sum64()))), nil
}

// GetStableRandomInt64FromList returns a stable random value from a given list.
func GetStableRandomInt64FromList(seed int, list []int64) (int64, error) {
	if len(list) <= 0 {
		return 0, fmt.Errorf("Cannot get stable random value from empty list")
	}

	r, err := GetStableRandomGenerator(fmt.Sprintf("%d", seed))
	if err != nil {
		return 0, errors.Wrap(err, "Failed to get stable random generator")
	}

	return list[r.Int63n(int64(len(list)))], nil
}
