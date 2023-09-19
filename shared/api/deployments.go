package api

// DeploymentsPost represents the fields of a new LXD deployment
//
// swagger:model
//
// API extension: deployments.
type DeploymentsPost struct {
	DeploymentPost `json:",inline" yaml:",inline"`
	DeploymentPut  `json:",inline" yaml:",inline"`
}

// DeploymentPost represents the fields required to rename a LXD deployment
//
// swagger:model
//
// API extension: deployments.
type DeploymentPost struct {
	// The name for the deployment
	// Example: myapp
	Name string `json:"name" yaml:"name"`
}

// DeploymentPut represents the modifiable fields of a LXD deployment
//
// swagger:model
//
// API extension: deployments.
type DeploymentPut struct {
	// Description of the deployment
	// Example: My new app and its required services
	Description string `json:"description" yaml:"description"`

	// Deployment configuration map (refer to doc/deployments.md)
	// Example: {"user.mykey": "foo"}
	Config map[string]string `json:"config" yaml:"config"`

	// Governor webhook URL for provider triggered requests
	// Example: https://n.n.n.n/scale
	GovernorWebhookURL string `json:"governor_webhook_url" yaml:"governor_webhook_url"`
}

// Deployment used for displaying a LXD Deployment
//
// swagger:model
//
// API extension: deployments.
type Deployment struct {
	DeploymentPost `json:",inline" yaml:",inline"`
	DeploymentPut  `json:",inline" yaml:",inline"`

	// List of URLs of objects using this deployment
	// Read only: true
	// Example: ["/1.0/instances/c1", "/1.0/instances/c2"]
	UsedBy []string `json:"used_by" yaml:"used_by"`
}

// URL for the deployment.
func (deployment *Deployment) URL(apiVersion string, projectName string) *URL {
	return NewURL().Path(apiVersion, "deployments", deployment.Name).Project(projectName)
}

// Etag returns the values used for etag generation.
func (deployment *Deployment) Etag() []any {
	return []any{deployment.Name, deployment.DeploymentPut}
}

// Writable converts a full Deployment struct into a DeploymentPut struct (filters read-only fields).
func (deployment *Deployment) Writable() DeploymentPut {
	return deployment.DeploymentPut
}

// DeploymentKeysPost represents the fields of a new LXD deployment key.
//
// swagger:model
//
// API extension: deployments.
type DeploymentKeysPost struct {
	DeploymentKeyPost `json:",inline" yaml:",inline"`
	DeploymentKeyPut  `json:",inline" yaml:",inline"`

	// The certificate fingerprint associated to this deployment key used for verification
	CertificateFingerprint string `json:"certificate_fingerprint" yaml:"certificate_fingerprint"`
}

// DeploymentKeyPost represents the fields required to rename a LXD deployment key
//
// swagger:model
//
// API extension: deployments.
type DeploymentKeyPost struct {
	// The name for the deployment key
	// Example: myapp-key1
	Name string `json:"name" yaml:"name"`
}

// DeploymentKeyPut represents the modifiable fields of a LXD deployment key
//
// swagger:model
//
// API extension: deployments.
type DeploymentKeyPut struct {
	// Description of the deployment key
	// Example: Deployment key for myapp
	Description string `json:"description" yaml:"description"`

	// The role for a deployment key.
	// It could be either "admin" or "read-only"
	Role string `json:"role" yaml:"role"`
}

// DeploymentKey is used for displaying a LXD Deployment Key
//
// swagger:model
//
// API extension: deployments.
type DeploymentKey struct {
	DeploymentKeyPost `json:",inline" yaml:",inline"`
	DeploymentKeyPut  `json:",inline" yaml:",inline"`

	// The certificate fingerprint associated to this deployment key used for verification
	CertificateFingerprint string `json:"certificate_fingerprint" yaml:"certificate_fingerprint"`
}

// URL for the deployment key.
func (dk *DeploymentKey) URL(apiVersion string, projectName string, deploymentName string) *URL {
	return NewURL().Path(apiVersion, "deployments", deploymentName, "keys", dk.Name).Project(projectName)
}

// Etag returns the values used for etag generation.
func (dk *DeploymentKey) Etag() []any {
	return []any{dk.Name, dk.DeploymentKeyPut}
}

// Writable converts a full DeploymentKey struct into a DeploymentKeyPut struct (filters read-only fields).
func (dk *DeploymentKey) Writable() DeploymentKeyPut {
	return dk.DeploymentKeyPut
}

// DeploymentShapesPost represents the fields required to create a LXD deployment shape
//
// swagger:model
//
// API extension: deployments.
type DeploymentShapesPost struct {
	DeploymentShapePost `json:",inline" yaml:",inline"`
	DeploymentShapePut  `json:",inline" yaml:",inline"`
}

// DeploymentShapePost represents the fields required to rename a LXD deployment shape
//
// swagger:model
//
// API extension: deployments.
type DeploymentShapePost struct {
	// The name for the deployment shape
	// Example: myapp
	Name string `json:"name" yaml:"name"`
}

// DeploymentShapePut represents the modifiable fields of a deployment shape template
//
// swagger:model
//
// API extension: deployments.
type DeploymentShapePut struct {
	// Description of the deployment shape
	// Example: Web servers
	Description string `json:"description" yaml:"description"`

	// DeploymentShape configuration map
	// Example: {"user.mykey": "foo"}
	Config map[string]string `json:"config" yaml:"config"`

	// Instance definition to use for instances in this set
	InstanceTemplate InstancesPost `json:"instance_template" yaml:"instance_template"`

	// Maximum allowed size of instance set
	ScalingMaximum int `yaml:"scaling_maximum" json:"scaling_maximum"`

	// Minimum allowed size of instance set
	ScalingMinimum int `yaml:"scaling_minimum" json:"scaling_minimum"`
}

// DeploymentShape represents the fields of a deployment shape template
//
// swagger:model
//
// API extension: deployments.
type DeploymentShape struct {
	DeploymentShapePost `json:",inline" yaml:",inline"`
	DeploymentShapePut  `json:",inline" yaml:",inline"`

	// Current size of instance set
	ScalingCurrent int `yaml:"scaling_current" json:"scaling_current"`
}

// URL for the deployment shape.
func (ds *DeploymentShape) URL(apiVersion string, projectName string, deploymentName string) *URL {
	return NewURL().Path(apiVersion, "deployments", deploymentName, "shapes", ds.Name).Project(projectName)
}

// Etag returns the values used for etag generation.
func (ds *DeploymentShape) Etag() []any {
	return []any{ds.DeploymentShapePost, ds.DeploymentShapePut}
}

// Writable converts a full DeploymentShape struct into a DeploymentShapePut struct (filters read-only fields).
func (ds *DeploymentShape) Writable() DeploymentShapePut {
	return ds.DeploymentShapePut
}

// DeploymentInstancesPost represents the fields required to create an instance in an existing LXD deployment.
//
// swagger:model
//
// API extension: deployments.
type DeploymentInstancesPost struct {
	// The deployment shape to use to create the instance
	// Example: k8s-kubelet
	ShapeName string `json:"deployment_shape_name" yaml:"deployment_shape_name"`

	// The instance name to use
	// Example: k8s-kubelet01
	InstanceName string `json:"instance_name" yaml:"instance_name"`
}
