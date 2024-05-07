package config

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/canonical/lxd/shared"
)

// Map is a structured map of config keys to config values.
//
// Each legal key is declared in a config Schema using a Key object.
type Map struct {
	schema Schema
	values map[string]string // Key/value pairs stored in the map.
}

// Load creates a new configuration Map with the given schema and initial
// values. It is meant to be called with a set of initial values that were set
// at a previous time and persisted to some storage like a database.
//
// If one or more keys fail to be loaded, return an ErrorList describing what
// went wrong. Non-failing keys are still loaded in the returned Map.
func Load(schema Schema, values map[string]string) (Map, error) {
	m := Map{
		schema: schema,
	}

	// Populate the initial values.
	_, err := m.update(values)
	return m, err
}

// Change the values of this configuration Map.
//
// Return a map of key/value pairs that were actually changed. If some keys
// fail to apply, details are included in the returned ErrorList.
func (m *Map) Change(changes map[string]string) (map[string]string, error) {
	values := make(map[string]string, len(m.schema))

	errors := ErrorList{}
	for name, change := range changes {
		// Ensure that we were actually passed a string.
		s := reflect.ValueOf(change)
		if s.Kind() != reflect.String {
			errors.add(name, nil, fmt.Sprintf("Invalid type %s", s.Kind()))
			continue
		}

		values[name] = change
	}

	if errors.Len() > 0 {
		return nil, errors
	}

	// Any key not explicitly set, is considered unset.
	for name, key := range m.schema {
		_, ok := values[name]
		if !ok {
			values[name] = key.Default
		}
	}

	names, err := m.update(values)

	changed := map[string]string{}
	for _, name := range names {
		changed[name], err = m.GetRaw(name)
		if err != nil {
			return nil, err
		}
	}

	return changed, err
}

// Dump the current configuration held by this Map.
//
// Keys that match their default value will not be included in the dump.
func (m *Map) Dump() (map[string]string, error) {
	values := map[string]string{}

	for name, value := range m.values {
		key, ok := m.schema[name]
		if ok {
			// Schema key
			value, err := m.GetRaw(name)
			if err != nil {
				return nil, err
			}

			if value != key.Default {
				values[name] = value
			}
		} else if shared.IsUserConfig(name) {
			// User key, just include it as is
			values[name] = value
		}
	}

	return values, nil
}

// GetRaw returns the value of the given key, which must be of type String.
func (m *Map) GetRaw(name string) (string, error) {
	value, ok := m.values[name]
	// User key?
	if shared.IsUserConfig(name) {
		return value, nil
	}

	if !ok {
		// Schema key
		key, err := m.schema.getKey(name)
		if err != nil {
			return "", fmt.Errorf("Failed to get key %q: %w", name, err)
		}

		value = key.Default
	}

	return value, nil
}

// GetString returns the value of the given key, which must be of type String.
func (m *Map) GetString(name string) (string, error) {
	if !shared.IsUserConfig(name) {
		err := m.schema.assertKeyType(name, String)
		if err != nil {
			return "", fmt.Errorf("Failed to assert key type string: %w", err)
		}
	}

	return m.GetRaw(name)
}

// GetBool returns the value of the given key, which must be of type Bool.
func (m *Map) GetBool(name string) (bool, error) {
	err := m.schema.assertKeyType(name, Bool)
	if err != nil {
		return false, fmt.Errorf("Failed to assert key type bool: %w", err)
	}

	value, err := m.GetRaw(name)
	if err != nil {
		return false, err
	}

	return shared.IsTrue(value), nil
}

// GetInt64 returns the value of the given key, which must be of type Int64.
func (m *Map) GetInt64(name string) (int64, error) {
	err := m.schema.assertKeyType(name, Int64)
	if err != nil {
		return 0, fmt.Errorf("Failed to assert key type int64: %w", err)
	}

	value, err := m.GetRaw(name)
	if err != nil {
		return 0, err
	}

	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("Failed to convert to int64: %w", err)
	}

	return n, nil
}

// Update the current values in the map using the newly provided ones. Return a
// list of key names that were actually changed and an ErrorList with possible
// errors.
func (m *Map) update(values map[string]string) ([]string, error) {
	// Detect if this is the first time we're setting values. This happens
	// when Load is called.
	initial := m.values == nil

	if initial {
		m.values = make(map[string]string, len(values))
	}

	// Update our keys with the values from the given map, and keep track
	// of which keys actually changed their value.
	errors := ErrorList{}
	names := []string{}
	for name, value := range values {
		changed, err := m.set(name, value, initial)
		if err != nil {
			errors.add(name, value, err.Error())
			continue
		}

		if changed {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	var err error
	if errors.Len() > 0 {
		errors.sort()
		err = errors
	}

	return names, err
}

// Set or change an individual key. Empty string means delete this value and
// effectively revert it to the default. Return a boolean indicating whether
// the value has changed, and error if something went wrong.
func (m *Map) set(name string, value string, initial bool) (bool, error) {
	// Bypass schema for user.* keys
	if shared.IsUserConfig(name) {
		for _, r := range strings.TrimPrefix(name, "user.") {
			// Only allow letters, digits, and punctuation characters.
			if !unicode.In(r, unicode.Letter, unicode.Digit, unicode.Punct) {
				return false, fmt.Errorf("Invalid key name")
			}
		}

		current, ok := m.values[name]
		if ok && value == current {
			// Value is unchanged
			return false, nil
		}

		if value == "" {
			delete(m.values, name)
		} else {
			m.values[name] = value
		}

		return true, nil
	}

	key, ok := m.schema[name]
	if !ok {
		return false, fmt.Errorf("Unknown key")
	}

	// When unsetting a config key, the value argument will be empty.
	// This ensures that the default value is set if the provided value is empty.
	if value == "" {
		value = key.Default
	}

	err := key.validate(value)
	if err != nil {
		return false, err
	}

	// Normalize boolan values, so the comparison below works fine.
	current, err := m.GetRaw(name)
	if err != nil {
		return false, err
	}

	if key.Type == Bool {
		value = normalizeBool(value)
		current = normalizeBool(current)
	}

	// Compare the new value with the current one, and return now if they
	// are equal.
	if value == current {
		return false, nil
	}

	// Trigger the Setter if this is not an initial load and the key's
	// schema has declared it.
	if !initial && key.Setter != nil {
		value, err = key.Setter(value)
		if err != nil {
			return false, err
		}
	}

	if value == "" {
		delete(m.values, name)
	} else {
		m.values[name] = value
	}

	return true, nil
}

// Normalize a boolean value, converting it to the string "true" or "false".
func normalizeBool(value string) string {
	if shared.IsTrue(value) {
		return "true"
	}

	return "false"
}
