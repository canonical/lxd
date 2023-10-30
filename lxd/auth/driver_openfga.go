package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/openfga/go-sdk"
	"github.com/openfga/go-sdk/client"
	"github.com/openfga/go-sdk/credentials"

	"github.com/canonical/lxd/lxd/certificate"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

func WriteOpenFGAAuthorizationModel(ctx context.Context, apiURL string, apiToken string, storeID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	u, err := url.Parse(apiURL)
	if err != nil {
		return "", fmt.Errorf("Failed parsing URL: %w", err)
	}

	conf := client.ClientConfiguration{
		ApiScheme: u.Scheme,
		ApiHost:   u.Host,
		StoreId:   storeID,
		Credentials: &credentials.Credentials{
			Method: credentials.CredentialsMethodApiToken,
			Config: &credentials.Config{
				ApiToken: apiToken,
			},
		},
	}

	fgaClient, err := client.NewSdkClient(&conf)
	if err != nil {
		return "", fmt.Errorf("Failed to create OpenFGA client: %w", err)
	}

	var builtinAuthorizationModel client.ClientWriteAuthorizationModelRequest
	err = json.Unmarshal([]byte(authModel), &builtinAuthorizationModel)
	if err != nil {
		return "", fmt.Errorf("Failed to unmarshal built in authorization model: %w", err)
	}

	data, err := fgaClient.WriteAuthorizationModel(ctx).Body(builtinAuthorizationModel).Execute()
	if err != nil {
		return "", err
	}

	return data.GetAuthorizationModelId(), nil
}

type fga struct {
	commonAuthorizer
	tls *tls

	apiURL      string
	apiToken    string
	storeID     string
	authModelID string

	client *client.OpenFgaClient
}

func (f *fga) configure(opts Opts) error {
	if opts.config == nil {
		return fmt.Errorf("Missing OpenFGA config")
	}

	val, ok := opts.config["openfga.api.token"]
	if !ok || val == nil {
		return fmt.Errorf("Missing OpenFGA API token")
	}

	f.apiToken, ok = val.(string)
	if !ok {
		return fmt.Errorf("Expected a string for configuration key %q, got: %T", "openfga.api.token", val)
	}

	val, ok = opts.config["openfga.api.url"]
	if !ok || val == nil {
		return fmt.Errorf("Missing OpenFGA API URL")
	}

	f.apiURL, ok = val.(string)
	if !ok {
		return fmt.Errorf("Expected a string for configuration key %q, got: %T", "openfga.api.url", val)
	}

	val, ok = opts.config["openfga.store.id"]
	if !ok || val == nil {
		return fmt.Errorf("Missing OpenFGA store ID")
	}

	f.storeID, ok = val.(string)
	if !ok {
		return fmt.Errorf("Expected a string for configuration key %q, got: %T", "openfga.store.id", val)
	}

	val, ok = opts.config["openfga.store.model_id"]
	if !ok || val == nil {
		return fmt.Errorf("Missing OpenFGA authorization model ID")
	}

	f.authModelID, ok = val.(string)
	if !ok {
		return fmt.Errorf("Expected a string for configuration key %q, got: %T", "openfga.store.model_id", val)
	}

	if opts.resources == nil {
		return fmt.Errorf("Missing resources for OpenFGA sync")
	}

	return nil
}

