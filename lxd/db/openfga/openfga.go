//go:build linux && cgo && !agent

package openfga

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/openfga/openfga/pkg/storage"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// NewOpenFGAStore returns a new storage.OpenFGADatastore that is backed directly by the dqlite database.
func NewOpenFGAStore(clusterDB *db.Cluster) storage.OpenFGADatastore {
	store := &openfgaStore{
		clusterDB: clusterDB,
	}

	return store
}

// openfgaStore is an implementation of storage.OpenFGADatastore that reads directly from our cluster database.
type openfgaStore struct {
	clusterDB *db.Cluster
	model     *openfgav1.AuthorizationModel
}

// Read reads multiple tuples from the store. Various predicates are applied based on the given key.
//
// Observations:
//   - This method is only called on Check requests.
//   - The `Relation` field of the given key is always either `project` or `server`.
//   - The `Object` field is never a group or identity.
//
// Implementation:
//   - When the `Relation` field of the given key is `server`, OpenFGA is asking "what objects of type `server` are related to the
//     object in the `Object` field. This is because the OpenFGA model does not specify that this is a one-to-many relationship.
//     In our case, we know that there is only one `server` object. So we can return a tuple that relates the `Object` in the tuple
//     to the server object with the `server` relation.
//   - When the `Relation` field of the given key is `project`, OpenFGA is asking "what objects of type `project` are related to the
//     object in the `Object` filed. Again, OpenFGA doesn't know that this is one-to-many. Since the URL of the object contains the
//     project name, we can parse this URL and return a tuple that relates the `Object` in the tuple to a project object via the
//     `project` relation.
//   - For any other relations or unexpected input, return an error.
//
// Notes:
//   - This method doesn't actually perform any queries (win!).
//   - If we change our design to use entity IDs directly, this method will need to change so that we can return the correct project ID.
//     (Currently we don't need to as the project name is already in the URL).
func (o *openfgaStore) Read(ctx context.Context, s string, key *openfgav1.TupleKey) (storage.TupleIterator, error) {
	obj := key.GetObject()
	relation := key.GetRelation()
	user := key.GetUser()

	hasObj := obj != ""
	hasRelation := relation != ""
	hasUser := user != ""

	// We always expect the `Object` field to be present.
	if !hasObj {
		return nil, fmt.Errorf("Read: Can only list by object")
	}

	// Users are what we are going to enumerate.
	if hasUser {
		return nil, fmt.Errorf("Read: Listing by user not supported")
	}

	// Expect the relation to be present.
	if !hasRelation {
		return nil, fmt.Errorf("Read: Listing all objects without a relation not supported")
	}

	// Validate the object. We expect the URL to be present.
	entityTypeStr, entityURL, hasURL := strings.Cut(obj, ":")
	if !hasURL {
		return nil, fmt.Errorf("Read: Listing all entities of type not supported")
	}

	entityType := entity.Type(entityTypeStr)
	err := entityType.Validate()
	if err != nil {
		return nil, fmt.Errorf("Read: Invalid object filter %q: %w", obj, err)
	}

	u, err := url.Parse(entityURL)
	if err != nil {
		return nil, fmt.Errorf("Read: Failed to parse entity URL %q: %w", entityURL, err)
	}

	urlEntityType, projectName, _, _, err := entity.ParseURL(*u)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse entity URL %q: %w", entityURL, err)
	}

	if urlEntityType != entityType {
		return nil, fmt.Errorf("Entity URL %q does not match tuple entity type (expected %q, got %q)", entityURL, entityType, urlEntityType)
	}

	requiresProject, err := entityType.RequiresProject()
	if err != nil {
		return nil, err
	}

	var tuples []*openfgav1.Tuple
	switch relation {
	case "project":
		// If the entity type is not project specific but we're looking for project relations then the input is invalid.
		// (Likely an error in the authorization driver).
		if !requiresProject {
			return nil, fmt.Errorf("Received unexpected query, entities of type %q do not have a project relation", entityType)
		}

		// Return a tuple relating the object to its parent project.
		tuples = []*openfgav1.Tuple{
			{
				Key: &openfgav1.TupleKey{
					Object:   obj,
					Relation: relation,
					User:     fmt.Sprintf("%s:%s", entity.TypeProject, entity.ProjectURL(projectName).String()),
				},
			},
		}

	case "server":
		// If the entity type is project specific but we're looking for server relations then the input is invalid.
		// (Likely an error in the authorization driver).
		if requiresProject {
			return nil, fmt.Errorf("Received unexpected query, entities of type %q do not have a server relation", entityType)
		}

		// Return a tuple relating the object to the server.
		tuples = []*openfgav1.Tuple{
			{
				Key: &openfgav1.TupleKey{
					Object:   obj,
					Relation: relation,
					User:     fmt.Sprintf("%s:%s", entity.TypeServer, entity.ServerURL().String()),
				},
			},
		}

	default:
		// Return an error if we get an unexpected relation.
		return nil, fmt.Errorf("Relation %q not supported", relation)
	}

	return storage.NewStaticTupleIterator(tuples), nil
}

