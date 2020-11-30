package config_test

import (
	"testing"

	"github.com/lxc/lxd/lxd/config"
	"github.com/stretchr/testify/assert"
)

func TestAvailableExecutable(t *testing.T) {
	assert.NoError(t, config.AvailableExecutable("ls"))
	assert.NoError(t, config.AvailableExecutable("none"))
	assert.Error(t, config.AvailableExecutable("somenonexistingbin"))
}
