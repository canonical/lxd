package cluster

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// EntityType is a wrapper for the underlying entityType. Allowing us to implement sql.Scanner and driver.Valuer.
// The zero-value of EntityType (with nil 'entityType') behaves as a "server" entity type. This corresponds with the
// behaviour of the warnings system in that if a warning is created against no specific entity it is considered cluster-wide.
type EntityType struct {
	entityType entityType
}

// entityType is a database representation of an entity type. This type is not exported because it cannot be used
// directly by other packages, this is because we can't implement sql.Scanner or driver.Valuer on an interface type.
// Instead, this type is wrapped by EntityType.
//
// To create a new database entity type, implement this interface and add it to the entityTypes list below.
type entityType interface {
	entity.Type

	// Code must return the entity type code for the entity type that is being implemented.
	Code() int64

	// AllURLsQuery must return a SQL query that returns the information required for generating a unique URL for the entity in a common format.
	// Each row returned by this query must contain the following columns:
	// 1. Entity type. Including the entity type in the result allows for querying multiple entity types at once by performing
	//    a UNION of two or more queries.
	// 2. Entity ID. The caller will likely have an entity type and an ID that they are trying to get a URL for (see warnings
	//    API/table). In other cases the caller may want to list all URLs of a particular type, so returning the ID along with
	//    the URL allows for subsequent mapping or usage.
	// 3. The project name (empty if the entity is not project specific).
	// 4. The location (target) of the entity. Some entities require this parameter for uniqueness (e.g. storage volumes and buckets).
	// 5. Path arguments that comprise the URL of the entity. These are returned as a JSON array in the order that they appear
	//    in the URL.
	AllURLsQuery() string

	// URLByIDQuery must return a SQL query that returns data in the same format as AllURLs. It must accept a bind argument
	// that is the ID of the entity in the database.
	URLByIDQuery() string

	// URLsByProjectQuery must return a SQL query that returns data in the same format as AllURLs. It must accept a bind
	// argument that is the name of the project that contains the entity.
	URLsByProjectQuery() string

	// IDFromURLQuery must return a SQL query that returns the ID of an entity by the information contained in its unique URL in a common format.
	// These queries are not used in isolation, they are used together as part of a larger UNION query.
	// Because of this, all queries expect as arguments the project name, the location, and the path arguments of the URL.
	// Some entity types don't require a project name or location, so that's why they explicitly check for an empty project
	// name or location being passed in.
	// Additionally, all of the queries accept an index number as their first binding argument so that the results can be correlated in
	// the calling function (see PopulateEntityReferencesFromURLs below).
	//
	// TODO: We could avoid a query snippet per entity by making these snippets support multiple entities for a single entity type.
	// (e.g. `WHERE projects.name IN (?, ...) AND instances.name IN (?, ...)` we'd need to be very careful!).
	IDFromURLQuery() string

	// OnDeleteTriggerName must return the name of the trigger that runs when the entity type is deleted.
	OnDeleteTriggerName() string

	// OnDeleteTriggerSQL must return SQL that creates a trigger with name OnDeleteTriggerName that runs when entities of
	// the type are deleted from the database.
	OnDeleteTriggerSQL() string
}

// RequiresProject implements entity.Type for EntityType by calling RequiresProject on the underlying entityType. If the
// underlying entityType is nil, we default to `entityTypeServer`.
func (e EntityType) RequiresProject() bool {
	if e.entityType == nil {
		return entityTypeServer{}.RequiresProject()
	}

	return e.entityType.RequiresProject()
}

// Name implements entity.Type for EntityType by calling Name on the underlying entityType. If the underlying entityType
// is nil, we default to `entityTypeServer`.
func (e EntityType) Name() entity.TypeName {
	if e.entityType == nil {
		return entityTypeServer{}.Name()
	}

	return e.entityType.Name()
}

// PathTemplate implements entity.Type for EntityType by calling PathTemplate on the underlying entityType. If the
// underlying entityType is nil, we default to `entityTypeServer`.
func (e EntityType) PathTemplate() []string {
	if e.entityType == nil {
		return entityTypeServer{}.PathTemplate()
	}

	return e.entityType.PathTemplate()
}