// ReadUserTuple reads a single tuple from the store.
//
// Observations:
//   - This method is only called when the `User` field in the given openfgav1.TupleKey is `identity:<identity URL>`.
//   - Our permissions management system currently does not allow identities to be granted entitlements directly.
//     Therefore, the only relation that an identity can have is to a `group` via the `member` relation or to a `project` via the
//     `operator` relation (if the identity is a restricted TLS client). In both of these cases, the tuples have been passed into
//     the OpenFGA `Check` or `ListObjects` request as contextual tuples.
//
// Implementation:
//   - The tuples that this method is meant to return have been passed in contextually. So validate the input matches
//     what is expected and return nil.
func (o *openfgaStore) ReadUserTuple(ctx context.Context, store string, tk *openfgav1.TupleKey) (*openfgav1.Tuple, error) {
	// Expect the User field to be present.
	user := tk.GetUser()
	if user == "" {
		return nil, fmt.Errorf("ReadUserTuple: User field of tuple key must be provided")
	}

	// Only allow `identity` for the User type.
	userEntityType, _, ok := strings.Cut(user, ":")
	if !ok {
		return nil, fmt.Errorf("ReadUserTuple: Unexpected format of user field %q", user)
	}

	if entity.Type(userEntityType) != entity.TypeIdentity {
		return nil, fmt.Errorf("ReadUserTuple: Entity type %q not supported", userEntityType)
	}

	return nil, nil
}

