//go:build linux && cgo && !agent

package openfga

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/openfga/openfga/pkg/storage"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/request"
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

// RequestCache should be set in the request context to allow the OpenFGADatastore implementation to reduce the number
// of database calls that are made on a per-request basis.
type RequestCache struct {
	// initialised is used to load the cache only once per request.
	// This is because the OpenFGA server will call methods on a datastore implementation concurrently.
	initialised atomic.Bool

	// permissionsByEntityType is a cache that allows us to know which groups have a given permission on a given entity.
	// This is used by the ReadUsersetTuples method.
	// The integer key is the entity ID.
	permissionsByEntityType   map[entity.Type]map[auth.Entitlement]map[int][]string
	permissionsByEntityTypeMu sync.RWMutex

	// permissionsByGroup allows to return all the permissions that a group has.
	// This is used by the ReadStartingWithUser method.
	permissionsByGroup   map[string]map[entity.Type]map[auth.Entitlement][]int
	permissionsByGroupMu sync.RWMutex
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
func (o *openfgaStore) Read(ctx context.Context, s string, key *openfgav1.TupleKey, options storage.ReadOptions) (storage.TupleIterator, error) {
	obj := key.GetObject()
	relation := key.GetRelation()
	user := key.GetUser()

	hasObj := obj != ""
	hasRelation := relation != ""
	hasUser := user != ""

	// We always expect the `Object` field to be present.
	if !hasObj {
		return nil, errors.New("Read: Can only list by object")
	}

	// Users are what we are going to enumerate.
	if hasUser {
		return nil, errors.New("Read: Listing by user not supported")
	}

	// Expect the relation to be present.
	if !hasRelation {
		return nil, errors.New("Read: Listing all objects without a relation not supported")
	}

	// Validate the object. We expect the URL to be present.
	entityTypeStr, entityURL, hasURL := strings.Cut(obj, ":")
	if !hasURL {
		return nil, errors.New("Read: Listing all entities of type not supported")
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

	urlEntityType, projectName, location, pathArgs, err := entity.ParseURL(*u)
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

	// We're returning a single relation between a parent and child. Set up the tuple key with the object and relation.
	tupleKey := &openfgav1.TupleKey{
		Object:   obj,
		Relation: relation,
	}

	// Our parent-child relations are always named as the entity type of the parent.
	relationEntityType := entity.Type(relation)
	switch relationEntityType {
	case entity.TypeProject:
		// If the entity type is not project specific but we're looking for project relations then the input is invalid.
		// (Likely an error in the authorization driver).
		if !requiresProject {
			return nil, fmt.Errorf("Received unexpected query, entities of type %q do not have a project relation", entityType)
		}

		// Set the user to relate the object (child) to the user (parent). In this case a parent project.
		tupleKey.User = string(entity.TypeProject) + ":" + entity.ProjectURL(projectName).String()

	case entity.TypeServer:
		// If the entity type is project specific but we're looking for server relations then the input is invalid.
		// (Likely an error in the authorization driver).
		if requiresProject {
			return nil, fmt.Errorf("Received unexpected query, entities of type %q do not have a server relation", entityType)
		}

		// Set the user to relate the object (child) to the user (parent). In this case a parent server.
		tupleKey.User = string(entity.TypeServer) + ":" + entity.ServerURL().String()

	case entity.TypeInstance:
		if !slices.Contains([]entity.Type{entity.TypeInstanceBackup, entity.TypeInstanceSnapshot}, entityType) {
			return nil, fmt.Errorf("Received unexpected query, entities of type %q do not have an instance relation", entityType)
		}

		if len(pathArgs) < 1 {
			return nil, fmt.Errorf("Received invalid entity URL %q with %q parent-child relation", entityURL, relation)
		}

		// Set the user to relate the object (child) to the user (parent). In this case a parent instance.
		tupleKey.User = string(entity.TypeInstance) + ":" + entity.InstanceURL(projectName, pathArgs[0]).String()

	case entity.TypeStorageVolume:
		if !slices.Contains([]entity.Type{entity.TypeStorageVolumeBackup, entity.TypeStorageVolumeSnapshot}, entityType) {
			return nil, fmt.Errorf("Received unexpected query, entities of type %q do not have an instance relation", entityType)
		}

		if len(pathArgs) < 3 {
			return nil, fmt.Errorf("Received invalid entity URL %q with %q parent-child relation", entityURL, relation)
		}

		// Set the user to relate the object (child) to the user (parent). In this case a parent storage volume.
		tupleKey.User = string(entity.TypeStorageVolume) + ":" + entity.StorageVolumeURL(projectName, location, pathArgs[0], pathArgs[1], pathArgs[2]).String()

	default:
		// Return an error if we get an unexpected relation.
		return nil, fmt.Errorf("Relation %q not supported", relation)
	}

	return storage.NewStaticTupleIterator([]*openfgav1.Tuple{{Key: tupleKey}}), nil
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
func (o *openfgaStore) ReadUserTuple(ctx context.Context, store string, tk *openfgav1.TupleKey, options storage.ReadUserTupleOptions) (*openfgav1.Tuple, error) {
	// Expect the User field to be present.
	user := tk.GetUser()
	if user == "" {
		return nil, errors.New("ReadUserTuple: User field of tuple key must be provided")
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

// ensureCacheLoaded is called when we have a non-nil cache in the request context. If the cache is already loaded, it returns early.
// Otherwise, it queries the database for all group permissions and populates the cache.
func (o *openfgaStore) ensureCacheLoaded(ctx context.Context, cache *RequestCache) error {
	// Check if already loaded.
	if cache.initialised.Load() {
		return nil
	}

	// If not loaded, lock the cache and set loaded to true. This should mean that concurrent callers may think the cache
	// is loaded when it isn't yet - but it will be loaded when they are able to acquire a lock.
	cache.permissionsByEntityTypeMu.Lock()
	cache.permissionsByGroupMu.Lock()
	defer cache.permissionsByEntityTypeMu.Unlock()
	defer cache.permissionsByGroupMu.Unlock()
	cache.initialised.Store(true)

	// Get a map of group to slice of permissions.
	var groupPermissions map[string][]cluster.Permission
	err := o.clusterDB.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		groupPermissions, err = cluster.GetGroupPermissions(ctx, tx.Tx())
		return err
	})
	if err != nil {
		return err
	}

	permissionCacheByEntityType := make(map[entity.Type]map[auth.Entitlement]map[int][]string)
	// function to add a permission to permissionCacheByEntityType
	addToPermissionByEntityTypeCache := func(groupName string, permission cluster.Permission) {
		entityType := entity.Type(permission.EntityType)
		entityTypePermissions, ok := permissionCacheByEntityType[entityType]
		if !ok {
			permissionCacheByEntityType[entityType] = map[auth.Entitlement]map[int][]string{permission.Entitlement: {permission.EntityID: {groupName}}}
			return
		}

		entityTypeEntitlementPermissions, ok := entityTypePermissions[permission.Entitlement]
		if !ok {
			entityTypePermissions[permission.Entitlement] = map[int][]string{permission.EntityID: {groupName}}
			return
		}

		entityTypeEntitlementPermissions[permission.EntityID] = append(entityTypeEntitlementPermissions[permission.EntityID], groupName)
	}

	permissionCacheByGroup := make(map[string]map[entity.Type]map[auth.Entitlement][]int)
	// function to add a permission permissionCacheByGroup
	addToPermissionByGroup := func(group string, permission cluster.Permission) {
		entityType := entity.Type(permission.EntityType)
		groupPermissionsForEntityType, ok := permissionCacheByGroup[group][entityType]
		if !ok {
			permissionCacheByGroup[group][entityType] = map[auth.Entitlement][]int{permission.Entitlement: {permission.EntityID}}
			return
		}

		groupPermissionsForEntityType[permission.Entitlement] = append(groupPermissionsForEntityType[permission.Entitlement], permission.EntityID)
	}

	// Iterate over the map of group to slice of permissions and
	// populate our request cache to optimise for ReadUsersetTuples and ReadStartingWithUser.
	for groupName, permissions := range groupPermissions {
		permissionCacheByGroup[groupName] = map[entity.Type]map[auth.Entitlement][]int{}
		for _, permission := range permissions {
			addToPermissionByEntityTypeCache(groupName, permission)
			addToPermissionByGroup(groupName, permission)
		}
	}

	cache.permissionsByEntityType = permissionCacheByEntityType
	cache.permissionsByGroup = permissionCacheByGroup
	return nil
}

// ReadUsersetTuples is called on check requests. It is used to read all the "users" that have a given relation to
// a given object. In this context, the "user" may not be the identity making requests to LXD. In OpenFGA, a "user"
// is any entity that can be related to an object (https://openfga.dev/docs/concepts#what-is-a-user). For example, in
// our model, `project` can be related to `profile` via a `project` relation, so `project:/1.0/projects/default` could
// be considered a user. The opposite is not true, so an `profile` cannot be a user. An `instance` can be related to an
// `instance_snapshot` via the `instance` relation, so an instance can be considered a `user`.
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
func (o *openfgaStore) ReadUsersetTuples(ctx context.Context, store string, filter storage.ReadUsersetTuplesFilter, options storage.ReadUsersetTuplesOptions) (storage.TupleIterator, error) {
	// Expect both an object and a relation.
	if filter.Object == "" || filter.Relation == "" {
		return nil, errors.New("ReadUsersetTuples: Filter must include both an object and a relation")
	}

	// Expect a URL to be present for the object. (E.g. we don't want to list all groups that have `can_view` on
	// all projects, we should be checking for a specific entity).
	entityTypeStr, entityURL, hasURL := strings.Cut(filter.Object, ":")
	if !hasURL {
		return nil, errors.New("ReadUsersetTuples: Listing all entities of type not supported")
	}

	entityType := entity.Type(entityTypeStr)
	err := entityType.Validate()
	if err != nil {
		return nil, fmt.Errorf("ReadUsersetTuples: Invalid object filter %q: %w", filter.Object, err)
	}

	u, err := url.Parse(entityURL)
	if err != nil {
		return nil, fmt.Errorf("ReadUsersetTuples: Failed to parse entity URL %q: %w", entityURL, err)
	}

	// Get cache from context. If it is not present, we'll fall back to calling the database directly.
	cache, err := request.GetContextValue[*RequestCache](ctx, request.CtxOpenFGARequestCache)
	if err != nil {
		groups, err := o.getGroupsWithEntitlementOnEntityWithURL(ctx, auth.Entitlement(filter.Relation), entityType, u)
		if err != nil {
			return nil, fmt.Errorf("ReadUsersetTuples: Failed to get groups with entitlement on entity: %w", err)
		}

		return storage.NewStaticTupleIterator(usersetTuples(filter.Object, filter.Relation, groups)), nil
	}

	err = o.ensureCacheLoaded(ctx, cache)
	if err != nil {
		return nil, fmt.Errorf("ReadUsersetTuples: Failed to ensure that the request cache is loaded: %w", err)
	}

	cache.permissionsByEntityTypeMu.RLock()
	defer cache.permissionsByEntityTypeMu.RUnlock()
	entityTypePermissions, ok := cache.permissionsByEntityType[entityType]
	if !ok {
		// There are no permissions for this entity type.
		return storage.NewStaticTupleIterator(nil), nil
	}

	entityTypeEntitlementPermissions, ok := entityTypePermissions[auth.Entitlement(filter.Relation)]
	if !ok {
		// There are no permissions for this entity type with this entitlement.
		return storage.NewStaticTupleIterator(nil), nil
	}

	// If the entity type is 'Server' we don't need to get an entity reference because there is only one 'Server' entity.
	if entityType == entity.TypeServer {
		groups, ok := entityTypeEntitlementPermissions[0]
		if !ok || len(groups) == 0 {
			// No groups have the permission.
			return storage.NewStaticTupleIterator(nil), nil
		}

		// Return the tuples that relate group members to the given entity with the given relations.
		return storage.NewStaticTupleIterator(usersetTuples(filter.Object, filter.Relation, groups)), nil
	}

	// OpenFGA has given us a filter.Object with the entity type concatenated with the URL of the entity.
	// Our cache has entity IDs. We need to get the ID of the entity from the URL so we can check the cache.
	var entityID int
	entityAPIURL := &api.URL{URL: *u}
	err = o.clusterDB.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		entityRef, err := cluster.GetEntityReferenceFromURL(ctx, tx.Tx(), entityAPIURL)
		if err != nil {
			return err
		}

		entityID = entityRef.EntityID
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("ReadUsersetTuples: Failed to get entity ID from URL: %w", err)
	}

	return storage.NewStaticTupleIterator(usersetTuples(filter.Object, filter.Relation, entityTypeEntitlementPermissions[entityID])), nil
}

// usersetTuples returns a slice of Tuple objects that relate the members of the given groups to an entity via an entitlement.
func usersetTuples(object string, relation string, groupNames []string) []*openfgav1.Tuple {
	tuples := make([]*openfgav1.Tuple, 0, len(groupNames))
	for _, groupName := range groupNames {
		tuples = append(tuples, &openfgav1.Tuple{
			Key: &openfgav1.TupleKey{
				// This is the entity.
				Object: object,
				// This is the entitlement.
				Relation: relation,
				// Members of the group have the permission ("#member"), not the group itself.
				User: string(entity.TypeAuthGroup) + ":" + entity.AuthGroupURL(groupName).String() + "#member",
			},
		})
	}

	return tuples
}

// getGroupsWithEntitlementOnEntityWithURL returns a list of groups with the given permission.
func (o *openfgaStore) getGroupsWithEntitlementOnEntityWithURL(ctx context.Context, entitlement auth.Entitlement, entityType entity.Type, entityURL *url.URL) ([]string, error) {
	var groupNames []string
	err := o.clusterDB.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the ID of the entity.
		entityRef, err := cluster.GetEntityReferenceFromURL(ctx, tx.Tx(), &api.URL{URL: *entityURL})
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
		groupNames, err = query.SelectStrings(ctx, tx.Tx(), q, entitlement, cluster.EntityType(entityType), entityRef.EntityID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		if !api.StatusErrorCheck(err, http.StatusNotFound) {
			// If we have a not found error then there are no tuples to return, but the datastore shouldn't return an error.
			return nil, nil
		}

		return nil, err
	}

	return groupNames, nil
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
// Implementation:
//   - For the first two cases we can perform a simple lookup for entities of the requested type (with project name if project relation).
//   - In the third case, we need to get all permissions with the given entity type and entitlement that are associated with the given group.
//   - For the fourth case we return nil, since we expect direct entitlements for identities to be passed in contextually.
func (o *openfgaStore) ReadStartingWithUser(ctx context.Context, store string, filter storage.ReadStartingWithUserFilter, options storage.ReadStartingWithUserOptions) (storage.TupleIterator, error) {
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

	// Expect that there will be exactly one user filter.
	if len(filter.UserFilter) != 1 {
		return nil, errors.New("ReadStartingWithUser: Unexpected user filter list length")
	}

	// Expect that the user filter object has an entity type and a URL.
	userTypeStr, userURL, ok := strings.Cut(filter.UserFilter[0].GetObject(), ":")
	if !ok {
		return nil, errors.New("ReadStartingWithUser: Must provide user reference")
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

	_, projectName, _, userURLPathArguments, err := entity.ParseURL(*u)
	if err != nil {
		return nil, fmt.Errorf("ReadStartingWithUser: Unexpected user entity URL %q: %w", userURL, err)
	}

	// Our parent-child relations are always named as the entity type of the parent.
	relationEntityType := entity.Type(filter.Relation)

	// If the relation is "project" or "server", we are listing all resources under the project/server.
	if slices.Contains([]entity.Type{entity.TypeProject, entity.TypeServer, entity.TypeInstance, entity.TypeStorageVolume}, relationEntityType) {
		if filter.Relation != string(userEntityType) {
			// Expect that the user entity type is expected for the relation.
			return nil, fmt.Errorf("ReadStartingWithUser: Relation %q is not valid for entities of type %q", filter.Relation, userEntityType)
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
			return nil, fmt.Errorf("ReadStartingWithUser: Failed to get entity URLs: %w", err)
		}

		// Compose the expected tuples relating the server/project to the entities.
		var tuples []*openfgav1.Tuple //nolint:prealloc
		for _, entityURL := range entityURLs[entityType] {
			tupleKey := &openfgav1.TupleKey{Object: string(entityType) + ":" + entityURL.String(), Relation: filter.Relation}
			switch relationEntityType {
			case entity.TypeProject:
				tupleKey.User = string(entity.TypeProject) + ":" + entity.ProjectURL(projectName).String()
			case entity.TypeServer:
				tupleKey.User = string(entity.TypeServer) + ":" + entity.ServerURL().String()
			case entity.TypeInstance:
				_, projectName, _, pathArgs, err := entity.ParseURL(entityURL.URL)
				if err != nil {
					return nil, fmt.Errorf("ReadStartingWithUser: Received invalid URL: %w", err)
				}

				if len(pathArgs) < 1 {
					return nil, fmt.Errorf("Received invalid object URL %q with %q parent-child relation", entityURL, filter.Relation)
				}

				if len(userURLPathArguments) < 1 {
					return nil, fmt.Errorf("Received invalid user URL %q with %q parent-child relation", userURL, filter.Relation)
				}

				if userURLPathArguments[0] != pathArgs[0] {
					// We're returning the parent instance of snapshots or backups here.
					// It's only a parent if it has the same instance name.
					continue
				}

				tupleKey.User = string(entity.TypeInstance) + ":" + entity.InstanceURL(projectName, pathArgs[0]).String()
			case entity.TypeStorageVolume:
				_, projectName, location, pathArgs, err := entity.ParseURL(entityURL.URL)
				if err != nil {
					return nil, fmt.Errorf("ReadStartingWithUser: Received invalid URL: %w", err)
				}

				if len(pathArgs) < 3 {
					return nil, fmt.Errorf("Received invalid object URL %q with %q parent-child relation", entityURL, filter.Relation)
				}

				if len(userURLPathArguments) < 3 {
					return nil, fmt.Errorf("Received invalid user URL %q with %q parent-child relation", userURL, filter.Relation)
				}

				if userURLPathArguments[0] != pathArgs[0] && userURLPathArguments[1] != pathArgs[1] && userURLPathArguments[2] != pathArgs[2] {
					// We're returning the parent storage volume of snapshots or backups here.
					// It's only a parent if it has the same storage pool, volume type, and volume name.
					continue
				}

				tupleKey.User = string(entity.TypeStorageVolume) + ":" + entity.StorageVolumeURL(projectName, location, pathArgs[0], pathArgs[1], pathArgs[2]).String()
			}

			tuples = append(tuples, &openfgav1.Tuple{Key: tupleKey})
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

	groupName := userURLPathArguments[0]
	entitlement := auth.Entitlement(filter.Relation)

	// Get cache from context. If it is not present, we'll fall back to calling the database directly.
	cache, err := request.GetContextValue[*RequestCache](ctx, request.CtxOpenFGARequestCache)
	if err != nil {
		entityURLs, err := o.getEntitiesOfTypeWhereGroupHasEntitlement(ctx, entityType, groupName, entitlement)
		if err != nil {
			return nil, fmt.Errorf("ReadStartingWithUser: Failed to get entities of type %q where group %q has entitlement %q: %w", entityType, groupName, entitlement, err)
		}

		return storage.NewStaticTupleIterator(readStartingWithUserTuples(entityType, entityURLs, entitlement, groupName)), nil
	}

	err = o.ensureCacheLoaded(ctx, cache)
	if err != nil {
		return nil, fmt.Errorf("ReadStartingWithUser: Failed to ensure that the request cache is loaded: %w", err)
	}

	cache.permissionsByGroupMu.RLock()
	defer cache.permissionsByGroupMu.RUnlock()

	groupPermissions, ok := cache.permissionsByGroup[groupName]
	if !ok {
		// The group has no permissions.
		return storage.NewStaticTupleIterator(nil), nil
	}

	groupPermissionsOnEntitiesOfType, ok := groupPermissions[entityType]
	if !ok {
		// The group has no permissions for the given entity type.
		return storage.NewStaticTupleIterator(nil), nil
	}

	if len(groupPermissionsOnEntitiesOfType[entitlement]) == 0 {
		// The group has no permissions for the given entity type and entitlement.
		return storage.NewStaticTupleIterator(nil), nil
	}

	// At this point we have a list of entity IDs that the group has the given entitlement against.
	// We need to get the URLs of these entities so that we can compose tuples to return to the OpenFGA server.
	// We'll use the getPermissionEntityURLs function to do this (which is already optimised).
	permissions := make([]cluster.Permission, 0, len(groupPermissionsOnEntitiesOfType[entitlement]))
	for _, entityID := range groupPermissionsOnEntitiesOfType[entitlement] {
		permissions = append(permissions, cluster.Permission{
			Entitlement: entitlement,
			EntityType:  cluster.EntityType(entityType),
			EntityID:    entityID,
		})
	}

	var validPermissions []cluster.Permission
	var entityURLs map[entity.Type]map[int]*api.URL
	err = o.clusterDB.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		validPermissions, entityURLs, err = cluster.GetPermissionEntityURLs(ctx, tx.Tx(), permissions)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("ReadStartingWithUser: Failed to get entity URLs for permissions: %w", err)
	}

	entityURLsWithPermissions := make([]string, 0, len(validPermissions))
	for _, p := range validPermissions {
		entityURLsWithPermissions = append(entityURLsWithPermissions, entityURLs[entity.Type(p.EntityType)][p.EntityID].String())
	}

	return storage.NewStaticTupleIterator(readStartingWithUserTuples(entityType, entityURLsWithPermissions, entitlement, groupName)), nil
}

// readStartingWithUserTuples returns a slice of Tuple objects that relate the members of a given group to a list of entities of a given type via an entitlement.
func readStartingWithUserTuples(entityType entity.Type, entityURLs []string, entitlement auth.Entitlement, groupName string) []*openfgav1.Tuple {
	// Construct the tuples relating the group to the entities via the expected entitlement.
	tuples := make([]*openfgav1.Tuple, 0, len(entityURLs))
	for _, entityURL := range entityURLs {
		tuples = append(tuples, &openfgav1.Tuple{
			Key: &openfgav1.TupleKey{
				Object:   string(entityType) + ":" + entityURL,
				Relation: string(entitlement),
				// Members of the group have the permission ("#member"), not the group itself.
				User: string(entity.TypeAuthGroup) + ":" + entity.AuthGroupURL(groupName).String() + "#member",
			},
		})
	}

	return tuples
}

// getEntitiesOfTypeWhereGroupHasEntitlement returns a list of entity URLs of the given type where the given group has the given entitlement.
func (o *openfgaStore) getEntitiesOfTypeWhereGroupHasEntitlement(ctx context.Context, entityType entity.Type, groupName string, entitlement auth.Entitlement) ([]string, error) {
	// Construct a query to list permissions with the given entity type and entitlement for the given group.
	q := `
SELECT auth_groups_permissions.entity_type, auth_groups_permissions.entity_id, auth_groups_permissions.entitlement
FROM auth_groups_permissions
JOIN auth_groups ON auth_groups_permissions.auth_group_id = auth_groups.id
WHERE auth_groups_permissions.entitlement = ? AND auth_groups_permissions.entity_type = ? AND auth_groups.name = ?
`
	relation := string(entitlement)
	args := []any{relation, cluster.EntityType(entityType), groupName}

	var entityURLs map[entity.Type]map[int]*api.URL
	var permissions []cluster.Permission
	err := o.clusterDB.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
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
		_, entityURLs, err = cluster.GetPermissionEntityURLs(ctx, tx.Tx(), permissions)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	entityURLStrs := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		entityURLStrs = append(entityURLStrs, entityURLs[entity.Type(permission.EntityType)][permission.EntityID].String())
	}

	return entityURLStrs, nil
}

// ReadPage is not implemented. It is not required for the functionality we need.
func (*openfgaStore) ReadPage(ctx context.Context, store string, tk *openfgav1.TupleKey, opts storage.ReadPageOptions) ([]*openfgav1.Tuple, string, error) {
	return nil, "", api.NewGenericStatusError(http.StatusNotImplemented)
}

// Write is not implemented, we should never be performing writes because we are reading directly from the cluster DB.
func (*openfgaStore) Write(ctx context.Context, store string, d storage.Deletes, w storage.Writes, opts ...storage.TupleWriteOption) error {
	return api.NewGenericStatusError(http.StatusNotImplemented)
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
func (o *openfgaStore) ReadAuthorizationModels(ctx context.Context, store string, options storage.ReadAuthorizationModelsOptions) ([]*openfgav1.AuthorizationModel, string, error) {
	if o.model != nil {
		return []*openfgav1.AuthorizationModel{o.model}, "", nil
	}

	return nil, "", errors.New("Authorization model not set")
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
	return nil, api.NewGenericStatusError(http.StatusNotImplemented)
}

// DeleteStore returns a not implemented error, because there is only one store.
func (*openfgaStore) DeleteStore(ctx context.Context, id string) error {
	return api.NewGenericStatusError(http.StatusNotImplemented)
}

// GetStore returns a not implemented error, because there is only one store.
func (*openfgaStore) GetStore(ctx context.Context, id string) (*openfgav1.Store, error) {
	return nil, api.NewGenericStatusError(http.StatusNotImplemented)
}

// ListStores returns a not implemented error, because there is only one store.
func (*openfgaStore) ListStores(ctx context.Context, paginationOptions storage.ListStoresOptions) ([]*openfgav1.Store, string, error) {
	return nil, "", api.NewGenericStatusError(http.StatusNotImplemented)
}

// WriteAssertions returns a not implemented error, because we do not need to use the assertions API.
func (*openfgaStore) WriteAssertions(ctx context.Context, store, modelID string, assertions []*openfgav1.Assertion) error {
	return api.NewGenericStatusError(http.StatusNotImplemented)
}

// ReadAssertions returns a not implemented error, because we do not need to use the assertions API.
func (*openfgaStore) ReadAssertions(ctx context.Context, store, modelID string) ([]*openfgav1.Assertion, error) {
	return nil, api.NewGenericStatusError(http.StatusNotImplemented)
}

// ReadChanges returns a not implemented error, because we do not need to use the read changes API.
func (*openfgaStore) ReadChanges(ctx context.Context, store string, filter storage.ReadChangesFilter, options storage.ReadChangesOptions) ([]*openfgav1.TupleChange, string, error) {
	return nil, "", api.NewGenericStatusError(http.StatusNotImplemented)
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