// String implements fmt.Stringer for EntityType by calling String on the underlying entityType. If the underlying
// entityType is nil, we default to `entityTypeServer`.
func (e EntityType) String() string {
	if e.entityType == nil {
		return entityTypeServer{}.String()
	}

	return e.entityType.String()
}

// Code implements entityType for EntityType by calling Code on the underlying entityType. If the underlying entityType
// is nil, we default to `entityTypeServer`.
func (e EntityType) Code() int64 {
	if e.entityType == nil {
		return entityTypeServer{}.Code()
	}

	return e.entityType.Code()
}

// AllURLsQuery implements entityType for EntityType by calling AllURLsQuery on the underlying entityType. If the
// underlying entityType is nil, we default to `entityTypeServer`.
func (e EntityType) AllURLsQuery() string {
	if e.entityType == nil {
		return entityTypeServer{}.AllURLsQuery()
	}

	return e.entityType.AllURLsQuery()
}

// URLByIDQuery implements entityType for EntityType by calling URLByIDQuery on the underlying entityType. If the
// underlying entityType is nil, we default to `entityTypeServer`.
func (e EntityType) URLByIDQuery() string {
	if e.entityType == nil {
		return entityTypeServer{}.URLByIDQuery()
	}

	return e.entityType.URLByIDQuery()
}

// URLsByProjectQuery implements entityType for EntityType by calling URLsByProjectQuery on the underlying entityType.
// If the underlying entityType is nil, we implement the method for 'entityTypeCodeServer'.
func (e EntityType) URLsByProjectQuery() string {
	if e.entityType == nil {
		return entityTypeServer{}.URLsByProjectQuery()
	}

	return e.entityType.URLsByProjectQuery()
}

// IDFromURLQuery implements entityType for EntityType by calling IDFromURLQuery on the underlying entityType. If the
// underlying entityType is nil, we default to `entityTypeServer`.
func (e EntityType) IDFromURLQuery() string {
	if e.entityType == nil {
		return entityTypeServer{}.IDFromURLQuery()
	}

	return e.entityType.IDFromURLQuery()
}

// OnDeleteTriggerName implements entityType for EntityType by calling OnDeleteTriggerName on the underlying entityType.
// If the underlying entityType is nil, we default to `entityTypeServer`.
func (e EntityType) OnDeleteTriggerName() string {
	if e.entityType == nil {
		return entityTypeServer{}.OnDeleteTriggerName()
	}

	return e.entityType.OnDeleteTriggerName()
}

// OnDeleteTriggerSQL  implements entityType for EntityType by calling OnDeleteTriggerSQL  on the underlying entityType.
// If the underlying entityType is nil, we default to `entityTypeServer`.
func (e EntityType) OnDeleteTriggerSQL() string {
	if e.entityType == nil {
		return entityTypeServer{}.OnDeleteTriggerSQL()
	}

	return e.entityType.OnDeleteTriggerSQL()
}

// entityTypes is a list of all entity types.
var entityTypes = []EntityType{
	{entityType: entityTypeContainer{}},
	{entityType: entityTypeImage{}},
	{entityType: entityTypeProfile{}},
	{entityType: entityTypeProject{}},
	{entityType: entityTypeCertificate{}},
	{entityType: entityTypeInstance{}},
	{entityType: entityTypeInstanceBackup{}},
	{entityType: entityTypeInstanceSnapshot{}},
	{entityType: entityTypeNetwork{}},
	{entityType: entityTypeNetworkACL{}},
	{entityType: entityTypeClusterMember{}},
	{entityType: entityTypeOperation{}},
	{entityType: entityTypeStoragePool{}},
	{entityType: entityTypeStorageVolume{}},
	{entityType: entityTypeStorageVolumeBackup{}},
	{entityType: entityTypeStorageVolumeSnapshot{}},
	{entityType: entityTypeWarning{}},
	{entityType: entityTypeClusterGroup{}},
	{entityType: entityTypeStorageBucket{}},
	{entityType: entityTypeNetworkZone{}},
	{entityType: entityTypeImageAlias{}},
	{entityType: entityTypeServer{}},
	{entityType: entityTypeAuthGroup{}},
	{entityType: entityTypeIdentityProviderGroup{}},
	{entityType: entityTypeIdentity{}},
}

