package cluster

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

func TestEntityStatementValidity(t *testing.T) {
	schema := Schema()
	db, err := schema.ExerciseUpdate(SchemaVersion, nil)
	require.NoError(t, err)

	for entityType, info := range entityTypes {
		allURLsQuery := info.allURLsQuery()
		if allURLsQuery == "" {
			continue
		}

		_, err := db.Prepare(allURLsQuery)
		assert.NoErrorf(t, err, "Entity statements %q (all): %v", entityType, err)
	}

	for entityType, info := range entityTypes {
		urlByIDQuery := info.urlsByIDsQuery(1)
		if urlByIDQuery == "" {
			continue
		}

		_, err := db.Prepare(urlByIDQuery)
		assert.NoErrorf(t, err, "Entity statements %q (by ID): %v", entityType, err)
	}

	for entityType, info := range entityTypes {
		urlsByProjectQuery := info.urlsByProjectQuery()
		if urlsByProjectQuery == "" {
			continue
		}

		_, err := db.Prepare(urlsByProjectQuery)
		assert.NoErrorf(t, err, "Entity statements %q (by project): %v", entityType, err)
	}

	for outerEntityType, outerEntityInfo := range entityTypes {
		outerQuery := outerEntityInfo.urlsByProjectQuery()
		if outerQuery == "" {
			continue
		}

		for middleEntityType, middleEntityInfo := range entityTypes {
			middleQuery := middleEntityInfo.urlsByIDsQuery(1)
			if middleQuery == "" {
				continue
			}

			for innerEntityType, innerEntityInfo := range entityTypes {
				innerQuery := innerEntityInfo.allURLsQuery()
				if innerQuery == "" {
					continue
				}

				unionQuery := strings.Join([]string{outerQuery, middleQuery, innerQuery}, " UNION ")
				_, err := db.Prepare(unionQuery)
				assert.NoErrorf(t, err, "Union statement (outer: %q; middle: %q; inner: %q): %v", outerEntityType, middleEntityType, innerEntityType, err)
			}
		}
	}

	for outerEntityType, outerEntityInfo := range entityTypes {
		outerQuery := outerEntityInfo.idFromURLQuery()
		if outerQuery == "" {
			continue
		}

		_, err := db.Prepare(outerQuery)
		assert.NoErrorf(t, err, "Entity ID from URL statement %q: %v", outerEntityType, err)
		for innerEntityType, innerEntityInfo := range entityTypes {
			innerQuery := innerEntityInfo.idFromURLQuery()
			if innerQuery == "" {
				continue
			}

			_, err = db.Prepare(strings.Join([]string{outerQuery, innerQuery}, " UNION "))
			assert.NoErrorf(t, err, "Union entity ID from URL statement (outer: %q; inner: %q): %v", outerEntityType, innerEntityType, err)
		}
	}
}

// TestEntityTypesCoversAllEntityTypes checks that every type in [entity.AllTypes] has a corresponding
// entry in the local entityTypes map. If a new entity type is added to shared/entity/type.go without
// a corresponding DB entry here, this test will fail.
func TestEntityTypesCoversAllEntityTypes(t *testing.T) {
	for _, entityType := range entity.AllTypes() {
		_, ok := entityTypes[entityType]
		assert.Truef(t, ok, "entity type %q is missing from lxd/db/cluster entityTypes map", entityType)
	}
}

func TestResolveLegacyImageAliasType(t *testing.T) {
	db, err := Schema().ExerciseUpdate(SchemaVersion, nil)
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	var projectID int64
	err = tx.QueryRow(`SELECT id FROM projects WHERE name = 'default'`).Scan(&projectID)
	require.NoError(t, err)

	containerImageResult, err := tx.Exec(`INSERT INTO images (fingerprint, filename, size, architecture, upload_date, project_id, type) VALUES ('legacy-container', 'legacy-container', 1, 0, 0, ?, 0)`, projectID)
	require.NoError(t, err)

	containerImageID, err := containerImageResult.LastInsertId()
	require.NoError(t, err)

	containerAliasResult, err := tx.Exec(`INSERT INTO images_aliases (name, image_id, description, project_id, image_type) VALUES ('legacy', ?, '', ?, 0)`, containerImageID, projectID)
	require.NoError(t, err)

	containerAliasID, err := containerAliasResult.LastInsertId()
	require.NoError(t, err)

	ctx := context.Background()

	_, err = resolveLegacyImageAliasType(ctx, tx, "default", "missing")
	require.Error(t, err)
	assert.True(t, api.StatusErrorCheck(err, http.StatusNotFound), "expected 404, got %v", err)

	aliasType, err := resolveLegacyImageAliasType(ctx, tx, "default", "legacy")
	require.NoError(t, err)
	assert.Equal(t, "container", aliasType)

	legacyURL := api.NewURL().Path("1.0", "images", "aliases", "legacy").Project("default")
	aliasRefs := map[*api.URL]*EntityRef{legacyURL: {}}
	err = PopulateEntityReferencesFromURLs(ctx, tx, aliasRefs)
	require.NoError(t, err)
	assert.Equal(t, int(containerAliasID), aliasRefs[legacyURL].EntityID)
	assert.Equal(t, []string{"container", "legacy"}, aliasRefs[legacyURL].PathArgs)

	aliasRef, err := GetEntityReferenceFromURL(ctx, tx, legacyURL)
	require.NoError(t, err)
	assert.Equal(t, int(containerAliasID), aliasRef.EntityID)
	assert.Equal(t, []string{"container", "legacy"}, aliasRef.PathArgs)

	vmImageResult, err := tx.Exec(`INSERT INTO images (fingerprint, filename, size, architecture, upload_date, project_id, type) VALUES ('legacy-vm', 'legacy-vm', 1, 0, 0, ?, 1)`, projectID)
	require.NoError(t, err)

	vmImageID, err := vmImageResult.LastInsertId()
	require.NoError(t, err)

	_, err = tx.Exec(`INSERT INTO images_aliases (name, image_id, description, project_id, image_type) VALUES ('legacy', ?, '', ?, 1)`, vmImageID, projectID)
	require.NoError(t, err)

	_, err = resolveLegacyImageAliasType(ctx, tx, "default", "legacy")
	require.Error(t, err)
	assert.True(t, api.StatusErrorCheck(err, http.StatusConflict), "expected 409, got %v", err)

	err = PopulateEntityReferencesFromURLs(ctx, tx, aliasRefs)
	require.Error(t, err)
	assert.True(t, api.StatusErrorCheck(err, http.StatusConflict), "expected 409, got %v", err)
}
