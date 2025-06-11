package lxd

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/canonical/lxd/shared/api"
)

// GetConfigURLs retrieves a list of configuration key paths.
func (r *ProtocolDevLXD) GetConfigURLs() ([]string, error) {
	var keyPaths []string

	// Fetch list of config key url paths.
	_, err := r.queryStruct(http.MethodGet, "/config", nil, "", &keyPaths)
	if err != nil {
		return nil, err
	}

	return keyPaths, nil
}

// GetConfig retrieves a guest's configuration as a map.
func (r *ProtocolDevLXD) GetConfig() (map[string]string, error) {
	keyPaths, err := r.GetConfigURLs()
	if err != nil {
		return nil, err
	}

	// Iterate over key paths and fetch their values.
	config := make(map[string]string, len(keyPaths))
	for _, path := range keyPaths {
		// Extract the key from the path.
		_, key, ok := strings.Cut(path, "/config/")
		if !ok {
			continue
		}

		// Fetch the value for the key.
		value, err := r.GetConfigByKey(key)
		if err != nil {
			return nil, err
		}

		config[key] = value
	}

	return config, nil
}

// GetConfigByKey retrieves a guest's configuration for the given key.
func (r *ProtocolDevLXD) GetConfigByKey(key string) (string, error) {
	url := api.NewURL().Path("config", key).URL
	resp, _, err := r.query(http.MethodGet, url.String(), nil, "")
	if err != nil {
		return "", err
	}

	if r.isDevLXDOverVsock {
		var value string

		// The returned string value is JSON encoded.
		err = json.Unmarshal(resp.Content, &value)
		if err != nil {
			return "", err
		}

		return value, nil
	}

	return string(resp.Content), nil
}
