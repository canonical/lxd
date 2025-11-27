package config

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Schema defines the available keys of a config Map, along with the types
// and options for their values, expressed using Key objects.
type Schema struct {
	sync.RWMutex
	Types map[string]Key
}

// Keys returns all keys defined in the schema.
func (s *Schema) Keys() []string {
	s.RLock()
	keys := make([]string, len(s.Types))
	i := 0
	for key := range s.Types {
		keys[i] = key
		i++
	}

	s.RUnlock()

	sort.Strings(keys)
	return keys
}

// Defaults returns a map of all key names in the schema along with their default
// values.
func (s *Schema) Defaults() map[string]any {
	s.RLock()
	values := make(map[string]any, len(s.Types))
	for name, key := range s.Types {
		values[name] = key.Default
	}

	s.RUnlock()

	return values
}

// Get the Key associated with the given name, or panic.
func (s *Schema) mustGetKey(name string) Key {
	s.RLock()
	key, ok := s.Types[name]
	s.RUnlock()
	if !ok {
		panic(fmt.Sprintf("Attempt to access unknown key %q", name))
	}

	return key
}

// Assert that the Key with the given name as the given type. Panic if no Key
// with such name exists, or if it does not match the tiven type.
func (s *Schema) assertKeyType(name string, code Type) {
	key := s.mustGetKey(name)
	if key.Type != code {
		panic(fmt.Sprintf("Key %q has type code %d, not %d", name, key.Type, code))
	}
}

// Key defines the type of the value of a particular config key, along with
// other knobs such as default, validator, etc.
type Key struct {
	Type       Type   // Type of the value. It defaults to String.
	Default    string // If the key is not set in a Map, use this value instead.
	Deprecated string // Optional message to set if this config value is deprecated.

	// Optional function used to validate the values. It's called by Map
	// all the times the value associated with this Key is going to be
	// changed.
	Validator func(string) error

	// Optional function to manipulate a value before it's actually saved
	// in a Map. It's called only by Map.Change(), and not by Load() since
	// values passed to Load() are supposed to have been previously
	// processed.
	Setter func(string) (string, error)
}

// Type is a numeric code identifying a node value type.
type Type int

// Possible Value types.
const (
	String Type = iota
	Bool
	Int64
)

// Tells if the given value can be assigned to this particular Value instance.
func (v *Key) validate(value string) error {
	validator := v.Validator
	if validator == nil {
		// Dummy validator
		validator = func(string) error { return nil }
	}

	// Handle unsetting
	if value == "" {
		return validator(v.Default)
	}

	switch v.Type {
	case String:
	case Bool:
		if !slices.Contains(booleans, strings.ToLower(value)) {
			return errors.New("Invalid boolean")
		}

	case Int64:
		_, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return errors.New("Invalid integer")
		}

	default:
		panic(fmt.Sprintf("Unexpected value type: %d", v.Type))
	}

	if v.Deprecated != "" && value != v.Default {
		return fmt.Errorf("Deprecated: %q", v.Deprecated)
	}

	// Run external validation function
	return validator(value)
}

var booleans = []string{"true", "false", "1", "0", "yes", "no", "on", "off"}
