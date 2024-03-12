package lxd

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/canonical/lxd/shared/api"
)

// GetAuthGroupNames returns a slice of all group names.
func (r *ProtocolLXD) GetAuthGroupNames() ([]string, error) {
	err := r.CheckExtension("access_management")
	if err != nil {
		return nil, err
	}

	urls := []string{}
	baseURL := "auth/groups"
	_, err = r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	return urlsToResourceNames(baseURL, urls...)
}

// GetAuthGroup returns a single group by its name.
func (r *ProtocolLXD) GetAuthGroup(groupName string) (*api.AuthGroup, string, error) {
	err := r.CheckExtension("access_management")
	if err != nil {
		return nil, "", err
	}

	group := api.AuthGroup{}
	etag, err := r.queryStruct(http.MethodGet, api.NewURL().Path("auth", "groups", groupName).String(), nil, "", &group)
	if err != nil {
		return nil, "", err
	}

	return &group, etag, nil
}

// GetAuthGroups returns a list of all groups.
func (r *ProtocolLXD) GetAuthGroups() ([]api.AuthGroup, error) {
	err := r.CheckExtension("access_management")
	if err != nil {
		return nil, err
	}

	var groups []api.AuthGroup
	_, err = r.queryStruct(http.MethodGet, api.NewURL().Path("auth", "groups").WithQuery("recursion", "1").String(), nil, "", &groups)
	if err != nil {
		return nil, err
	}

	return groups, nil
}

