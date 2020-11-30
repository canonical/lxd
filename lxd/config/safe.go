package config

import (
	"fmt"

	log "github.com/lxc/lxd/shared/log15"

	"github.com/lxc/lxd/shared/logger"
)

// SafeLoad is a wrapper around Load() that does not error when invalid keys
// are found, and just logs warnings instead. Other kinds of errors are still
// returned.
func SafeLoad(schema Schema, values map[string]string) (Map, error) {
	m, err := Load(schema, values)
	if err != nil {
		errors, ok := err.(ErrorList)
		if !ok {
			return m, err
		}
		for _, error := range errors {
			message := fmt.Sprintf("Invalid configuration key: %s", error.Reason)
			logger.Error(message, log.Ctx{"key": error.Name})
		}
	}

	return m, nil
}