// EntityTypeFromName returns the EntityType that has the given entity.TypeName, or an error if none are found.
func EntityTypeFromName(entityTypeName entity.TypeName) (EntityType, error) {
	for _, entityType := range entityTypes {
		if entityTypeName == entityType.Name() {
			return entityType, nil
		}
	}

	return EntityType{}, fmt.Errorf("Missing database entity type definition for entity type %q", entityTypeName)
}

const (
	entityTypeCodeNone                  int64 = -1
	entityTypeCodeContainer             int64 = 0
	entityTypeCodeImage                 int64 = 1
	entityTypeCodeProfile               int64 = 2
	entityTypeCodeProject               int64 = 3
	entityTypeCodeCertificate           int64 = 4
	entityTypeCodeInstance              int64 = 5
	entityTypeCodeInstanceBackup        int64 = 6
	entityTypeCodeInstanceSnapshot      int64 = 7
	entityTypeCodeNetwork               int64 = 8
	entityTypeCodeNetworkACL            int64 = 9
	entityTypeCodeClusterMember         int64 = 10
	entityTypeCodeOperation             int64 = 11
	entityTypeCodeStoragePool           int64 = 12
	entityTypeCodeStorageVolume         int64 = 13
	entityTypeCodeStorageVolumeBackup   int64 = 14
	entityTypeCodeStorageVolumeSnapshot int64 = 15
	entityTypeCodeWarning               int64 = 16
	entityTypeCodeClusterGroup          int64 = 17
	entityTypeCodeStorageBucket         int64 = 18
	entityTypeCodeNetworkZone           int64 = 19
	entityTypeCodeImageAlias            int64 = 20
	entityTypeCodeServer                int64 = 21
	entityTypeCodeAuthGroup             int64 = 22
	entityTypeCodeIdentityProviderGroup int64 = 23
	entityTypeCodeIdentity              int64 = 24
)

// Scan implements sql.Scanner for EntityType. This converts the integer value back into the correct entity.Type
// constant or returns an error.
func (e *EntityType) Scan(value any) error {
	// Always expect null values to be coalesced into entityTypeNone (-1).
	if value == nil {
		return fmt.Errorf("Entity type cannot be null")
	}

	intValue, err := driver.Int32.ConvertValue(value)
	if err != nil {
		return fmt.Errorf("Invalid entity type `%v`: %w", value, err)
	}

	entityTypeInt, ok := intValue.(int64)
	if !ok {
		return fmt.Errorf("Entity should be an integer, got `%v` (%T)", intValue, intValue)
	}

	// If the code is entityTypeCodeNone we set the underlying entityType to the default value of entityTypeServer.
	if entityTypeInt == entityTypeCodeNone {
		e.entityType = entityTypeServer{}
		return nil
	}

	// Iterate through our types, if we find a matching code, set the underlying entityType and return.
	for _, entityType := range entityTypes {
		if entityType.Code() == entityTypeInt {
			e.entityType = entityType.entityType
			return nil
		}
	}

	return fmt.Errorf("Unknown entity type %d", entityTypeInt)
}

// Value implements driver.Valuer for EntityType. This converts the EntityType into an integer or throws an error.
func (e EntityType) Value() (driver.Value, error) {
	return e.Code(), nil
}

// EntityRef represents the expected format of entity URL queries.
type EntityRef struct {
	EntityType  EntityType
	EntityID    int
	ProjectName string
	Location    string
	PathArgs    []string
}

