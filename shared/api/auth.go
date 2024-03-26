package api

const (
	// AuthenticationMethodTLS is the default authentication method for interacting with LXD remotely.
	AuthenticationMethodTLS = "tls"

	// AuthenticationMethodOIDC is a token based authentication method.
	AuthenticationMethodOIDC = "oidc"
)

const (
	// IdentityTypeCertificateClientRestricted represents identities that authenticate using TLS and are not privileged.
	IdentityTypeCertificateClientRestricted = "Client certificate (restricted)"

	// IdentityTypeCertificateClientUnrestricted represents identities that authenticate using TLS and are privileged.
	IdentityTypeCertificateClientUnrestricted = "Client certificate (unrestricted)"

	// IdentityTypeCertificateServer represents cluster member authentication.
	IdentityTypeCertificateServer = "Server certificate"

	// IdentityTypeCertificateMetricsRestricted represents identities that may only view metrics and are not privileged.
	IdentityTypeCertificateMetricsRestricted = "Metrics certificate (restricted)"

	// IdentityTypeCertificateMetricsUnrestricted represents identities that may only view metrics and are privileged.
	IdentityTypeCertificateMetricsUnrestricted = "Metrics certificate (unrestricted)"

	// IdentityTypeOIDCClient represents an identity that authenticates with OIDC.
	IdentityTypeOIDCClient = "OIDC client"
)

// Identity is the type for an authenticated party that can make requests to the HTTPS API.
//
// swagger:model
//
// API extension: access_management.
type Identity struct {
	// AuthenticationMethod is the authentication method that the identity
	// authenticates to LXD with.
	// Example: tls
	AuthenticationMethod string `json:"authentication_method" yaml:"authentication_method"`

	// Type is the type of identity.
	// Example: oidc-service-account
	Type string `json:"type" yaml:"type"`

	// Identifier is a unique identifier for the identity (e.g. certificate fingerprint or email for OIDC).
	// Example: jane.doe@example.com
	Identifier string `json:"id" yaml:"id"`

	// Name is the Name claim of the identity if authenticated via OIDC, or the name
	// of the certificate if authenticated with TLS.
	// Example: Jane Doe
	Name string `json:"name" yaml:"name"`

	// Groups is the list of groups for which the identity is a member.
	// Example: ["foo", "bar"]
	Groups []string `json:"groups" yaml:"groups"`
}

// Writable converts a Identity struct into a IdentityPut struct (filters read-only fields).
func (i Identity) Writable() IdentityPut {
	return IdentityPut{
		Groups: i.Groups,
	}
}

// SetWritable sets applicable values from IdentityPut struct to Identity struct.
func (i *Identity) SetWritable(put IdentityPut) {
	i.Groups = put.Groups
}

// IdentityInfo expands an Identity to include effective group membership and effective permissions.
// These fields can only be evaluated for the currently authenticated identity.
//
// swagger:model
//
// API extension: access_management.
type IdentityInfo struct {
	Identity `yaml:",inline"`

	// Effective groups is the combined and deduplicated list of LXD groups that the identity is a direct member of, and
	// the LXD groups that the identity is an effective member of via identity provider group mappings.
	// Example: ["foo", "bar"]
	EffectiveGroups []string `json:"effective_groups" yaml:"effective_groups"`

	// Effective permissions is the combined and deduplicated list of permissions that the identity has by virtue of
	// direct membership to a LXD group, or effective membership of a LXD group via identity provider group mappings.
	EffectivePermissions []Permission `json:"effective_permissions" yaml:"effective_permissions"`
}

// IdentityPut contains the editable fields of an IdentityInfo.
//
// swagger:model
//
// API extension: access_management.
type IdentityPut struct {
	// Groups is the list of groups for which the identity is a member.
	// Example: ["foo", "bar"]
	Groups []string `json:"groups" yaml:"groups"`
}

// AuthGroup is the type for a LXD group.
//
// swagger:model
//
// API extension: access_management.
type AuthGroup struct {
	// Name is the name of the group.
	// Example: default-c1-viewers
	Name string `json:"name" yaml:"name"`

	// Description is a short description of the group.
	// Example: Viewers of instance c1 in the default project.
	Description string `json:"description" yaml:"description"`

	// Permissions are a list of permissions.
	Permissions []Permission `json:"permissions" yaml:"permissions"`

	// Identities is a map of authentication method to slice of identity identifiers.
	Identities map[string][]string `json:"identities" yaml:"identities"`

	// IdentityProviderGroups are a list of groups from the IdP whose mapping
	// includes this group.
	// Example: ["sales", "operations"]
	IdentityProviderGroups []string `json:"identity_provider_groups" yaml:"identity_provider_groups"`
}