func (f *fga) load(ctx context.Context, certificateCache *certificate.Cache, opts Opts) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := f.configure(opts)
	if err != nil {
		return err
	}

	f.tls = &tls{}
	err = f.tls.load(ctx, certificateCache, opts)
	if err != nil {
		return err
	}

	u, err := url.Parse(f.apiURL)
	if err != nil {
		return fmt.Errorf("Failed parsing URL: %w", err)
	}

	conf := client.ClientConfiguration{
		ApiScheme: u.Scheme,
		ApiHost:   u.Host,
		StoreId:   f.storeID,
		Credentials: &credentials.Credentials{
			Method: credentials.CredentialsMethodApiToken,
			Config: &credentials.Config{
				ApiToken: f.apiToken,
			},
		},
	}

	f.client, err = client.NewSdkClient(&conf)
	if err != nil {
		return fmt.Errorf("Failed to create OpenFGA client: %w", err)
	}

	var builtinAuthorizationModel client.ClientWriteAuthorizationModelRequest

	err = json.Unmarshal([]byte(authModel), &builtinAuthorizationModel)
	if err != nil {
		return err
	}

	// Compare existing model with built-in model
	readModelResponse, err := f.client.ReadAuthorizationModel(ctx).Options(client.ClientReadAuthorizationModelOptions{
		AuthorizationModelId: openfga.PtrString(f.authModelID),
	}).Body(client.ClientReadAuthorizationModelRequest{}).Execute()
	if err != nil {
		return fmt.Errorf("Failed to read pre-existing OpenFGA model: %w", err)
	} else if readModelResponse.AuthorizationModel == nil {
		return fmt.Errorf("Pre-existing authorization model is missing")
	}

	if readModelResponse.AuthorizationModel.SchemaVersion != builtinAuthorizationModel.SchemaVersion {
		return fmt.Errorf("Existing OpenFGA model has schema version %q, but our model has version %q", readModelResponse.AuthorizationModel.SchemaVersion, builtinAuthorizationModel.SchemaVersion)
	}

	existingTypeDefinitions, err := json.Marshal(readModelResponse.AuthorizationModel.TypeDefinitions)
	if err != nil {
		return fmt.Errorf("Failed to compare OpenFGA model type definitions: %w", err)
	}

	builtinTypeDefinitions, err := json.Marshal(builtinAuthorizationModel.TypeDefinitions)
	if err != nil {
		return fmt.Errorf("Failed to compare OpenFGA model type definitions: %w", err)
	}

	if string(existingTypeDefinitions) != string(builtinTypeDefinitions) {
		return fmt.Errorf("Existing OpenFGA model does not equal new model")
	}

	return f.syncResources(ctx, *opts.resources)
}

func (f *fga) CheckPermission(ctx context.Context, r *http.Request, object Object, entitlement Entitlement) error {
	logCtx := logger.Ctx{"object": object, "entitlement": entitlement, "url": r.URL.String(), "method": r.Method}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	details, err := f.requestDetails(r)
	if err != nil {
		return api.StatusErrorf(http.StatusForbidden, "Failed to extract request details: %v", err)
	}

	if details.isInternalOrUnix() {
		return nil
	}

	// Use the TLS driver if the user authenticated with TLS.
	if details.authenticationProtocol() == api.AuthenticationMethodTLS {
		return f.tls.CheckPermission(ctx, r, object, entitlement)
	}

	username := details.username()
	logCtx["username"] = username
	logCtx["protocol"] = details.protocol

	objectUser := ObjectUser(username)
	options := client.ClientCheckOptions{
		AuthorizationModelId: openfga.PtrString(f.authModelID),
	}

	body := client.ClientCheckRequest{
		User:     objectUser.String(),
		Relation: string(entitlement),
		Object:   object.String(),
	}

	f.logger.Debug("Checking OpenFGA relation", logCtx)
	resp, err := f.client.Check(ctx).Options(options).Body(body).Execute()
	if err != nil {
		return fmt.Errorf("Failed to check OpenFGA relation: %w", err)
	}

	if !resp.GetAllowed() {
		return api.StatusErrorf(http.StatusForbidden, "User does not have entitlement %q on object %q", entitlement, object)
	}

	return nil
}

