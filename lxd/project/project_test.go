package project_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/shared/api"
)

func ExampleInstance() {
	prefixed := project.Instance(api.ProjectDefaultName, "test")
	fmt.Println(prefixed)

	prefixed = project.Instance("project_name", "test1")
	fmt.Println(prefixed)
	// Output: test
	// project_name_test1
}

func ExampleInstanceParts() {
	projectName, name := project.InstanceParts("unprefixed")
	fmt.Println(projectName, name)

	projectName, name = project.InstanceParts(project.Instance(api.ProjectDefaultName, "test"))
	fmt.Println(projectName, name)

	projectName, name = project.InstanceParts("project_name_test")
	fmt.Println(projectName, name)

	projectName, name = project.InstanceParts(project.Instance("proj", "test1"))
	fmt.Println(projectName, name)

	// Output: default unprefixed
	// default test
	// project_name test
	// proj test1
}

func ExampleStorageVolume() {
	prefixed := project.StorageVolume(api.ProjectDefaultName, "test")
	fmt.Println(prefixed)

	prefixed = project.StorageVolume("project_name", "test1")
	fmt.Println(prefixed)
	// Output: default_test
	// project_name_test1
}

func TestRegistryAllowed(t *testing.T) {
	tests := []struct {
		name            string
		config          map[string]string
		registryName    string
		registryBuiltin bool
		want            bool
	}{
		{
			name:            "unrestricted project allows any registry",
			config:          map[string]string{"restricted": "false"},
			registryName:    "my-registry",
			registryBuiltin: false,
			want:            true,
		},
		{
			name:            "default excludes built-in registries from restriction",
			config:          map[string]string{"restricted": "true"},
			registryName:    "ubuntu",
			registryBuiltin: true,
			want:            true,
		},
		{
			name:            "default blocks custom registries",
			config:          map[string]string{"restricted": "true"},
			registryName:    "my-registry",
			registryBuiltin: false,
			want:            false,
		},
		{
			name:            "allow permits any registry",
			config:          map[string]string{"restricted": "true", "restricted.registries": "allow"},
			registryName:    "my-registry",
			registryBuiltin: false,
			want:            true,
		},
		{
			name:            "block denies built-in registries",
			config:          map[string]string{"restricted": "true", "restricted.registries": "block"},
			registryName:    "ubuntu",
			registryBuiltin: true,
			want:            false,
		},
		{
			name:            "builtin keyword allows built-in registry",
			config:          map[string]string{"restricted": "true", "restricted.registries": "builtin"},
			registryName:    "ubuntu",
			registryBuiltin: true,
			want:            true,
		},
		{
			name:            "builtin keyword blocks custom registry",
			config:          map[string]string{"restricted": "true", "restricted.registries": "builtin"},
			registryName:    "my-registry",
			registryBuiltin: false,
			want:            false,
		},
		{
			name:            "list allows named registry",
			config:          map[string]string{"restricted": "true", "restricted.registries": "my-registry, other"},
			registryName:    "my-registry",
			registryBuiltin: false,
			want:            true,
		},
		{
			name:            "list blocks unnamed registry",
			config:          map[string]string{"restricted": "true", "restricted.registries": "my-registry"},
			registryName:    "other",
			registryBuiltin: false,
			want:            false,
		},
		{
			name:            "list without builtin blocks built-in registry",
			config:          map[string]string{"restricted": "true", "restricted.registries": "my-registry"},
			registryName:    "ubuntu",
			registryBuiltin: true,
			want:            false,
		},
		{
			name:            "builtin combined with list allows built-in registry",
			config:          map[string]string{"restricted": "true", "restricted.registries": "builtin, my-registry"},
			registryName:    "ubuntu",
			registryBuiltin: true,
			want:            true,
		},
		{
			name:            "builtin combined with list allows named custom registry",
			config:          map[string]string{"restricted": "true", "restricted.registries": "builtin, my-registry"},
			registryName:    "my-registry",
			registryBuiltin: false,
			want:            true,
		},
		{
			name:            "builtin combined with list blocks other custom registry",
			config:          map[string]string{"restricted": "true", "restricted.registries": "builtin, my-registry"},
			registryName:    "other",
			registryBuiltin: false,
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := project.RegistryAllowed(tt.config, tt.registryName, tt.registryBuiltin)
			assert.Equal(t, tt.want, got)
		})
	}
}