// scan accepts a scanning function (e.g. `(*sql.Row).Scan`) and uses it to parse the row and set its fields.
func (e *EntityRef) scan(scan func(dest ...any) error) error {
	var pathArgs string
	err := scan(&e.EntityType, &e.EntityID, &e.ProjectName, &e.Location, &pathArgs)
	if err != nil {
		return fmt.Errorf("Failed to scan entity URL: %w", err)
	}

	err = json.Unmarshal([]byte(pathArgs), &e.PathArgs)
	if err != nil {
		return fmt.Errorf("Failed to unmarshal entity URL path arguments: %w", err)
	}

	return nil
}

// getURL is a convenience for generating a URL from the EntityRef.
func (e *EntityRef) getURL() (*api.URL, error) {
	u, err := entity.URL(e.EntityType, e.ProjectName, e.Location, e.PathArgs...)
	if err != nil {
		return nil, fmt.Errorf("Failed to create entity URL: %w", err)
	}

	return u, nil
}

// GetEntityURL returns the *api.URL of a single entity by its type and ID.
func GetEntityURL(ctx context.Context, tx *sql.Tx, entityType entity.Type, entityID int) (*api.URL, error) {
	if entity.Equal(entity.TypeServer, entityType) {
		return entity.TypeServer.URL(), nil
	}

	dbEntityType, err := EntityTypeFromName(entityType.Name())
	if err != nil {
		return nil, fmt.Errorf("Could not get entity URL: %w", err)
	}

	stmt := dbEntityType.URLByIDQuery()
	if stmt == "" {
		return nil, fmt.Errorf("Could not get entity URL: No URL from ID statement found for entity type %q", entityType)
	}

	row := tx.QueryRowContext(ctx, stmt, entityID)
	entityRef := &EntityRef{}
	err = entityRef.scan(row.Scan)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("Failed to scan entity URL: %w", err)
	} else if err != nil {
		return nil, api.StatusErrorf(http.StatusNotFound, "No entity found with id `%d` and type %q", entityID, entityType)
	}

	return entityRef.getURL()
}

// GetEntityURLs accepts a project name and a variadic of entity types and returns a map of entity.Type to map of entity ID, to *api.URL.
// This method combines the above queries into a single query using the UNION operator. If no entity types are given, this function will
// return URLs for all entity types. If no project name is given, this function will return URLs for all projects. This may result in
// stupendously large queries, so use with caution!
func GetEntityURLs(ctx context.Context, tx *sql.Tx, projectName string, entityTypesFilter ...entity.Type) (map[entity.TypeName]map[int]*api.URL, error) {
	var stmts []string
	var args []any
	result := make(map[entity.TypeName]map[int]*api.URL)

	entityTypeNames := make([]entity.TypeName, 0, len(entityTypesFilter))
	for _, entityType := range entityTypesFilter {
		entityTypeNames = append(entityTypeNames, entityType.Name())
	}
	// If the server entity type is in the list of entity types, or if we are getting all entity types and
	// not filtering by project, we need to add a server URL to the result. The entity ID of the server entity type is
	// always zero.
	if shared.ValueInSlice(entity.TypeNameServer, entityTypeNames) || (len(entityTypes) == 0 && projectName == "") {
		result[entity.TypeNameServer] = map[int]*api.URL{0: entity.TypeServer.URL()}

		// Return early if there are no other entity types in the list (no queries to execute).
		if len(entityTypesFilter) == 1 {
			return result, nil
		}
	}

	// Collate all the statements we need.
	// If the project is not empty, each statement will need an argument for the project name.
	// Additionally, pre-populate the result map as we know the entity types in advance (this is so that we don't have
	// to check and assign on each loop iteration when scanning rows).
	if len(entityTypesFilter) == 0 && projectName == "" {
		for _, dbEntityType := range entityTypes {
			stmt := dbEntityType.AllURLsQuery()
			if stmt != "" {
				stmts = append(stmts, stmt)
				result[dbEntityType.Name()] = make(map[int]*api.URL)
			}
		}
	} else if len(entityTypesFilter) == 0 && projectName != "" {
		for _, dbEntityType := range entityTypes {
			stmt := dbEntityType.URLsByProjectQuery()
			if stmt != "" {
				stmts = append(stmts, stmt)
				args = append(args, projectName)
				result[dbEntityType.Name()] = make(map[int]*api.URL)
			}
		}
	} else if projectName == "" {
		for _, entityType := range entityTypesFilter {
			// We've already added the server url to the result.
			if entity.Equal(entity.TypeServer, entityType) {
				continue
			}

			dbEntityType, err := EntityTypeFromName(entityType.Name())
			if err != nil {
				return nil, fmt.Errorf("Could not get entity URLs: %w", err)
			}

			stmt := dbEntityType.AllURLsQuery()
			if stmt == "" {
				return nil, fmt.Errorf("Could not get entity URLs: No statement found for entity type %q", entityType)
			}

			stmts = append(stmts, stmt)
			result[entityType.Name()] = make(map[int]*api.URL)
		}
	} else {
		for _, entityType := range entityTypesFilter {
			// We've already added the server url to the result.
			if entity.Equal(entity.TypeServer, entityType) {
				continue
			}

			dbEntityType, err := EntityTypeFromName(entityType.Name())
			if err != nil {
				return nil, fmt.Errorf("Could not get entity URLs: %w", err)
			}

			stmt := dbEntityType.URLsByProjectQuery()
			if stmt == "" {
				return nil, fmt.Errorf("Could not get entity URLs: No statement found for entity type %q", entityType)
			}

			stmts = append(stmts, stmt)
			args = append(args, projectName)
			result[entityType.Name()] = make(map[int]*api.URL)
		}
	}

	// Join into a single statement with UNION and query.
	stmt := strings.Join(stmts, " UNION ")
	rows, err := tx.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("Failed to perform entity URL query: %w", err)
	}

	for rows.Next() {
		entityRef := &EntityRef{}
		err := entityRef.scan(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("Failed to scan entity URL: %w", err)
		}

		u, err := entityRef.getURL()
		if err != nil {
			return nil, err
		}

		result[entityRef.EntityType.Name()][entityRef.EntityID] = u
	}

	return result, nil
}