// ReadUsersetTuples is called on check requests. It is used to read all the "users" that have a given relation to
// a given object. In this context, the "user" may not be the identity making requests to LXD. In OpenFGA, a "user"
// is any entity that can be related to an object (https://openfga.dev/docs/concepts#what-is-a-user). For example, in
// our model, `project` can be related to `instance` via a `project` relation, so `project:/1.0/projects/default` could
// be considered a user. The opposite is not true, so an `instance` cannot be a user.
//
// Observations:
//   - The input filter always has an object and a relation.
//   - The relation is never `member`, `project`, or `server`.
//
// Implementation:
//   - Since the relation is not `member`, `project`, or `server` it is an entitlement that may form part of a
//     permission, and only groups have permissions. So we first get the ID of the entity via the URL that is
//     part of the Object. Then we get the permission for that entity ID, entity type, and relation (entitlement).
//     Finally, we return all groups that have that permission.
//   - One exception to the above is the "type bound public access" (https://openfga.dev/docs/modeling/public-access)
//     that is defined for `server` `can_view`, which allows all identities access to `GET /1.0` and `GET /1.0/storage`.
//     We check for this case before making any DB queries.
//
// Notes:
//   - The exception for the type-bound public access may be better placed as a contextual tuple. However, adding it here
//     means we can avoid an unnecessary transaction that will happen a lot.
//   - We will need to modify this exception when we implement service accounts.
func (o *openfgaStore) ReadUsersetTuples(ctx context.Context, store string, filter storage.ReadUsersetTuplesFilter) (storage.TupleIterator, error) {
	// Expect both an object and a relation.
	if filter.Object == "" || filter.Relation == "" {
		return nil, fmt.Errorf("ReadUsersetTuples: Filter must include both an object and a relation")
	}

	// Expect a URL to be present for the object. (E.g. we don't want to list all groups that have `can_view` on
	// all projects, we should be checking for a specific entity).
	entityTypeStr, entityURL, hasURL := strings.Cut(filter.Object, ":")
	if !hasURL {
		return nil, fmt.Errorf("ReadUsersetTuples: Listing all entities of type not supported")
	}

	entityType := entity.Type(entityTypeStr)
	err := entityType.Validate()
	if err != nil {
		return nil, fmt.Errorf("ReadUsersetTuples: Invalid object filter %q: %w", filter.Object, err)
	}

	// Check for type-bound public access exception.
	if entityType == entity.TypeServer && filter.Relation == "can_view" {
		return storage.NewStaticTupleIterator([]*openfgav1.Tuple{
			// Only returning one tuple here for the identity. When adding service accounts, we'll need
			// to add another tuple to account for them.
			{
				Key: &openfgav1.TupleKey{
					Object:   fmt.Sprintf("%s:%s", entity.TypeServer, entity.ServerURL().String()),
					Relation: string(auth.EntitlementCanView),
					User:     fmt.Sprintf("%s:*", entity.TypeIdentity),
				},
			},
		}), nil
	}

	u, err := url.Parse(entityURL)
	if err != nil {
		return nil, fmt.Errorf("ReadUsersetTuples: Failed to parse entity URL %q: %w", entityURL, err)
	}

	var groupNames []string
	err = o.clusterDB.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the ID of the entity.
		entityRef, err := cluster.GetEntityReferenceFromURL(ctx, tx.Tx(), &api.URL{URL: *u})
		if err != nil {
			return err
		}

		// Get all groups with the permission.
		q := `
SELECT auth_groups.name
FROM auth_groups_permissions
JOIN auth_groups ON auth_groups_permissions.auth_group_id = auth_groups.id
WHERE auth_groups_permissions.entitlement = ? AND auth_groups_permissions.entity_type = ? AND auth_groups_permissions.entity_id = ?
`
		groupNames, err = query.SelectStrings(ctx, tx.Tx(), q, filter.Relation, cluster.EntityType(entityType), entityRef.EntityID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		if !api.StatusErrorCheck(err, http.StatusNotFound) {
			// If we have a not found error then there are no tuples to return, but the datastore shouldn't return an error.
			return storage.NewStaticTupleIterator(nil), nil
		}

		return nil, err
	}

	// Return the groups as tuples relating them to the object via the relation.
	tuples := make([]*openfgav1.Tuple, 0, len(groupNames))
	for _, groupName := range groupNames {
		tuples = append(tuples, &openfgav1.Tuple{
			Key: &openfgav1.TupleKey{
				Object:   filter.Object,
				Relation: filter.Relation,
				// Members of the group have the permission ("#member"), not the group itself.
				User: fmt.Sprintf("%s:%s#member", entity.TypeAuthGroup, entity.AuthGroupURL(groupName)),
			},
		})
	}

	return storage.NewStaticTupleIterator(tuples), nil
}