func (f *fga) GetPermissionChecker(ctx context.Context, r *http.Request, entitlement Entitlement, objectType ObjectType) (PermissionChecker, error) {
	allowFunc := func(b bool) func(Object) bool {
		return func(Object) bool {
			return b
		}
	}

	logCtx := logger.Ctx{"object_type": objectType, "entitlement": entitlement, "url": r.URL.String(), "method": r.Method}
	details, err := f.requestDetails(r)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusForbidden, "Failed to extract request details: %v", err)
	}

	if details.isInternalOrUnix() {
		return allowFunc(true), nil
	}

	// Use the TLS driver if the user authenticated with TLS.
	if details.authenticationProtocol() == api.AuthenticationMethodTLS {
		return f.tls.GetPermissionChecker(ctx, r, entitlement, objectType)
	}

	username := details.username()
	logCtx["username"] = username
	logCtx["protocol"] = details.protocol

	options := client.ClientListObjectsOptions{
		AuthorizationModelId: openfga.PtrString(f.authModelID),
	}

	f.logger.Debug("Listing related objects for user", logCtx)
	resp, err := f.client.ListObjects(ctx).Options(options).Body(client.ClientListObjectsRequest{
		User:     ObjectUser(username).String(),
		Relation: string(entitlement),
		Type:     string(objectType),
	}).Execute()
	if err != nil {
		return nil, fmt.Errorf("Failed to OpenFGA objects of type %q with relation %q for user %q: %w", objectType, entitlement, username, err)
	}

	objects := resp.GetObjects()

	return func(object Object) bool {
		return shared.ValueInSlice(object.String(), objects)
	}, nil
}

func (f *fga) AddProject(ctx context.Context, _ int64, projectName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectServer().String(),
			Relation: relationServer,
			Object:   ObjectProject(projectName).String(),
		},
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectProfile(projectName, "default").String(),
		},
	}

	return f.updateTuples(ctx, writes, nil)
}

func (f *fga) DeleteProject(ctx context.Context, _ int64, projectName string) error {
	// Only empty projects can be deleted, so we don't need to worry about any tuples with this project as a parent.
	deletions := []client.ClientTupleKey{
		{
			// Remove the default profile
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectProfile(projectName, "default").String(),
		},
		{
			User:     ObjectServer().String(),
			Relation: relationServer,
			Object:   ObjectProject(projectName).String(),
		},
	}

	return f.updateTuples(ctx, nil, deletions)
}

func (f *fga) RenameProject(ctx context.Context, _ int64, oldName string, newName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectServer().String(),
			Relation: relationServer,
			Object:   ObjectProject(newName).String(),
		},
		{
			User:     ObjectProject(newName).String(),
			Relation: relationProject,
			Object:   ObjectProfile(newName, "default").String(),
		},
	}

	// Only empty projects can be renamed, so we don't need to worry about any tuples with this project as a parent.
	deletions := []client.ClientTupleKey{
		{
			// Remove the default profile
			User:     ObjectProject(oldName).String(),
			Relation: relationProject,
			Object:   ObjectProfile(oldName, "default").String(),
		},
		{
			User:     ObjectServer().String(),
			Relation: relationServer,
			Object:   ObjectProject(oldName).String(),
		},
	}

	return f.updateTuples(ctx, writes, deletions)
}

// AddCertificate is a no-op.
func (f *fga) AddCertificate(ctx context.Context, fingerprint string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectServer().String(),
			Relation: relationServer,
			Object:   ObjectCertificate(fingerprint).String(),
		},
	}

	return f.updateTuples(ctx, writes, nil)
}

// DeleteCertificate is a no-op.
func (f *fga) DeleteCertificate(ctx context.Context, fingerprint string) error {
	deletions := []client.ClientTupleKey{
		{
			User:     ObjectServer().String(),
			Relation: relationServer,
			Object:   ObjectCertificate(fingerprint).String(),
		},
	}

	return f.updateTuples(ctx, nil, deletions)
}

// AddStoragePool is a no-op.
func (f *fga) AddStoragePool(ctx context.Context, storagePoolName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectServer().String(),
			Relation: relationServer,
			Object:   ObjectStoragePool(storagePoolName).String(),
		},
	}

	return f.updateTuples(ctx, writes, nil)
}

