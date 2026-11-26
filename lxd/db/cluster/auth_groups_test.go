package cluster

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

func TestAuthGroupImageAliasPermissionURLs(t *testing.T) {
	db, err := Schema().ExerciseUpdate(SchemaVersion, nil)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	cases := []struct {
		name      string
		imageType int
		shared    bool
	}{
		{name: "unique container", imageType: 0},
		{name: "unique virtual machine", imageType: 1},
		{name: "shared container", imageType: 0, shared: true},
		{name: "shared virtual machine", imageType: 1, shared: true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := db.Begin()
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()

			ctx := context.Background()
			var projectID int64
			err = tx.QueryRow(`SELECT id FROM projects WHERE name = 'default'`).Scan(&projectID)
			require.NoError(t, err)

			var aliasID int64
			for imageType := range 2 {
				result, err := tx.Exec(`INSERT INTO images (fingerprint, filename, size, architecture, upload_date, project_id, type) VALUES (?, 'test', 1, 0, 0, ?, ?)`, tt.name+string(rune('0'+imageType)), projectID, imageType)
				require.NoError(t, err)
				imageID, err := result.LastInsertId()
				require.NoError(t, err)

				if imageType != tt.imageType && !tt.shared {
					continue
				}

				result, err = tx.Exec(`INSERT INTO images_aliases (name, image_id, description, project_id, image_type) VALUES ('legacy', ?, '', ?, ?)`, imageID, projectID, imageType)
				require.NoError(t, err)
				id, err := result.LastInsertId()
				require.NoError(t, err)
				if imageType == tt.imageType {
					aliasID = id
				}
			}

			_, err = tx.Exec(`INSERT INTO auth_groups (name, description) VALUES ('test-group', '')`)
			require.NoError(t, err)
			group, err := GetAuthGroup(ctx, tx, "test-group")
			require.NoError(t, err)
			permissions := []Permission{{EntityType: EntityType(entity.TypeImageAlias), EntityID: int(aliasID), Entitlement: auth.EntitlementCanView}}
			err = SetAuthGroupPermissions(ctx, tx, group.ID, permissions)
			require.NoError(t, err)

			canView := func(*api.URL) bool { return true }
			apiGroup, err := group.ToAPI(ctx, tx, canView, canView)
			require.NoError(t, err)
			require.Len(t, apiGroup.Permissions, 1)

			legacyPermission := api.Permission{
				EntityType:      string(entity.TypeImageAlias),
				EntityReference: "/1.0/images/aliases/legacy?project=default",
				Entitlement:     string(auth.EntitlementCanView),
			}

			imageType := "container"
			if tt.imageType == 1 {
				imageType = "virtual-machine"
			}

			canonicalURL := entity.ImageAliasURL("default", imageType, "legacy").String()
			if tt.shared {
				assert.Equal(t, canonicalURL, apiGroup.Permissions[0].EntityReference)
				assert.NotContains(t, apiGroup.Permissions, legacyPermission)
			} else {
				// Pre-extension clients construct an untyped permission and remove it by exact equality.
				assert.Contains(t, apiGroup.Permissions, legacyPermission)
				remaining := slices.DeleteFunc(slices.Clone(apiGroup.Permissions), func(permission api.Permission) bool {
					return permission == legacyPermission
				})
				assert.Empty(t, remaining)
			}

			// Single-group and recursive group responses use the same representation.
			_, groupURLs, err := GetAuthGroupPermissionEntityURLs(ctx, tx, permissions)
			require.NoError(t, err)
			assert.Equal(t, apiGroup.Permissions[0].EntityReference, groupURLs[entity.TypeImageAlias][int(aliasID)].String())

			// Presentation must not change the canonical authorization URL or database identity.
			_, canonicalURLs, err := GetPermissionEntityURLs(ctx, tx, permissions)
			require.NoError(t, err)
			assert.Equal(t, canonicalURL, canonicalURLs[entity.TypeImageAlias][int(aliasID)].String())
			ref, err := GetEntityReferenceFromURL(ctx, tx, groupURLs[entity.TypeImageAlias][int(aliasID)])
			require.NoError(t, err)
			assert.Equal(t, int(aliasID), ref.EntityID)
		})
	}
}
