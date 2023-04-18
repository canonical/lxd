package api

// DeploymentsPost represents the fields of a new LXD deployment
//
// swagger:model
//
// API extension: deployments.
type DeploymentsPost struct {
	DeploymentPost `yaml:",inline"`
	DeploymentPut  `yaml:",inline"`
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
	// Deployment configuration map (refer to doc/deployments.md)
	// Example: {"user.mykey": "foo"}
	Config map[string]string `json:"config" yaml:"config"`

	// Description of the deployment
	// Example: My new app and its required services
	Description string `json:"description" yaml:"description"`
}

// Deployment represents a LXD deployment
//
// swagger:model
//
// API extension: deployments.
type Deployment struct {
	DeploymentPost `yaml:",inline"`
	DeploymentPut  `yaml:",inline"`

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

// DeploymentInstanceSetsPost represents the fields required to create a LXD deployment instance set
//
// swagger:model
//
// API extension: deployments.
type DeploymentInstanceSetsPost struct {
	DeploymentInstanceSetPost `yaml:",inline"`
	DeploymentInstanceSetPut  `yaml:",inline"`
}

// DeploymentInstanceSetPost represents the fields required to rename a LXD deployment instance set
//
// swagger:model
//
// API extension: deployments.
type DeploymentInstanceSetPost struct {
	// The name for the deployment instance set
	// Example: myapp
	Name string `json:"name" yaml:"name"`
}

// DeploymentInstanceSetPut represents the modifiable fields of a deployment instance set template
//
// swagger:model
//
// API extension: deployments.
type DeploymentInstanceSetPut struct {
	// Description of the instance set
	// Example: Web servers
	Description string `json:"description" yaml:"description"`

	// Instance definition to use for instances in this set
	InstanceTemplate InstancesPost `json:"instance_template" yaml:"instance_template"`

	// Maximum allowed size of instance set
	ScalingMaximum int `yaml:"scaling_maximum" json:"scaling_maximum"`

	// Minimum allowed size of instance set
	ScalingMinimum int `yaml:"scaling_minimum" json:"scaling_minimum"`
}

// DeploymentInstanceSet represents the fields of a deployment instance set template
//
// swagger:model
//
// API extension: deployments.
type DeploymentInstanceSet struct {
	DeploymentInstanceSetPost `yaml:",inline"`
	DeploymentInstanceSetPut  `yaml:"inline"`

	// Current size of instance set
	ScalingCurrent int `yaml:"scaling_current" json:"scaling_current"`
}

// URL for the deployment instance set.
func (instanceSet *DeploymentInstanceSet) URL(apiVersion string, projectName string, deploymentName string) *URL {
	return NewURL().Path(apiVersion, "deployments", deploymentName, "instance-sets", instanceSet.Name).Project(projectName)
}

// Etag returns the values used for etag generation.
func (instanceSet *DeploymentInstanceSet) Etag() []any {
	return []any{instanceSet.DeploymentInstanceSetPost, instanceSet.DeploymentInstanceSetPut}
}

// Writable converts a full DeploymentInstanceSet struct into a DeploymentInstanceSetPut struct (filters read-only fields).
func (instanceSet *DeploymentInstanceSet) Writable() DeploymentInstanceSetPut {
	return instanceSet.DeploymentInstanceSetPut
}
