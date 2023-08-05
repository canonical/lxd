package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/lxd/config"
)

// TestAvailableExecutable checks if the AvailableExecutable method correctly identifies existing and non-existing executables.
func TestAvailableExecutable(t *testing.T) {
	assert.NoError(t, config.AvailableExecutable("ls"))
	assert.NoError(t, config.AvailableExecutable("none"))
	assert.Error(t, config.AvailableExecutable("somenonexistingbin"))
}