// DeleteStoragePool is a no-op.
func (f *fga) DeleteStoragePool(ctx context.Context, storagePoolName string) error {
	deletions := []client.ClientTupleKey{
		{
			User:     ObjectServer().String(),
			Relation: relationServer,
			Object:   ObjectStoragePool(storagePoolName).String(),
		},
	}

	return f.updateTuples(ctx, nil, deletions)
}

// AddImage is a no-op.
func (f *fga) AddImage(ctx context.Context, projectName string, fingerprint string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectImage(projectName, fingerprint).String(),
		},
	}

	return f.updateTuples(ctx, writes, nil)
}

// DeleteImage is a no-op.
func (f *fga) DeleteImage(ctx context.Context, projectName string, fingerprint string) error {
	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectImage(projectName, fingerprint).String(),
		},
	}

	return f.updateTuples(ctx, nil, deletions)
}

// AddImageAlias is a no-op.
func (f *fga) AddImageAlias(ctx context.Context, projectName string, imageAliasName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectImageAlias(projectName, imageAliasName).String(),
		},
	}

	return f.updateTuples(ctx, writes, nil)
}

// DeleteImageAlias is a no-op.
func (f *fga) DeleteImageAlias(ctx context.Context, projectName string, imageAliasName string) error {
	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectImageAlias(projectName, imageAliasName).String(),
		},
	}

	return f.updateTuples(ctx, nil, deletions)
}

// RenameImageAlias is a no-op.
func (f *fga) RenameImageAlias(ctx context.Context, projectName string, oldAliasName string, newAliasName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectImageAlias(projectName, newAliasName).String(),
		},
	}

	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectImageAlias(projectName, oldAliasName).String(),
		},
	}

	return f.updateTuples(ctx, writes, deletions)
}

// AddInstance is a no-op.
func (f *fga) AddInstance(ctx context.Context, projectName string, instanceName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectInstance(projectName, instanceName).String(),
		},
	}

	return f.updateTuples(ctx, writes, nil)
}

// DeleteInstance is a no-op.
func (f *fga) DeleteInstance(ctx context.Context, projectName string, instanceName string) error {
	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectInstance(projectName, instanceName).String(),
		},
	}

	return f.updateTuples(ctx, nil, deletions)
}

// RenameInstance is a no-op.
func (f *fga) RenameInstance(ctx context.Context, projectName string, oldInstanceName string, newInstanceName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectInstance(projectName, newInstanceName).String(),
		},
	}

	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectInstance(projectName, oldInstanceName).String(),
		},
	}

	return f.updateTuples(ctx, writes, deletions)
}

// AddNetwork is a no-op.
func (f *fga) AddNetwork(ctx context.Context, projectName string, networkName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectNetwork(projectName, networkName).String(),
		},
	}

	return f.updateTuples(ctx, writes, nil)
}

// DeleteNetwork is a no-op.
func (f *fga) DeleteNetwork(ctx context.Context, projectName string, networkName string) error {
	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectNetwork(projectName, networkName).String(),
		},
	}

	return f.updateTuples(ctx, nil, deletions)
}

// RenameNetwork is a no-op.
func (f *fga) RenameNetwork(ctx context.Context, projectName string, oldNetworkName string, newNetworkName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectNetwork(projectName, newNetworkName).String(),
		},
	}

	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectNetwork(projectName, oldNetworkName).String(),
		},
	}

	return f.updateTuples(ctx, writes, deletions)
}

// AddNetworkZone is a no-op.
func (f *fga) AddNetworkZone(ctx context.Context, projectName string, networkZoneName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectNetworkZone(projectName, networkZoneName).String(),
		},
	}

	return f.updateTuples(ctx, writes, nil)
}

// DeleteNetworkZone is a no-op.
func (f *fga) DeleteNetworkZone(ctx context.Context, projectName string, networkZoneName string) error {
	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectNetworkZone(projectName, networkZoneName).String(),
		},
	}

	return f.updateTuples(ctx, nil, deletions)
}