// ReadStartingWithUser is used when listing user objects.
//
// Observations:
//
// - This method appears to be called in four scenarios:
//
//  1. Listing objects related to the server object via `server`.
//
//  2. Listing objects related to project objects via `project`.
//
//  3. Listing objects that a group is related to via an entitlement.
//
//  4. Listing objects that an identity is related to via an entitlement.
//
//     - The UserFilter field of storage.ReadStartingWithUserFilter usually has length 1. Sometimes there is another user
//     when a type-bound public access is used (e.g. `identity:*`). We return early if this is the case, as we don't need
//     to call the database.
//
// Implementation:
//   - For the first two cases we can perform a simple lookup for entities of the requested type (with project name if project relation).
//   - In the third case, we need to get all permissions with the given entity type and entitlement that are associated with the given group.
//   - For the fourth case we return nil, since we expect direct entitlements for identities to be passed in contextually.
func (o *openfgaStore) ReadStartingWithUser(ctx context.Context, store string, filter storage.ReadStartingWithUserFilter) (storage.TupleIterator, error) {
	// Example expected input, case 1:
	// filter.ObjectType = "certificate"
	// filter.Relation = "server"
	// filter.UserFilter[0].Object = "server:/1.0"
	//
	// Result: List all certificate tuples.

	// Example expected input, case 2:
	// filter.ObjectType = "instance"
	// filter.Relation = "project"
	// filter.UserFilter[0].Object = "project:/1.0/projects/default"
	//
	// Result: List all instances in the default project.

	// Example expected input, case 3:
	// filter.ObjectType = "storage_volume"
	// filter.Relation = "can_manage_snapshots"
	// filter.UserFilter[0].Object = "group:/1.0/auth/groups/my-group"
	// filter.UserFilter[0].Relation = "member"
	//
	// Result: List all storage volumes for which the given group has the "can_manage_snapshots" entitlement.

	// Example expected input, case 4:
	// filter.ObjectType = "storage_volume"
	// filter.Relation = "can_manage_snapshots"
	// filter.UserFilter[0].Object = "identity:/1.0/auth/identities/oidc/jane.doe@example.com"
	//
	// Result: Return nil.

	// Expect the object type to be present.
	if filter.ObjectType == "" {
		return nil, api.StatusErrorf(http.StatusBadRequest, "ReadStartingWithUser: Must provide object type")
	}

	// Expect the relation to be present.
	if filter.Relation == "" {
		return nil, api.StatusErrorf(http.StatusBadRequest, "ReadStartingWithUser: Must provide relation")
	}

	entityType := entity.Type(filter.ObjectType)
	err := entityType.Validate()
	if err != nil {
		return nil, fmt.Errorf("ReadUsersetTuples: Invalid object filter %q: %w", entityType, err)
	}

	// Check for type-bound public access exception.
	if entityType == entity.TypeServer && filter.Relation == string(auth.EntitlementCanView) {
		return storage.NewStaticTupleIterator([]*openfgav1.Tuple{
			// Only returning one tuple here for the identity. When adding service accounts, we'll need
			// to add another tuple to account for them.
			{
				Key: &openfgav1.TupleKey{
					Object:   fmt.Sprintf("%s:%s", entity.TypeServer, entity.ServerURL().String()),
					Relation: string(auth.EntitlementCanView),
					User:     fmt.Sprintf("%s:*", entity.TypeIdentity),
				},
			},
		}), nil
	}

	// Expect that there will be exactly one user filter when not dealing with a type-bound public access.
	if len(filter.UserFilter) != 1 {
		return nil, fmt.Errorf("ReadStartingWithUser: Unexpected user filter list length")
	}

	// Expect that the user filter object has an entity type and a URL.
	userTypeStr, userURL, ok := strings.Cut(filter.UserFilter[0].GetObject(), ":")
	if !ok {
		return nil, fmt.Errorf("ReadStartingWithUser: Must provide user reference")
	}

	// Validate the user entity type.
	userEntityType := entity.Type(userTypeStr)
	err = userEntityType.Validate()
	if err != nil {
		return nil, fmt.Errorf("ReadStartingWithUser: Invalid user type %q: %w", userTypeStr, err)
	}

	// Parse the user URL.
	u, err := url.Parse(userURL)
	if err != nil {
		return nil, fmt.Errorf("ReadStartingWithUser: Failed to parse user entity URL %q: %w", userURL, err)
	}

	_, _, _, userURLPathArguments, err := entity.ParseURL(*u)
	if err != nil {
		return nil, fmt.Errorf("ReadStartingWithUser: Unexpected user entity URL %q: %w", userURL, err)
	}

	// If the relation is "project" or "server", we are listing all resources under the project/server.
	if filter.Relation == "project" || filter.Relation == "server" {
		// Expect that the user entity type is expected for the relation.
		if filter.Relation == "project" && userEntityType != entity.TypeProject {
			return nil, fmt.Errorf("ReadStartingWithUser: Cannot list project relations for non-project entities")
		} else if filter.Relation == "server" && userEntityType != entity.TypeServer {
			return nil, fmt.Errorf("ReadStartingWithUser: Cannot list server relations for non-server entities")
		}

		// If the filter is by project, we want to filter entity URLs by the project name.
		var projectName string
		if filter.Relation == "project" {
			// The project name is the first path argument of a project URL.
			projectName = userURLPathArguments[0]
		}

		// Get the entity URLs with the given type and project (if set).
		var entityURLs map[entity.Type]map[int]*api.URL
		err = o.clusterDB.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			entityURLs, err = cluster.GetEntityURLs(ctx, tx.Tx(), projectName, entityType)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, err
		}

		// Compose the expected tuples relating the server/project to the entities.
		var tuples []*openfgav1.Tuple
		for _, entityURL := range entityURLs[entityType] {
			if filter.Relation == "project" {
				tuples = append(tuples, &openfgav1.Tuple{
					Key: &openfgav1.TupleKey{
						Object:   fmt.Sprintf("%s:%s", entityType, entityURL.String()),
						Relation: "project",
						User:     fmt.Sprintf("%s:%s", entity.TypeProject, entity.ProjectURL(projectName)),
					},
				})
			} else {
				tuples = append(tuples, &openfgav1.Tuple{
					Key: &openfgav1.TupleKey{
						Object:   fmt.Sprintf("%s:%s", entityType, entityURL.String()),
						Relation: "server",
						User:     fmt.Sprintf("%s:%s", entity.TypeServer, entity.ServerURL()),
					},
				})
			}
		}

		return storage.NewStaticTupleIterator(tuples), nil
	}

	// Return an empty iterator (nil) when the user entity type is "identity", as we expect these tuples to be passed in
	// contextually. Note: We will likely need to update this if/when we add service accounts.
	if userEntityType == entity.TypeIdentity {
		return nil, nil
	}

	// Expect the user entity type to be "group", no other cases are handled.
	if userEntityType != entity.TypeAuthGroup {
		return nil, fmt.Errorf("ReadStartingWithUser: Unexpected user filter entity type %q", userEntityType)
	}

	// Construct a query to list permissions with the given entity type and entitlement for the given group.
	q := `
SELECT auth_groups_permissions.entity_type, auth_groups_permissions.entity_id, auth_groups_permissions.entitlement
FROM auth_groups_permissions
JOIN auth_groups ON auth_groups_permissions.auth_group_id = auth_groups.id
WHERE auth_groups_permissions.entitlement = ? AND auth_groups_permissions.entity_type = ? AND auth_groups.name = ?
`
	groupName := userURLPathArguments[0]
	args := []any{filter.Relation, cluster.EntityType(filter.ObjectType), groupName}

	var entityURLs map[entity.Type]map[int]*api.URL
	var permissions []cluster.Permission
	err = o.clusterDB.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		rows, err := tx.Tx().QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}

		for rows.Next() {
			var permission cluster.Permission
			err = rows.Scan(&permission.EntityType, &permission.EntityID, &permission.Entitlement)
			if err != nil {
				return err
			}

			permissions = append(permissions, permission)
		}

		// Get the URLs of the permissions we've queried for and filter out any invalid ones.
		// Ignore the dangling permissions to make as few queries as possible.
		permissions, entityURLs, err = cluster.GetPermissionEntityURLs(ctx, tx.Tx(), permissions)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Construct the tuples relating the group to the entities via the expected entitlement.
	var tuples []*openfgav1.Tuple
	for _, permission := range permissions {
		tuples = append(tuples, &openfgav1.Tuple{
			Key: &openfgav1.TupleKey{
				Object:   fmt.Sprintf("%s:%s", permission.EntityType, entityURLs[entity.Type(permission.EntityType)][permission.EntityID].String()),
				Relation: string(permission.Entitlement),
				// Members of the group have the permission ("#member"), not the group itself.
				User: fmt.Sprintf("%s:%s#member", entity.TypeAuthGroup, entity.AuthGroupURL(groupName)),
			},
		})
	}

	return storage.NewStaticTupleIterator(tuples), nil
}