// Writable converts a AuthGroup struct into a AuthGroupPut struct (filters read-only fields).
func (g AuthGroup) Writable() AuthGroupPut {
	return AuthGroupPut{
		Description: g.Description,
		Permissions: g.Permissions,
	}
}

// SetWritable sets applicable values from AuthGroupPut struct to AuthGroup struct.
func (g *AuthGroup) SetWritable(put AuthGroupPut) {
	g.Description = put.Description
	g.Permissions = put.Permissions
}

// AuthGroupsPost is used for creating a new group.
//
// swagger:model
//
// API extension: access_management.
type AuthGroupsPost struct {
	AuthGroupPost `yaml:",inline"`
	AuthGroupPut  `yaml:",inline"`
}

// AuthGroupPost is used for renaming a group.
//
// swagger:model
//
// API extension: access_management.
type AuthGroupPost struct {
	// Name is the name of the group.
	// Example: default-c1-viewers
	Name string `json:"name" yaml:"name"`
}

// AuthGroupPut contains the editable fields of a group.
//
// swagger:model
//
// API extension: access_management.
type AuthGroupPut struct {
	// Description is a short description of the group.
	// Example: Viewers of instance c1 in the default project.
	Description string `json:"description" yaml:"description"`

	// Permissions are a list of permissions.
	Permissions []Permission `json:"permissions" yaml:"permissions"`
}

// IdentityProviderGroup represents a mapping between LXD groups and groups defined by an identity provider.
//
// swagger:model
//
// API extension: access_management.
type IdentityProviderGroup struct {
	// Name is the name of the IdP group.
	Name string `json:"name" yaml:"name"`

	// Groups are the groups the IdP group resolves to.
	// Example: ["foo", "bar"]
	Groups []string `json:"groups" yaml:"groups"`
}

// Writable converts a IdentityProviderGroup struct into a IdentityProviderGroupPut struct (filters read-only fields).
func (ipg IdentityProviderGroup) Writable() IdentityProviderGroupPut {
	return IdentityProviderGroupPut{
		Groups: ipg.Groups,
	}
}

// SetWritable sets applicable values from IdentityProviderGroupPut struct to IdentityProviderGroup struct.
func (ipg *IdentityProviderGroup) SetWritable(put IdentityProviderGroupPut) {
	ipg.Groups = put.Groups
}

// IdentityProviderGroupPost is used for renaming an IdentityProviderGroup.
//
// swagger:model
//
// API extension: access_management.
type IdentityProviderGroupPost struct {
	// Name is the name of the IdP group.
	Name string `json:"name" yaml:"name"`
}

// IdentityProviderGroupPut contains the editable fields of an IdentityProviderGroup.
//
// swagger:model
//
// API extension: access_management.
type IdentityProviderGroupPut struct {
	// Groups are the groups the IdP group resolves to.
	// Example: ["foo", "bar"]
	Groups []string `json:"groups" yaml:"groups"`
}

// Permission represents a permission that may be granted to a group.
//
// swagger:model
//
// API extension: access_management.
type Permission struct {
	// EntityType is the string representation of the entity type.
	// Example: instance
	EntityType string `json:"entity_type" yaml:"entity_type"`

	// EntityReference is the URL of the entity that the permission applies to.
	// Example: /1.0/instances/c1?project=default
	EntityReference string `json:"url" yaml:"url"`

	// Entitlement is the entitlement define for the entity type.
	// Example: can_view
	Entitlement string `json:"entitlement" yaml:"entitlement"`
}

// PermissionInfo expands a Permission to include any groups that may have the specified Permission.
//
// swagger:model
//
// API extension: access_management.
type PermissionInfo struct {
	Permission `yaml:",inline"`

	// Groups is a list of groups that have the Entitlement on the Entity.
	// Example: ["foo", "bar"]
	Groups []string `json:"groups" yaml:"groups"`
}