// AddNetworkACL is a no-op.
func (f *fga) AddNetworkACL(ctx context.Context, projectName string, networkACLName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectNetworkACL(projectName, networkACLName).String(),
		},
	}

	return f.updateTuples(ctx, writes, nil)
}

// DeleteNetworkACL is a no-op.
func (f *fga) DeleteNetworkACL(ctx context.Context, projectName string, networkACLName string) error {
	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectNetworkACL(projectName, networkACLName).String(),
		},
	}

	return f.updateTuples(ctx, nil, deletions)
}

// RenameNetworkACL is a no-op.
func (f *fga) RenameNetworkACL(ctx context.Context, projectName string, oldNetworkACLName string, newNetworkACLName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectNetworkACL(projectName, newNetworkACLName).String(),
		},
	}

	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectNetworkACL(projectName, oldNetworkACLName).String(),
		},
	}

	return f.updateTuples(ctx, writes, deletions)
}

// AddProfile is a no-op.
func (f *fga) AddProfile(ctx context.Context, projectName string, profileName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectProfile(projectName, profileName).String(),
		},
	}

	return f.updateTuples(ctx, writes, nil)
}

// DeleteProfile is a no-op.
func (f *fga) DeleteProfile(ctx context.Context, projectName string, profileName string) error {
	deletes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectProfile(projectName, profileName).String(),
		},
	}

	return f.updateTuples(ctx, nil, deletes)
}

// RenameProfile is a no-op.
func (f *fga) RenameProfile(ctx context.Context, projectName string, oldProfileName string, newProfileName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectProfile(projectName, newProfileName).String(),
		},
	}

	deletes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectProfile(projectName, oldProfileName).String(),
		},
	}

	return f.updateTuples(ctx, writes, deletes)
}

// AddStoragePoolVolume is a no-op.
func (f *fga) AddStoragePoolVolume(ctx context.Context, projectName string, storagePoolName string, storageVolumeType string, storageVolumeName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectStorageVolume(projectName, storagePoolName, storageVolumeType, storageVolumeName).String(),
		},
	}

	return f.updateTuples(ctx, writes, nil)
}

// DeleteStoragePoolVolume is a no-op.
func (f *fga) DeleteStoragePoolVolume(ctx context.Context, projectName string, storagePoolName string, storageVolumeType string, storageVolumeName string) error {
	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectStorageVolume(projectName, storagePoolName, storageVolumeType, storageVolumeName).String(),
		},
	}

	return f.updateTuples(ctx, nil, deletions)
}

// RenameStoragePoolVolume is a no-op.
func (f *fga) RenameStoragePoolVolume(ctx context.Context, projectName string, storagePoolName string, storageVolumeType string, oldStorageVolumeName string, newStorageVolumeName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectStorageVolume(projectName, storagePoolName, storageVolumeType, newStorageVolumeName).String(),
		},
	}

	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectStorageVolume(projectName, storagePoolName, storageVolumeType, oldStorageVolumeName).String(),
		},
	}

	return f.updateTuples(ctx, writes, deletions)
}

// AddStorageBucket is a no-op.
func (f *fga) AddStorageBucket(ctx context.Context, projectName string, storagePoolName string, storageBucketName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectStorageBucket(projectName, storagePoolName, storageBucketName).String(),
		},
	}

	return f.updateTuples(ctx, writes, nil)
}

// DeleteStorageBucket is a no-op.
func (f *fga) DeleteStorageBucket(ctx context.Context, projectName string, storagePoolName string, storageBucketName string) error {
	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectStorageBucket(projectName, storagePoolName, storageBucketName).String(),
		},
	}

	return f.updateTuples(ctx, nil, deletions)
}

// AddDeployment is a no-op.
func (f *fga) AddDeployment(ctx context.Context, projectName string, deploymentName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectDeployment(projectName, deploymentName).String(),
		},
	}

	return f.updateTuples(ctx, writes, nil)
}