// ReadPage is not implemented. It is not required for the functionality we need.
func (*openfgaStore) ReadPage(ctx context.Context, store string, tk *openfgav1.TupleKey, opts storage.PaginationOptions) ([]*openfgav1.Tuple, []byte, error) {
	return nil, nil, api.StatusErrorf(http.StatusNotImplemented, "not implemented")
}

// Write is not implemented, we should never be performing writes because we are reading directly from the cluster DB.
func (*openfgaStore) Write(ctx context.Context, store string, d storage.Deletes, w storage.Writes) error {
	return api.StatusErrorf(http.StatusNotImplemented, "not implemented")
}

// WriteAuthorizationModel sets the model.
func (o *openfgaStore) WriteAuthorizationModel(ctx context.Context, store string, model *openfgav1.AuthorizationModel) error {
	o.model = model
	return nil
}

// ReadAuthorizationModel returns the model that has been set or an error if it hasn't been set.
func (o *openfgaStore) ReadAuthorizationModel(ctx context.Context, store string, id string) (*openfgav1.AuthorizationModel, error) {
	if o.model != nil {
		return o.model, nil
	}

	return nil, storage.ErrNotFound
}

// FindLatestAuthorizationModel returns the model that has been set or an error if it hasn't been set.
func (o *openfgaStore) FindLatestAuthorizationModel(ctx context.Context, store string) (*openfgav1.AuthorizationModel, error) {
	if o.model != nil {
		return o.model, nil
	}

	return nil, storage.ErrNotFound
}

