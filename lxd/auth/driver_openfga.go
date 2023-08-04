package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	sdk "github.com/openfga/go-sdk"
	"github.com/openfga/go-sdk/credentials"

	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

type openfga struct {
	commonAuthorizer

	apiURL   string
	apiToken string
	storeID  string

	apiClient   *sdk.APIClient
	authModelID string
}

func (o *openfga) validateConfig() error {
	val, ok := o.config["openfga.api.token"]
	if ok {
		o.apiToken = val.(string)
	}

	val, ok = o.config["openfga.api.url"]
	if !ok {
		return fmt.Errorf("Missing OpenFGA API URL")
	}

	o.apiURL = val.(string)

	val, ok = o.config["openfga.store.id"]
	if !ok {
		return fmt.Errorf("Missing OpenFGA store ID")
	}

	o.storeID = val.(string)

	return nil
}

func (o *openfga) load() error {
	err := o.validateConfig()
	if err != nil {
		return err
	}

	u, err := url.Parse(o.apiURL)
	if err != nil {
		return fmt.Errorf("Failed parsing URL: %w", err)
	}

	conf := sdk.Configuration{
		ApiScheme: u.Scheme,
		ApiHost:   u.Host,
		StoreId:   o.storeID,
	}

	if o.apiToken != "" {
		conf.Credentials = &credentials.Credentials{
			Method: credentials.CredentialsMethodApiToken,
			Config: &credentials.Config{
				ApiToken: o.apiToken,
			},
		}
	}

	config, err := sdk.NewConfiguration(conf)
	if err != nil {
		return err
	}

	client := sdk.NewAPIClient(config)

	// Write authorization model
	var body sdk.WriteAuthorizationModelRequest

	err = json.Unmarshal([]byte(authModel), &body)
	if err != nil {
		return err
	}

	data, _, err := client.OpenFgaApi.WriteAuthorizationModel(context.Background()).Body(body).Execute()
	if err != nil {
		return err
	}

	o.authModelID = data.GetAuthorizationModelId()

	return nil
}

func (o *openfga) AddProject(projectID int64, name string) error {
	return nil
}

func (o *openfga) DeleteProject(projectID int64) error {
	return nil
}

func (o *openfga) RenameProject(projectID int64, newName string) error {
	return nil
}

// UserAccess is not used by OpenFGA.
func (o *openfga) UserAccess(username string) (*UserAccess, error) {
	return nil, nil
}

func (o *openfga) UserIsAdmin(r *http.Request) bool {
	return o.UserHasPermission(r, "", GroupObject("admin"), RelationMember)
}

func (o *openfga) UserHasPermission(r *http.Request, _ string, objectName string, relation Relation) bool {
	var user string

	val := r.Context().Value(request.CtxUsername)
	if val != nil {
		user = val.(string)
	}

	fullUser := UserObject(user)

	o.logger.Debug("Checking ReBAC permission", logger.Ctx{"user": fullUser, "relation": relation, "objectName": objectName})

	body := sdk.CheckRequest{
		AuthorizationModelId: sdk.PtrString(o.authModelID),
		TupleKey: sdk.TupleKey{
			User:     sdk.PtrString(fullUser),
			Relation: sdk.PtrString(string(relation)),
			Object:   sdk.PtrString(objectName),
		},
	}

	data, _, err := o.apiClient.OpenFgaApi.Check(context.Background()).Body(body).Execute()
	if err != nil {
		o.logger.Debug("Failed checking permissions", logger.Ctx{"err": err})
		return false
	}

	return data.GetAllowed()
}

func (o *openfga) StopStatusCheck() {}

// ListObjects returns the a list of objects the user is related to. It also returns whether the user is an admin.
func (o *openfga) ListObjects(r *http.Request, relation Relation, objectType ObjectType) ([]string, error) {
	user := ""
	val := r.Context().Value(request.CtxUsername)
	if val != nil {
		user = val.(string)
	}

	fullUser := fmt.Sprintf("user:%s", user)
	body := sdk.NewListObjectsRequest(string(objectType), string(relation), fullUser)

	objectResponse, _, err := o.apiClient.OpenFgaApi.ListObjects(context.Background()).Body(*body).Execute()
	if err != nil {
		return nil, err
	}

	objects := objectResponse.GetObjects()

	// Returned objects will be of the form "<objectName>:<object>".
	// Since we're only interested in the object itself, trim the prefix.
	for i := range objects {
		objects[i] = strings.TrimPrefix(objects[i], fmt.Sprintf("%s:", string(objectType)))
	}

	o.logger.Debug("Checking ReBAC objects", logger.Ctx{"objects": objects, "relation": relation, "objectType": objectType})

	return objects, nil
}

func (o *openfga) AddTuple(user string, relation Relation, object string) error {
	body := sdk.WriteRequest{
		Writes: &sdk.TupleKeys{
			TupleKeys: []sdk.TupleKey{
				{
					User:     sdk.PtrString(user),
					Relation: sdk.PtrString(string(relation)),
					Object:   sdk.PtrString(object),
				},
			},
		},
		AuthorizationModelId: sdk.PtrString(o.authModelID)}

	_, _, err := o.apiClient.OpenFgaApi.Write(context.Background()).Body(body).Execute()
	if err != nil && !isWriteFailedDueToInvalidInputError(err) {
		return err
	}

	return nil
}

func (o *openfga) DeleteTuple(user string, relation Relation, object string) error {
	body := sdk.WriteRequest{
		Deletes: &sdk.TupleKeys{
			TupleKeys: []sdk.TupleKey{
				{
					User:     sdk.PtrString(user),
					Relation: sdk.PtrString(string(relation)),
					Object:   sdk.PtrString(object),
				},
			},
		},
		AuthorizationModelId: sdk.PtrString(o.authModelID)}

	_, _, err := o.apiClient.OpenFgaApi.Write(context.Background()).Body(body).Execute()
	if err != nil && !isWriteFailedDueToInvalidInputError(err) {
		return err
	}

	return nil
}

func (o *openfga) GetPermissionChecker(r *http.Request, relation Relation, objectType ObjectType) (func(object string) bool, error) {
	allowedObjects, err := o.ListObjects(r, relation, objectType)
	if err != nil {
		return nil, err
	}

	userIsAdmin := o.UserIsAdmin(r)

	return func(object string) bool {
		// If the object argument contains the object type, i.e. <objectType>:<objectName>, strip the type as that part isn't needed for the permission check.
		if strings.Contains(object, ":") {
			object = strings.SplitN(object, ":", 2)[1]
		}

		return userIsAdmin || shared.StringInSlice(object, allowedObjects)
	}, nil
}

// isWriteFailedDueToInvalidInputError checks if the error is an OpenFGA invalid write input error.
// This error is returned by OpenFGA if an already existing tuple is trying to be added again, or if
// a non-existing tuple is trying to be removed.
func isWriteFailedDueToInvalidInputError(err error) bool {
	var validationError sdk.FgaApiValidationError

	if errors.As(err, &validationError) {
		return validationError.ResponseCode() == sdk.WRITE_FAILED_DUE_TO_INVALID_INPUT
	}

	return false
}
