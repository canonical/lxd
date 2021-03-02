package api

// ProjectsPost represents the fields of a new LXD project
//
// swagger:model
//
// API extension: projects
type ProjectsPost struct {
	ProjectPut `yaml:",inline"`

	// The name of the new project
	// Example: foo
	Name string `json:"name" yaml:"name"`
}

// ProjectPost represents the fields required to rename a LXD project
//
// swagger:model
//
// API extension: projects
type ProjectPost struct {
	// The new name for the project
	// Example: bar
	Name string `json:"name" yaml:"name"`
}

// ProjectPut represents the modifiable fields of a LXD project
//
// swagger:model
//
// API extension: projects
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
// API extension: projects
type Project struct {
	ProjectPut `yaml:",inline"`

	// The project name
	// Read only: true
	// Example: foo
	Name string `json:"name" yaml:"name"`

	// List of URLs of objects using this project
	// Read only: true
	// Example: ["/1.0/images/0e60015346f06627f10580d56ac7fffd9ea775f6d4f25987217d5eed94910a20", "/1.0/instances/c1", "/1.0/networks/lxdbr0", "/1.0/profiles/default", "/1.0/storage-pools/default/volumes/custom/blah"]
	UsedBy []string `json:"used_by" yaml:"used_by"`
}

// Writable converts a full Project struct into a ProjectPut struct (filters read-only fields)
//
// API extension: projects
func (project *Project) Writable() ProjectPut {
	return project.ProjectPut
}
