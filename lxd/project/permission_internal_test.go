package project

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/instance/instancetype"
)

// checkContainerInstanceRestrictions runs the project restriction checks against a single
// container instance carrying the given config.
func checkContainerInstanceRestrictions(projectConfig map[string]string, instanceConfig map[string]string) error {
	proj := &db.Project{
		Name:   "proj1",
		Config: projectConfig,
	}

	inst := db.Instance{
		Name:   "c1",
		Type:   instancetype.Container,
		Config: instanceConfig,
	}

	return checkRestrictions(proj, []db.Instance{inst}, []db.Profile{})
}

func TestProjectRestrictedContainerPrivilegeIsolated(t *testing.T) {
	isolatedProject := map[string]string{
		"restricted":                      "true",
		"restricted.containers.privilege": "isolated",
	}

	// A container that omits "security.idmap.isolated" must be rejected, because the key
	// defaults to non-isolated and the restriction must not be bypassable by simply
	// leaving it unset.
	err := checkContainerInstanceRestrictions(isolatedProject, map[string]string{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "Non-isolated containers are forbidden")

	// An explicit "security.idmap.isolated=false" must be rejected.
	err = checkContainerInstanceRestrictions(isolatedProject, map[string]string{
		"security.idmap.isolated": "false",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "Non-isolated containers are forbidden")

	// An explicit empty "security.idmap.isolated" must be rejected.
	err = checkContainerInstanceRestrictions(isolatedProject, map[string]string{
		"security.idmap.isolated": "",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "Non-isolated containers are forbidden")

	// An isolated container must be allowed.
	err = checkContainerInstanceRestrictions(isolatedProject, map[string]string{
		"security.idmap.isolated": "true",
	})
	require.NoError(t, err)
}
