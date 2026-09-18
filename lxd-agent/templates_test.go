package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/shared/api"
)

func TestTemplatesApplyFileMode(t *testing.T) {
	tests := []struct {
		name            string
		existing        bool
		createOnly      bool
		expectedContent string
		expectedMode    os.FileMode
		expectApplied   bool
	}{
		{
			name:            "new file",
			expectedContent: "templated",
			expectedMode:    0644,
			expectApplied:   true,
		},
		{
			name:            "existing file",
			existing:        true,
			expectedContent: "templated",
			expectedMode:    0600,
			expectApplied:   true,
		},
		{
			name:            "create only existing file",
			existing:        true,
			createOnly:      true,
			expectedContent: "original",
			expectedMode:    0600,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			templateDir := t.TempDir()
			targetPath := filepath.Join(t.TempDir(), "probe")

			metadata := api.ImageMetadata{
				Templates: map[string]*api.ImageMetadataTemplate{
					targetPath: {
						CreateOnly: tt.createOnly,
						Template:   "probe.tpl",
					},
				},
			}

			metadataContent, err := yaml.Marshal(metadata)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(templateDir, "metadata.yaml"), metadataContent, 0644))
			require.NoError(t, os.WriteFile(filepath.Join(templateDir, "probe.tpl.out"), []byte("templated"), 0644))

			if tt.existing {
				require.NoError(t, os.WriteFile(targetPath, []byte("original"), tt.expectedMode))
				require.NoError(t, os.Chmod(targetPath, tt.expectedMode))
			}

			files, err := templatesApply(templateDir)
			require.NoError(t, err)
			if tt.expectApplied {
				assert.Equal(t, []string{targetPath}, files)
			} else {
				assert.Empty(t, files)
			}

			content, err := os.ReadFile(targetPath)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedContent, string(content))

			info, err := os.Stat(targetPath)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedMode, info.Mode().Perm())
		})
	}
}
