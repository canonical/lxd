package api

// Site represents high-level information about a site.
//
// swagger:model
//
// API extension: sites.
type Site struct {
	// The name of the site
	// Example: lxd02
	Name string `json:"name" yaml:"name"`

	// The site endpoint addresses
	// Example: [10.0.0.1:8443, 10.0.0.2:8443]
	Addresses []string `json:"addresses" yaml:"addresses"`

	// The type of the site
	// Example:
	Type int `json:"type" yaml:"type"`

	// Description of the site
	// Example:
	Description string `json:"description" yaml:"description"`
}

// SiteConfigKey represents a single config key.
//
// swagger:model
//
// API extension: sites.
type SiteConfigKey struct {
	// The name of the object requiring this key
	// Example: local
	Name string `json:"name" yaml:"name"`

	// The name of the key
	// Example:
	Key string `json:"key" yaml:"key"`

	// The value on the site
	// Example:
	Value string `json:"value" yaml:"value"`
}

// SitePut represents the fields required to modify a site.
//
// swagger:model
//
// API extension: sites.
type SitePut struct {
	Site `yaml:",inline"`
}

// SitePost represents the fields required to add a site using a join token to establish trust.
//
// swagger:model
//
// API extension: sites.
type SitePost struct {
	// The name of the site
	// Example: site_b
	Name string `json:"name" yaml:"name"`

	// API extension: explicit_trust_token
	TrustToken string `json:"trust_token" yaml:"trust_token"`

	// The name of the created identity for the site
	// Example: tls/site_b
	IdentityName string `json:"identity_name" yaml:"identity_name"`

	// The name of the created authentication group for the site
	// Example: sites
	Group string `json:"identity_group" yaml:"identity_group"`
}
