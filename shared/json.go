package shared

import (
	"fmt"
)

type Jmap map[string]any

func (m Jmap) GetString(key string) (string, error) {
	val, ok := m[key]
	if !ok {
		return "", fmt.Errorf("Response was missing `%s`", key)
	} else if val, ok := val.(string); !ok {
		return "", fmt.Errorf("`%s` was not a string", key)
	} else {
		return val, nil
	}
}

func (m Jmap) GetMap(key string) (Jmap, error) {
	val, ok := m[key]
	if !ok {
		return nil, fmt.Errorf("Response was missing `%s`", key)
	} else if val, ok := val.(map[string]any); !ok {
		return nil, fmt.Errorf("`%s` was not a map, got %T", key, m[key])
	} else {
		return val, nil
	}
}

func (m Jmap) GetInt(key string) (int, error) {
	val, ok := m[key]
	if !ok {
		return -1, fmt.Errorf("Response was missing `%s`", key)
	} else if val, ok := val.(float64); !ok {
		return -1, fmt.Errorf("`%s` was not an int", key)
	} else {
		return int(val), nil
	}
}

func (m Jmap) GetBool(key string) (bool, error) {
	val, ok := m[key]
	if !ok {
		return false, fmt.Errorf("Response was missing `%s`", key)
	} else if val, ok := val.(bool); !ok {
		return false, fmt.Errorf("`%s` was not an int", key)
	} else {
		return val, nil
	}
}
