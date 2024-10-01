package nodes

import (
	"fmt"
	"hash/fnv"
	"reflect"
	"sort"
)

func hash[T any](data T) int64 {
	switch v := any(data).(type) {
	case map[string]any:
		return hashMap(v)
	case []any:
		return hashList(v)
	default:
		h := fnv.New64a()
		h.Write([]byte(hashValue(v)))
		return int64(h.Sum64())
	}
}

func hashValue[T any](v T) string {
	switch val := any(v).(type) {
	case string, int, int64, float64, bool:
		return fmt.Sprintf("%v", val)
	case []any:
		return fmt.Sprintf("%d", hashList(val))
	case map[string]any:
		return fmt.Sprintf("%d", hashMap(val))
	default:
		if reflect.TypeOf(v).Kind() == reflect.Slice {
			s := reflect.ValueOf(v)
			anySlice := make([]any, s.Len())
			for i := 0; i < s.Len(); i++ {
				anySlice[i] = s.Index(i).Interface()
			}

			return fmt.Sprintf("%d", hashList(anySlice))
		}

		return fmt.Sprintf("%v", reflect.ValueOf(v))
	}
}

func hashList[T any](l []T) int64 {
	h := fnv.New64a()
	hashes := make([]string, len(l))
	for i, v := range l {
		hashes[i] = hashValue(v)
	}

	sort.Strings(hashes)
	for _, hash := range hashes {
		h.Write([]byte(hash))
	}

	return int64(h.Sum64())
}

func hashMap(m map[string]any) int64 {
	h := fnv.New64a()
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)
	for _, k := range keys {
		v := m[k]
		pair := fmt.Sprintf("%s:%v", k, hashValue(v))
		h.Write([]byte(pair))
	}

	return int64(h.Sum64())
}

// composeHashes combines multiple hashes into a single hash.
// The order of composition doesn't affect the final hash because the
// operation is both commutative and associative.
func composeHashes(hashes ...int64) int64 {
	var result int64
	for _, hash := range hashes {
		result ^= hash
	}

	return result
}