// DeleteDeployment is a no-op.
func (f *fga) DeleteDeployment(ctx context.Context, projectName string, deploymentName string) error {
	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectDeployment(projectName, deploymentName).String(),
		},
	}

	return f.updateTuples(ctx, nil, deletions)
}

// RenameDeployment is a no-op.
func (f *fga) RenameDeployment(ctx context.Context, projectName string, oldDeploymentName string, newDeploymentName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectDeployment(projectName, newDeploymentName).String(),
		},
	}

	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectDeployment(projectName, oldDeploymentName).String(),
		},
	}

	return f.updateTuples(ctx, writes, deletions)
}

// AddDeploymentKey is a no-op.
func (f *fga) AddDeploymentKey(ctx context.Context, projectName string, deploymentName string, deploymentKeyName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectDeploymentKey(projectName, deploymentName, deploymentKeyName).String(),
		},
	}

	return f.updateTuples(ctx, writes, nil)
}

// DeleteDeploymentKey is a no-op.
func (f *fga) DeleteDeploymentKey(ctx context.Context, projectName string, deploymentName string, deploymentKeyName string) error {
	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectDeploymentKey(projectName, deploymentName, deploymentKeyName).String(),
		},
	}

	return f.updateTuples(ctx, nil, deletions)
}

// RenameDeploymentKey is a no-op.
func (f *fga) RenameDeploymentKey(ctx context.Context, projectName string, deploymentName string, oldDeploymentKeyName string, newDeploymentKeyName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectDeploymentKey(projectName, deploymentName, newDeploymentKeyName).String(),
		},
	}

	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectDeploymentKey(projectName, deploymentName, oldDeploymentKeyName).String(),
		},
	}

	return f.updateTuples(ctx, writes, deletions)
}

// AddDeploymentShape is a no-op.
func (f *fga) AddDeploymentShape(ctx context.Context, projectName string, deploymentName string, deploymentShapeName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectDeploymentShape(projectName, deploymentName, deploymentShapeName).String(),
		},
	}

	return f.updateTuples(ctx, writes, nil)
}

// DeleteDeploymentShape is a no-op.
func (f *fga) DeleteDeploymentShape(ctx context.Context, projectName string, deploymentName string, deploymentShapeName string) error {
	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectDeploymentShape(projectName, deploymentName, deploymentShapeName).String(),
		},
	}

	return f.updateTuples(ctx, nil, deletions)
}

// RenameDeploymentShape is a no-op.
func (f *fga) RenameDeploymentShape(ctx context.Context, projectName string, deploymentName string, oldDeploymentShapeName string, newDeploymentShapeName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectDeploymentShape(projectName, deploymentName, newDeploymentShapeName).String(),
		},
	}

	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectDeploymentShape(projectName, deploymentName, oldDeploymentShapeName).String(),
		},
	}

	return f.updateTuples(ctx, writes, deletions)
}

// AddDeploymentShapeInstance is a no-op.
func (f *fga) AddDeploymentShapeInstance(ctx context.Context, projectName string, deploymentName string, deploymentShapeName string, instanceName string) error {
	writes := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectDeploymentShapeInstance(projectName, deploymentName, deploymentShapeName, instanceName).String(),
		},
	}

	return f.updateTuples(ctx, writes, nil)
}

// DeleteDeploymentShapeInstance is a no-op.
func (f *fga) DeleteDeploymentShapeInstance(ctx context.Context, projectName string, deploymentName string, deploymentShapeName string, instanceName string) error {
	deletions := []client.ClientTupleKey{
		{
			User:     ObjectProject(projectName).String(),
			Relation: relationProject,
			Object:   ObjectDeploymentShapeInstance(projectName, deploymentName, deploymentShapeName, instanceName).String(),
		},
	}

	return f.updateTuples(ctx, nil, deletions)
}