// CreateAuthGroup creates a new group.
func (r *ProtocolLXD) CreateAuthGroup(group api.AuthGroupsPost) error {
	err := r.CheckExtension("access_management")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodPost, api.NewURL().Path("auth", "groups").String(), group, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateAuthGroup replaces the editable fields of the group with the given name.
func (r *ProtocolLXD) UpdateAuthGroup(groupName string, groupPut api.AuthGroupPut, ETag string) error {
	err := r.CheckExtension("access_management")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodPut, api.NewURL().Path("auth", "groups", groupName).String(), groupPut, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameAuthGroup renames the group with the given name.
func (r *ProtocolLXD) RenameAuthGroup(groupName string, groupPost api.AuthGroupPost) error {
	err := r.CheckExtension("access_management")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodPost, api.NewURL().Path("auth", "groups", groupName).String(), groupPost, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteAuthGroup deletes the group with the given name.
func (r *ProtocolLXD) DeleteAuthGroup(groupName string) error {
	err := r.CheckExtension("access_management")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodDelete, api.NewURL().Path("auth", "groups", groupName).String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetIdentityAuthenticationMethodsIdentifiers returns a map of authentication method to list of identifiers (e.g. certificate fingerprint, email address)
// for all identities.
func (r *ProtocolLXD) GetIdentityAuthenticationMethodsIdentifiers() (map[string][]string, error) {
	err := r.CheckExtension("access_management")
	if err != nil {
		return nil, err
	}

	urls := []string{}
	baseURL := "auth/identities"
	_, err = r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	authMethodSlashIdentifiers, err := urlsToResourceNames(baseURL, urls...)
	if err != nil {
		return nil, err
	}

	authMethodIdentifiers := make(map[string][]string)
	for _, authMethodSlashIdentifier := range authMethodSlashIdentifiers {
		authMethod, escapedIdentifier, ok := strings.Cut(authMethodSlashIdentifier, "/")
		if !ok {
			return nil, fmt.Errorf("Invalid identity URL suffix %q", authMethodSlashIdentifier)
		}

		identifier, err := url.PathUnescape(escapedIdentifier)
		if err != nil {
			return nil, fmt.Errorf("Failed to unescape identity identifier: %w", err)
		}

		_, ok = authMethodIdentifiers[authMethod]
		if !ok {
			authMethodIdentifiers[authMethod] = []string{identifier}
			continue
		}

		authMethodIdentifiers[authMethod] = append(authMethodIdentifiers[authMethod], identifier)
	}

	return authMethodIdentifiers, nil
}

// GetIdentityIdentifiersByAuthenticationMethod returns a list of identifiers (e.g. certificate fingerprint, email address) of
// identities that authenticate with the given authentication method.
func (r *ProtocolLXD) GetIdentityIdentifiersByAuthenticationMethod(authenticationMethod string) ([]string, error) {
	err := r.CheckExtension("access_management")
	if err != nil {
		return nil, err
	}

	urls := []string{}
	baseURL := fmt.Sprintf("auth/identities/%s", authenticationMethod)
	_, err = r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	return urlsToResourceNames(baseURL, urls...)
}

// GetIdentities returns a list of identities.
func (r *ProtocolLXD) GetIdentities() ([]api.Identity, error) {
	err := r.CheckExtension("access_management")
	if err != nil {
		return nil, err
	}

	var identities []api.Identity
	_, err = r.queryStruct(http.MethodGet, api.NewURL().Path("auth", "identities").WithQuery("recursion", "1").String(), nil, "", &identities)
	if err != nil {
		return nil, err
	}

	return identities, nil
}

// GetIdentitiesByAuthenticationMethod returns a list of identities that authenticate with the given authentication method.
func (r *ProtocolLXD) GetIdentitiesByAuthenticationMethod(authenticationMethod string) ([]api.Identity, error) {
	err := r.CheckExtension("access_management")
	if err != nil {
		return nil, err
	}

	var identities []api.Identity
	_, err = r.queryStruct(http.MethodGet, api.NewURL().Path("auth", "identities", authenticationMethod).WithQuery("recursion", "1").String(), nil, "", &identities)
	if err != nil {
		return nil, err
	}

	return identities, nil
}

// GetIdentity returns the identity with the given authentication method and identifier. A name may be supplied in place
// of the identifier if the name is unique within the authentication method.
func (r *ProtocolLXD) GetIdentity(authenticationMethod string, nameOrIdentifier string) (*api.Identity, string, error) {
	err := r.CheckExtension("access_management")
	if err != nil {
		return nil, "", err
	}

	identity := api.Identity{}
	etag, err := r.queryStruct(http.MethodGet, api.NewURL().Path("auth", "identities", authenticationMethod, nameOrIdentifier).String(), nil, "", &identity)
	if err != nil {
		return nil, "", err
	}

	return &identity, etag, nil
}

// GetCurrentIdentityInfo returns the identity of the requestor. The response includes contextual information that is
// used for authorization.
func (r *ProtocolLXD) GetCurrentIdentityInfo() (*api.IdentityInfo, string, error) {
	err := r.CheckExtension("access_management")
	if err != nil {
		return nil, "", err
	}

	identityInfo := api.IdentityInfo{}
	etag, err := r.queryStruct(http.MethodGet, api.NewURL().Path("auth", "identities", "current").String(), nil, "", &identityInfo)
	if err != nil {
		return nil, "", err
	}

	return &identityInfo, etag, nil
}

// UpdateIdentity replaces the editable fields of an identity with the given input.
func (r *ProtocolLXD) UpdateIdentity(authenticationMethod string, nameOrIdentifer string, identityPut api.IdentityPut, ETag string) error {
	err := r.CheckExtension("access_management")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodPut, api.NewURL().Path("auth", "identities", authenticationMethod, nameOrIdentifer).String(), identityPut, ETag)
	if err != nil {
		return err
	}

	return nil
}

// GetIdentityProviderGroupNames returns a list of identity provider group names.
func (r *ProtocolLXD) GetIdentityProviderGroupNames() ([]string, error) {
	err := r.CheckExtension("access_management")
	if err != nil {
		return nil, err
	}

	urls := []string{}
	baseURL := "auth/identity-provider-groups"
	_, err = r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	return urlsToResourceNames(baseURL, urls...)
}

// GetIdentityProviderGroups returns all identity provider groups defined on the server.
func (r *ProtocolLXD) GetIdentityProviderGroups() ([]api.IdentityProviderGroup, error) {
	err := r.CheckExtension("access_management")
	if err != nil {
		return nil, err
	}

	var idpGroups []api.IdentityProviderGroup
	_, err = r.queryStruct(http.MethodGet, api.NewURL().Path("auth", "identity-provider-groups").WithQuery("recursion", "1").String(), nil, "", &idpGroups)
	if err != nil {
		return nil, err
	}

	return idpGroups, nil
}

// GetIdentityProviderGroup returns the identity provider group with the given name.
func (r *ProtocolLXD) GetIdentityProviderGroup(identityProviderGroupName string) (*api.IdentityProviderGroup, string, error) {
	err := r.CheckExtension("access_management")
	if err != nil {
		return nil, "", err
	}

	idpGroup := api.IdentityProviderGroup{}
	etag, err := r.queryStruct(http.MethodGet, api.NewURL().Path("auth", "identity-provider-groups", identityProviderGroupName).String(), nil, "", &idpGroup)
	if err != nil {
		return nil, "", err
	}

	return &idpGroup, etag, nil
}

// CreateIdentityProviderGroup creates a new identity provider group.
func (r *ProtocolLXD) CreateIdentityProviderGroup(identityProviderGroup api.IdentityProviderGroup) error {
	err := r.CheckExtension("access_management")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodPost, api.NewURL().Path("auth", "identity-provider-groups").String(), identityProviderGroup, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateIdentityProviderGroup replaces the groups that are mapped to the identity provider group with the given name.
func (r *ProtocolLXD) UpdateIdentityProviderGroup(identityProviderGroupName string, identityProviderGroupPut api.IdentityProviderGroupPut, ETag string) error {
	err := r.CheckExtension("access_management")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodPut, api.NewURL().Path("auth", "identity-provider-groups", identityProviderGroupName).String(), identityProviderGroupPut, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameIdentityProviderGroup renames the identity provider group with the given name.
func (r *ProtocolLXD) RenameIdentityProviderGroup(identityProviderGroupName string, identityProviderGroupPost api.IdentityProviderGroupPost) error {
	err := r.CheckExtension("access_management")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodPost, api.NewURL().Path("auth", "identity-provider-groups", identityProviderGroupName).String(), identityProviderGroupPost, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteIdentityProviderGroup deletes the identity provider group with the given name.
func (r *ProtocolLXD) DeleteIdentityProviderGroup(identityProviderGroupName string) error {
	err := r.CheckExtension("access_management")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodDelete, api.NewURL().Path("auth", "identity-provider-groups", identityProviderGroupName).String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetPermissions returns all permissions available on the server. It does not return information on whether these
// permissions are assigned to groups.
func (r *ProtocolLXD) GetPermissions(args GetPermissionsArgs) ([]api.Permission, error) {
	err := r.CheckExtension("access_management")
	if err != nil {
		return nil, err
	}

	u := api.NewURL().Path("auth", "permissions")
	if args.ProjectName != "" {
		u = u.WithQuery("project", args.ProjectName)
	}

	if args.EntityType != "" {
		u = u.WithQuery("entity-type", args.EntityType)
	}

	var permissions []api.Permission
	_, err = r.UseProject("").(*ProtocolLXD).queryStruct(http.MethodGet, u.String(), nil, "", &permissions)
	if err != nil {
		return nil, err
	}

	return permissions, nil
}

// GetPermissionsInfo returns all permissions available on the server and includes the groups that are assigned each permission.
func (r *ProtocolLXD) GetPermissionsInfo(args GetPermissionsArgs) ([]api.PermissionInfo, error) {
	err := r.CheckExtension("access_management")
	if err != nil {
		return nil, err
	}

	u := api.NewURL().Path("auth", "permissions").WithQuery("recursion", "1")
	if args.ProjectName != "" {
		u = u.WithQuery("project", args.ProjectName)
	}

	if args.EntityType != "" {
		u = u.WithQuery("entity-type", args.EntityType)
	}

	var permissions []api.PermissionInfo
	_, err = r.UseProject("").(*ProtocolLXD).queryStruct(http.MethodGet, u.String(), nil, "", &permissions)
	if err != nil {
		return nil, err
	}

	return permissions, nil
}