// ReadAuthorizationModels returns a slice containing our own model or an error if it hasn't been set yet.
func (o *openfgaStore) ReadAuthorizationModels(ctx context.Context, store string, options storage.PaginationOptions) ([]*openfgav1.AuthorizationModel, []byte, error) {
	if o.model != nil {
		return []*openfgav1.AuthorizationModel{o.model}, nil, nil
	}

	return nil, nil, fmt.Errorf("Authorization model not set")
}

// MaxTuplesPerWrite returns -1 because we should never be writing to the store.
func (*openfgaStore) MaxTuplesPerWrite() int {
	return -1
}

// MaxTypesPerAuthorizationModel returns the default value. It doesn't matter as long as it's higher than the number of
// types in our built-in model.
func (*openfgaStore) MaxTypesPerAuthorizationModel() int {
	return 100
}

// CreateStore returns a not implemented error, because there is only one store.
func (*openfgaStore) CreateStore(ctx context.Context, store *openfgav1.Store) (*openfgav1.Store, error) {
	return nil, api.StatusErrorf(http.StatusNotImplemented, "not implemented")
}

// DeleteStore returns a not implemented error, because there is only one store.
func (*openfgaStore) DeleteStore(ctx context.Context, id string) error {
	return api.StatusErrorf(http.StatusNotImplemented, "not implemented")
}

// GetStore returns a not implemented error, because there is only one store.
func (*openfgaStore) GetStore(ctx context.Context, id string) (*openfgav1.Store, error) {
	return nil, api.StatusErrorf(http.StatusNotImplemented, "not implemented")
}

// ListStores returns a not implemented error, because there is only one store.
func (*openfgaStore) ListStores(ctx context.Context, paginationOptions storage.PaginationOptions) ([]*openfgav1.Store, []byte, error) {
	return nil, nil, api.StatusErrorf(http.StatusNotImplemented, "not implemented")
}

// WriteAssertions returns a not implemented error, because we do not need to use the assertions API.
func (*openfgaStore) WriteAssertions(ctx context.Context, store, modelID string, assertions []*openfgav1.Assertion) error {
	return api.StatusErrorf(http.StatusNotImplemented, "not implemented")
}

// ReadAssertions returns a not implemented error, because we do not need to use the assertions API.
func (*openfgaStore) ReadAssertions(ctx context.Context, store, modelID string) ([]*openfgav1.Assertion, error) {
	return nil, api.StatusErrorf(http.StatusNotImplemented, "not implemented")
}

// ReadChanges returns a not implemented error, because we do not need to use the read changes API.
func (*openfgaStore) ReadChanges(ctx context.Context, store, objectType string, paginationOptions storage.PaginationOptions, horizonOffset time.Duration) ([]*openfgav1.TupleChange, []byte, error) {
	return nil, nil, api.StatusErrorf(http.StatusNotImplemented, "not implemented")
}

// IsReady returns true.
func (*openfgaStore) IsReady(ctx context.Context) (storage.ReadinessStatus, error) {
	return storage.ReadinessStatus{
		Message: "",
		IsReady: true,
	}, nil
}

// Close is a no-op.
func (*openfgaStore) Close() {}