func (f *fga) updateTuples(ctx context.Context, writes []client.ClientTupleKey, deletions []client.ClientTupleKey) error {
	if len(writes) == 0 && len(deletions) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	opts := client.ClientWriteOptions{AuthorizationModelId: openfga.PtrString(f.authModelID)}
	body := client.ClientWriteRequest{}
	if writes != nil {
		body.Writes = &writes
	}

	if deletions != nil {
		body.Deletes = &deletions
	}

	clientWriteResponse, err := f.client.Write(ctx).Options(opts).Body(body).Execute()
	if err != nil {
		return fmt.Errorf("Failed to write to OpenFGA store: %w", err)
	}

	for _, write := range clientWriteResponse.Writes {
		if write.Error != nil {
			return fmt.Errorf("Failed to write tuple to OpenFGA store (user: %q; relation: %q; object: %q): %w", write.TupleKey.User, write.TupleKey.Relation, write.TupleKey.Object, err)
		}
	}

	for _, deletion := range clientWriteResponse.Deletes {
		if deletion.Error != nil {
			return fmt.Errorf("Failed to delete tuple from OpenFGA store (user: %q; relation: %q; object: %q): %w", deletion.TupleKey.User, deletion.TupleKey.Relation, deletion.TupleKey.Object, err)
		}
	}

	return nil
}

func (f *fga) projectObjects(ctx context.Context, projectName string) ([]string, error) {
	options := client.ClientListObjectsOptions{
		AuthorizationModelId: openfga.PtrString(f.authModelID),
	}

	objectTypes := []ObjectType{
		ObjectTypeInstance,
		ObjectTypeImage,
		ObjectTypeImageAlias,
		ObjectTypeNetwork,
		ObjectTypeNetworkACL,
		ObjectTypeNetworkZone,
		ObjectTypeProfile,
		ObjectTypeStorageVolume,
		ObjectTypeStorageBucket,
	}

	var allObjects []string
	projectObjectString := ObjectProject(projectName).String()
	for _, objectType := range objectTypes {
		resp, err := f.client.ListObjects(ctx).Options(options).Body(client.ClientListObjectsRequest{
			User:     projectObjectString,
			Relation: relationProject,
			Type:     string(objectType),
		}).Execute()
		if err != nil {
			return nil, err
		}

		allObjects = append(allObjects, resp.GetObjects()...)
	}

	return allObjects, nil
}

