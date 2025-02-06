package api

// ProjectDefaultName is the name of the default project that can never be deleted.
const ProjectDefaultName = "default"

// ProjectsPost represents the fields of a new LXD project
//
// swagger:model
//
// API extension: projects.
type ProjectsPost struct {
	ProjectPut `yaml:",inline"`

	// The name of the new project
	// Example: foo
	Name string `json:"name" yaml:"name"`

	// Add a root disk device using the specified storage pool to the default profile
	// Example: default
	StoragePool string `json:"storage" yaml:"storage"`

	// Add a network device connected to the specified network to the default profile
	// Example: lxdbr0
	Network string `json:"network" yaml:"network"`
}

// ProjectPost represents the fields required to rename a LXD project
//
// swagger:model
//
// API extension: projects.
type ProjectPost struct {
	// The new name for the project
	// Example: bar
	Name string `json:"name" yaml:"name"`
}

// ProjectPut represents the modifiable fields of a LXD project
//
// swagger:model
//
// API extension: projects.
type ProjectPut struct {
	// Project configuration map (refer to doc/projects.md)
	// Example: {"features.profiles": "true", "features.networks": "false"}
	Config map[string]string `json:"config" yaml:"config"`

	// Description of the project
	// Example: My new project
	Description string `json:"description" yaml:"description"`
}

// Project represents a LXD project
//
// swagger:model
//
// API extension: projects.
type Project struct {
	WithEntitlements `yaml:",inline"`

	// The project name
	// Read only: true
	// Example: foo
	Name string `json:"name" yaml:"name"`

	// Description of the project
	// Example: My new project
	Description string `json:"description" yaml:"description"`

	// Project configuration map (refer to doc/projects.md)
	// Example: {"features.profiles": "true", "features.networks": "false"}
	Config map[string]string `json:"config" yaml:"config"`

	// List of URLs of objects using this project
	// Read only: true
	// Example: ["/1.0/images/0e60015346f06627f10580d56ac7fffd9ea775f6d4f25987217d5eed94910a20", "/1.0/instances/c1", "/1.0/networks/lxdbr0", "/1.0/profiles/default", "/1.0/storage-pools/default/volumes/custom/blah"]
	UsedBy []string `json:"used_by" yaml:"used_by"`
}

// Writable converts a full Project struct into a ProjectPut struct (filters read-only fields)
//
// API extension: projects.
func (project *Project) Writable() ProjectPut {
	return ProjectPut{
		Description: project.Description,
		Config:      project.Config,
	}
}

// SetWritable sets applicable values from ProjectPut struct to Project struct.
func (project *Project) SetWritable(put ProjectPut) {
	project.Description = put.Description
	project.Config = put.Config
}

// URL returns the URL for the project.
func (project *Project) URL(apiVersion string) *URL {
	return NewURL().Path(apiVersion, "projects", project.Name)
}

// ProjectState represents the current running state of a LXD project
//
// swagger:model
//
// API extension: project_usage.
type ProjectState struct {
	// Allocated and used resources
	// Read only: true
	// Example: {"containers": {"limit": 10, "usage": 4}, "cpu": {"limit": 20, "usage": 16}}
	Resources map[string]ProjectStateResource `json:"resources" yaml:"resources"`
}

// ProjectStateResource represents the state of a particular resource in a LXD project
//
// swagger:model
//
// API extension: project_usage.
type ProjectStateResource struct {
	// Limit for the resource (-1 if none)
	// Example: 10
	Limit int64

	// Current usage for the resource
	// Example: 4
	Usage int64
}
