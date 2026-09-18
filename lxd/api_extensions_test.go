package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/features"
)

func TestVisibleAPIExtensions(t *testing.T) {
	t.Cleanup(func() {
		require.NoError(t, features.LoadFromEnv(features.EnvVar))
	})

	t.Setenv(features.EnvVar, "")
	require.NoError(t, features.LoadFromEnv(features.EnvVar))

	const testExtension = "test_gated_extension"
	extensions := []string{"foo", "bar", testExtension}
	gatedAPIExtensions[testExtension] = features.FailureDomainPlacement
	defer delete(gatedAPIExtensions, testExtension)

	assert.Equal(t, []string{"foo", "bar"}, visibleAPIExtensions(extensions))

	t.Setenv(features.EnvVar, string(features.FailureDomainPlacement))
	require.NoError(t, features.LoadFromEnv(features.EnvVar))
	assert.Equal(t, extensions, visibleAPIExtensions(extensions))
}