func (f *fga) syncResources(ctx context.Context, resources Resources) error {
	var writes []client.ClientTupleKey
	var deletions []client.ClientTupleKey

	// Check if the type-bound public access is set.
	resp, err := f.client.Check(ctx).Options(client.ClientCheckOptions{AuthorizationModelId: openfga.PtrString(f.authModelID)}).Body(client.ClientCheckRequest{
		User:     "user:*",
		Relation: relationUser,
		Object:   ObjectServer().String(),
	}).Execute()
	if err != nil {
		return err
	}

	// If not, set it.
	if !resp.GetAllowed() {
		writes = append(writes, client.ClientTupleKey{
			User:     "user:*",
			Relation: relationUser,
			Object:   ObjectServer().String(),
		})
	}

	// Helper function for diffing local objects with those in OpenFGA. These are appended to the writes and deletions
	// slices as appropriate. If the given relation is relationProject, we need to construct a project object for the
	// "user" field. The project is calculated from the object we are inspecting.
	diffObjects := func(relation string, remoteObjectStrs []string, localObjects []Object) error {
		user := ObjectServer().String()

		for _, localObject := range localObjects {
			if !shared.ValueInSlice(localObject.String(), remoteObjectStrs) {
				if relation == relationProject {
					user = ObjectProject(localObject.Project()).String()
				}

				writes = append(writes, client.ClientTupleKey{
					User:     user,
					Relation: relation,
					Object:   localObject.String(),
				})
			}
		}

		for _, remoteObjectStr := range remoteObjectStrs {
			remoteObject, err := ObjectFromString(remoteObjectStr)
			if err != nil {
				return err
			}

			if !shared.ValueInSlice(remoteObject, resources.CertificateObjects) {
				if relation == relationProject {
					user = ObjectProject(remoteObject.Project()).String()
				}

				deletions = append(deletions, client.ClientTupleKey{
					User:     user,
					Relation: relation,
					Object:   remoteObject.String(),
				})
			}
		}

		return nil
	}

	// List the certificates we have added to OpenFGA already.
	options := client.ClientListObjectsOptions{AuthorizationModelId: openfga.PtrString(f.authModelID)}
	certificatesResp, err := f.client.ListObjects(ctx).Options(options).Body(client.ClientListObjectsRequest{
		User:     ObjectServer().String(),
		Relation: relationServer,
		Type:     string(ObjectTypeCertificate),
	}).Execute()
	if err != nil {
		return err
	}

	// Compare with local certificates.
	err = diffObjects(relationServer, certificatesResp.GetObjects(), resources.CertificateObjects)
	if err != nil {
		return err
	}

	// List the storage pools we have added to OpenFGA already.
	storagePoolsResp, err := f.client.ListObjects(ctx).Options(options).Body(client.ClientListObjectsRequest{
		User:     ObjectServer().String(),
		Relation: relationServer,
		Type:     string(ObjectTypeStoragePool),
	}).Execute()
	if err != nil {
		return err
	}

	// Compare with local storage pools.
	err = diffObjects(relationServer, storagePoolsResp.GetObjects(), resources.StoragePoolObjects)
	if err != nil {
		return err
	}

	// List the projects we have added to OpenFGA already.
	projectsResp, err := f.client.ListObjects(ctx).Options(options).Body(client.ClientListObjectsRequest{
		User:     ObjectServer().String(),
		Relation: relationServer,
		Type:     string(ObjectTypeProject),
	}).Execute()
	if err != nil {
		return err
	}

	// Compare with local projects.
	remoteProjectObjectStrs := projectsResp.GetObjects()
	err = diffObjects(relationServer, remoteProjectObjectStrs, resources.ProjectObjects)
	if err != nil {
		return err
	}

	// Get a slice of project level resources for all projects.
	var remoteProjectResourceObjectStrs []string
	for _, remoteProjectObjectStr := range remoteProjectObjectStrs {
		remoteProjectObject, err := ObjectFromString(remoteProjectObjectStr)
		if err != nil {
			return err
		}

		// project level resources just for this project.
		remoteProjectResources, err := f.projectObjects(ctx, remoteProjectObject.Project())
		if err != nil {
			return err
		}

		remoteProjectResourceObjectStrs = append(remoteProjectResourceObjectStrs, remoteProjectResources...)
	}

	// Compose a slice of all project level objects from the given Resources.
	localProjectObjects := append(resources.ImageObjects, resources.ImageAliasObjects...)
	localProjectObjects = append(localProjectObjects, resources.InstanceObjects...)
	localProjectObjects = append(localProjectObjects, resources.NetworkObjects...)
	localProjectObjects = append(localProjectObjects, resources.NetworkZoneObjects...)
	localProjectObjects = append(localProjectObjects, resources.NetworkACLObjects...)
	localProjectObjects = append(localProjectObjects, resources.ProfileObjects...)
	localProjectObjects = append(localProjectObjects, resources.StoragePoolVolumeObjects...)
	localProjectObjects = append(localProjectObjects, resources.StorageBucketObjects...)
	localProjectObjects = append(localProjectObjects, resources.DeploymentObjects...)
	localProjectObjects = append(localProjectObjects, resources.DeploymentKeyObjects...)
	localProjectObjects = append(localProjectObjects, resources.DeploymentShapeObjects...)
	localProjectObjects = append(localProjectObjects, resources.DeploymentShapeInstanceObjects...)

	// Perform a diff on the project resource objects.
	err = diffObjects(relationProject, remoteProjectResourceObjectStrs, localProjectObjects)
	if err != nil {
		return err
	}

	// Perform any necessary writes and deletions against the OpenFGA server.
	return f.updateTuples(ctx, writes, deletions)
}