// PopulateEntityReferencesFromURLs populates the values in the given map with entity references corresponding to the api.URL keys.
// It will return an error if any of the given URLs do not correspond to a LXD entity.
func PopulateEntityReferencesFromURLs(ctx context.Context, tx *sql.Tx, entityURLMap map[*api.URL]*EntityRef) error {
	// If the input list is empty, nothing to do.
	if len(entityURLMap) == 0 {
		return nil
	}

	entityURLs := make([]*api.URL, 0, len(entityURLMap))
	for entityURL := range entityURLMap {
		entityURLs = append(entityURLs, entityURL)
	}

	stmts := make([]string, 0, len(entityURLs))
	var args []any
	for i, entityURL := range entityURLs {
		// Parse the URL to get the majority of the fields of the EntityRef for that URL.
		entityType, projectName, location, pathArgs, err := entity.ParseURL(entityURL.URL)
		if err != nil {
			return fmt.Errorf("Failed to get entity IDs from URLs: %w", err)
		}

		dbEntityType, err := EntityTypeFromName(entityType.Name())
		if err != nil {
			return fmt.Errorf("Failed to get entity IDs from URLs: %w", err)
		}

		// Populate the result map.
		entityURLMap[entityURL] = &EntityRef{
			EntityType:  dbEntityType,
			ProjectName: projectName,
			Location:    location,
			PathArgs:    pathArgs,
		}

		// If the given URL is the server url it is valid but there is no need to perform a query for it, the entity
		// ID of the server is always zero (by virtue of being the zero value for int).
		if entity.Equal(entity.TypeServer, entityType) {
			continue
		}

		// Get the statement corresponding to the entity type.
		stmt := dbEntityType.IDFromURLQuery()
		if stmt == "" {
			return fmt.Errorf("Could not get entity IDs from URLs: No statement found for entity type %q", entityType)
		}

		// Each statement accepts an identifier for the query, the project name, the location, and all path arguments as arguments.
		// In this case we can use the index of the url from the argument slice as an identifier.
		stmts = append(stmts, stmt)
		args = append(args, i, projectName, location)
		for _, pathArg := range pathArgs {
			args = append(args, pathArg)
		}
	}

	// If the only argument was a server URL we don't have any statements to execute.
	if len(stmts) == 0 {
		return nil
	}

	// Join the statements with a union and execute.
	stmt := strings.Join(stmts, " UNION ")
	rows, err := tx.QueryContext(ctx, stmt, args...)
	if err != nil {
		return fmt.Errorf("Failed to get entityIDs from URLS: %w", err)
	}

	for rows.Next() {
		var rowID, entityID int
		err = rows.Scan(&rowID, &entityID)
		if err != nil {
			return fmt.Errorf("Failed to get entityIDs from URLS: %w", err)
		}

		if rowID >= len(entityURLs) {
			return fmt.Errorf("Failed to get entityIDs from URLS: Internal error, returned row ID greater than number of URLs")
		}

		// Using the row ID, get the *api.URL from the argument slice, then use it as a key in our result map to get the *EntityRef.
		entityRef, ok := entityURLMap[entityURLs[rowID]]
		if !ok {
			return fmt.Errorf("Failed to get entityIDs from URLS: Internal error, entity URL missing from result object")
		}

		// Set the value of the EntityID in the *EntityRef.
		entityRef.EntityID = entityID
	}

	err = rows.Err()
	if err != nil {
		return fmt.Errorf("Failed to get entity IDs from URLs: %w", err)
	}

	// Check that all given URLs have been resolved to an ID.
	for u, ref := range entityURLMap {
		if ref.EntityID == 0 && !entity.Equal(entity.TypeServer, ref.EntityType) {
			return fmt.Errorf("Failed to find entity ID for URL %q", u.String())
		}
	}

	return nil
}

