package drivers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared/api"
)

// newAgentCertTestInstance returns a qemu instance whose Path() resolves to a fresh
// temporary directory, along with that path.
func newAgentCertTestInstance(t *testing.T) (*qemu, string) {
	t.Helper()

	t.Setenv("LXD_DIR", t.TempDir())

	d := &qemu{
		common: common{
			dbType:  instancetype.VM,
			name:    "v1",
			project: api.Project{Name: api.ProjectDefaultName},
		},
	}

	instancePath := d.Path()
	require.NoError(t, os.MkdirAll(instancePath, 0700))

	return d, instancePath
}

// TestReadAgentCertReturnsExistingMaterial checks that the certificates are returned
// verbatim and that the server key is not among them.
func TestReadAgentCertReturnsExistingMaterial(t *testing.T) {
	d, instancePath := newAgentCertTestInstance(t)

	for name, contents := range map[string]string{
		"agent.crt":        "AGENT CERT",
		"agent.key":        "AGENT KEY",
		"agent-client.crt": "CLIENT CERT",
		"agent-client.key": "CLIENT KEY",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(instancePath, name), []byte(contents), 0600))
	}

	agentCert, clientCert, clientKey, err := d.readAgentCert()
	require.NoError(t, err)
	assert.Equal(t, "AGENT CERT", agentCert)
	assert.Equal(t, "CLIENT CERT", clientCert)
	assert.Equal(t, "CLIENT KEY", clientKey)
}

// TestReadAgentCertNeverGeneratesMaterial checks that an empty instance directory
// yields an error and that nothing is created.
func TestReadAgentCertNeverGeneratesMaterial(t *testing.T) {
	d, instancePath := newAgentCertTestInstance(t)

	_, _, _, err := d.readAgentCert()
	assert.Error(t, err)

	entries, readErr := os.ReadDir(instancePath)
	require.NoError(t, readErr)
	assert.Empty(t, entries, "readAgentCert must not create anything in the instance directory")
}

// TestReadAgentCertReportsMissingFile checks that the error names the file that could
// not be read.
func TestReadAgentCertReportsMissingFile(t *testing.T) {
	for _, missing := range []string{"agent.crt", "agent-client.crt", "agent-client.key"} {
		t.Run(missing, func(t *testing.T) {
			d, instancePath := newAgentCertTestInstance(t)

			for _, name := range []string{"agent.crt", "agent-client.crt", "agent-client.key"} {
				if name == missing {
					continue
				}

				require.NoError(t, os.WriteFile(filepath.Join(instancePath, name), []byte("x"), 0600))
			}

			_, _, _, err := d.readAgentCert()
			require.Error(t, err)
			assert.Contains(t, err.Error(), missing)
		})
	}
}
