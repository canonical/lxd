package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// IdentityProviderGroupsRow is the database representation of an identity provider group.
// db:model identity_provider_groups
type IdentityProviderGroupsRow struct {
	ID   int64  `db:"id"`
	Name string `db:"name"`
}

// APIName returns a human-readable name for the entity.
func (i IdentityProviderGroupsRow) APIName() string {
	return "Identity provider group"
}

// GetIdentityProviderGroups returns all identity provider groups.
func GetIdentityProviderGroups(ctx context.Context, tx *sql.Tx) ([]IdentityProviderGroupsRow, error) {
	return query.Select[IdentityProviderGroupsRow](ctx, tx, "")
}

// GetIdentityProviderGroup returns the identity provider group with the given name.
func GetIdentityProviderGroup(ctx context.Context, tx *sql.Tx, name string) (*IdentityProviderGroupsRow, error) {
	return query.SelectOne[IdentityProviderGroupsRow](ctx, tx, "WHERE name = ?", name)
}

// CreateIdentityProviderGroup adds a new identity provider group to the database.
func CreateIdentityProviderGroup(ctx context.Context, tx *sql.Tx, object IdentityProviderGroupsRow) (int64, error) {
	return query.Create(ctx, tx, object)
}

// DeleteIdentityProviderGroup deletes the identity provider group matching the given name.
func DeleteIdentityProviderGroup(ctx context.Context, tx *sql.Tx, name string) error {
	return query.DeleteOne[IdentityProviderGroupsRow](ctx, tx, "WHERE name = ?", name)
}

// RenameIdentityProviderGroup renames the identity provider group matching the given name.
func RenameIdentityProviderGroup(ctx context.Context, tx *sql.Tx, name string, to string) error {
	result, err := tx.ExecContext(ctx, "UPDATE identity_provider_groups SET name = ? WHERE name = ?", to, name)
	if err != nil {
		if query.IsConflictErr(err) {
			return api.NewStatusError(http.StatusConflict, "An identity provider group already exists with this name")
		}

		return fmt.Errorf("Failed renaming identity provider group: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed getting affected rows: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("Query affected %d rows instead of 1", n)
	}

	return nil
}

// ToAPI converts the IdentityProviderGroupsRow to an api.IdentityProviderGroup, making more database calls as necessary.
func (i *IdentityProviderGroupsRow) ToAPI(ctx context.Context, tx *sql.Tx, canViewGroup auth.PermissionChecker) (*api.IdentityProviderGroup, error) {
	idpGroup := &api.IdentityProviderGroup{
		Name: i.Name,
	}

	groups, err := GetAuthGroupsByIdentityProviderGroupID(ctx, tx, i.ID)
	if err != nil {
		return nil, err
	}

	groupNames := make([]string, 0, len(groups))
	for _, group := range groups {
		if canViewGroup(entity.AuthGroupURL(group.Name)) {
			groupNames = append(groupNames, group.Name)
		}
	}

	idpGroup.Groups = groupNames
	return idpGroup, nil
}

// GetAuthGroupsByIdentityProviderGroupID returns a list of groups that the identity provider group with the given ID.
func GetAuthGroupsByIdentityProviderGroupID(ctx context.Context, tx *sql.Tx, idpGroupID int64) ([]AuthGroupsRow, error) {
	clause := `
JOIN auth_groups_identity_provider_groups ON auth_groups.id = auth_groups_identity_provider_groups.auth_group_id
WHERE auth_groups_identity_provider_groups.identity_provider_group_id = ?`

	return query.Select[AuthGroupsRow](ctx, tx, clause, idpGroupID)
}

// SetIdentityProviderGroupMapping deletes all auth_group -> identity_provider_group mappings from the `auth_groups_identity_provider_groups` table
// where the identity provider group ID is equal to the given value. Then it inserts new associations into the table where the
// group IDs correspond to the given group names.
func SetIdentityProviderGroupMapping(ctx context.Context, tx *sql.Tx, identityProviderGroupID int64, groupNames []string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM auth_groups_identity_provider_groups WHERE identity_provider_group_id = ?`, identityProviderGroupID)
	if err != nil {
		return fmt.Errorf("Failed deleting existing identity provider group mappings: %w", err)
	}

	if len(groupNames) == 0 {
		return nil
	}

	args := []any{identityProviderGroupID}
	for _, groupName := range groupNames {
		args = append(args, groupName)
	}

	q := fmt.Sprintf(`
INSERT INTO auth_groups_identity_provider_groups (auth_group_id, identity_provider_group_id)
SELECT auth_groups.id, ?
FROM auth_groups
WHERE auth_groups.name IN %s
`, query.Params(len(groupNames)))

	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("Failed writing identity provider group mappings: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed checking validity of identity provider group mapping creation: %w", err)
	}

	if int(rowsAffected) != len(groupNames) {
		return fmt.Errorf("Failed writing expected number of rows to identity provider group association table (expected %d, got %d)", len(groupNames), rowsAffected)
	}

	return nil
}

// GetDistinctAuthGroupNamesFromIDPGroupNames returns all of the distinct group names that are mapped to from the given
// list of identity provider group names.
func GetDistinctAuthGroupNamesFromIDPGroupNames(ctx context.Context, tx *sql.Tx, idpGroupNames []string) ([]string, error) {
	if len(idpGroupNames) == 0 {
		return nil, nil
	}

	args := make([]any, 0, len(idpGroupNames))
	for _, idpGroupName := range idpGroupNames {
		args = append(args, idpGroupName)
	}

	q := "\nSELECT DISTINCT auth_groups.name\nFROM auth_groups\nJOIN auth_groups_identity_provider_groups ON auth_groups.id = auth_groups_identity_provider_groups.auth_group_id\nJOIN identity_provider_groups ON auth_groups_identity_provider_groups.identity_provider_group_id = identity_provider_groups.id\nWHERE identity_provider_groups.name IN " + query.Params(len(idpGroupNames))
	mappedGroups, err := query.SelectStrings(ctx, tx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("Failed getting groups from identity provider groups: %w", err)
	}

	return mappedGroups, nil
}