// GetEntityReferenceFromURL gets a single EntityRef by parsing the given api.URL and finding the ID of the entity.
// It is used by the OpenFGA datastore implementation to find permissions for the entity with the given URL.
func GetEntityReferenceFromURL(ctx context.Context, tx *sql.Tx, entityURL *api.URL) (*EntityRef, error) {
	// Parse the URL to get the majority of the fields of the EntityRef for that URL.
	entityType, projectName, location, pathArgs, err := entity.ParseURL(entityURL.URL)
	if err != nil {
		return nil, fmt.Errorf("Failed to get entity ID from URL: %w", err)
	}

	dbEntityType, err := EntityTypeFromName(entityType.Name())
	if err != nil {
		return nil, fmt.Errorf("Could not get entity ID from UR: %w", err)
	}

	// Populate the fields we know from the URL.
	entityRef := &EntityRef{
		EntityType:  dbEntityType,
		ProjectName: projectName,
		Location:    location,
		PathArgs:    pathArgs,
	}

	// If the given URL is the server url it is valid but there is no need to perform a query for it, the entity
	// ID of the server is always zero (by virtue of being the zero value for int).
	if entity.Equal(entity.TypeServer, entityType) {
		return entityRef, nil
	}

	// Get the statement corresponding to the entity type.
	stmt := dbEntityType.IDFromURLQuery()
	if stmt == "" {
		return nil, fmt.Errorf("Could not get entity ID from URL: No statement found for entity type %q", entityType)
	}

	// The first bind argument in all entityIDFromURL queries is an index that we use to correspond output of large UNION
	// queries (see PopulateEntityReferencesFromURLs). In this case we are only querying for one ID, so the `0` argument
	// is a placeholder.
	args := []any{0, projectName, location}
	for _, pathArg := range pathArgs {
		args = append(args, pathArg)
	}

	row := tx.QueryRowContext(ctx, stmt, args...)

	var rowID, entityID int
	err = row.Scan(&rowID, &entityID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, api.StatusErrorf(http.StatusNotFound, "No such entity %q", entityURL.String())
		}

		return nil, fmt.Errorf("Failed to get entityID from URL: %w", err)
	}

	entityRef.EntityID = entityID

	return entityRef, nil
}
