//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared/api"
)

func TestGetProjectInstancesAndProfilesInheritedProfiles(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	ctx := context.Background()

	// A project with features.profiles=false inherits the default project's profiles.
	projectID, err := cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Name: "inheriting"})
	require.NoError(t, err)

	err = cluster.CreateProjectConfig(ctx, tx.Tx(), projectID, map[string]string{"features.profiles": "false"})
	require.NoError(t, err)

	// Create a profile in the default project.
	_, err = cluster.CreateProfile(ctx, tx.Tx(), cluster.Profile{
		Project: api.ProjectDefaultName,
		Name:    "default-profile",
	})
	require.NoError(t, err)

	// Create an instance in the inheriting project which uses that profile.
	instanceID, err := cluster.CreateInstance(ctx, tx.Tx(), cluster.Instance{
		Project:      "inheriting",
		Name:         "c1",
		Node:         "none",
		Type:         instancetype.Container,
		Architecture: 1,
	})
	require.NoError(t, err)

	err = cluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(instanceID), "inheriting", []string{"default-profile"})
	require.NoError(t, err)

	args, err := tx.GetProjectInstancesAndProfiles(ctx, &api.Project{Name: "inheriting"})
	require.NoError(t, err)

	require.Contains(t, args, "inheriting")

	profileNames := make([]string, 0, len(args["inheriting"].Profiles))
	for _, profile := range args["inheriting"].Profiles {
		profileNames = append(profileNames, profile.Name)
	}

	assert.Contains(t, profileNames, "default-profile")
}
