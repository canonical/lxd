package instancetype

import (
	"fmt"

	"github.com/lxc/lxd/shared/api"
)

// Type indicates the type of instance.
type Type int

const (
	// Any represents any type of instance.
	Any = Type(-1)

	// Container represents a container instance type.
	Container = Type(0)

	// VM represents a virtual-machine instance type.
	VM = Type(1)
)

//GetInstanceTypes returns slice of known Instance Types
func GetInstanceTypes() []Type {
	return []Type{
		Any, Container, VM,
	}
}

// New validates the supplied string against the allowed types of instance and returns the internal
// representation of that type. If empty string is supplied then the type returned is TypeContainer.
// If an invalid name is supplied an error will be returned.
func New(name string) (Type, error) {
	// If "container" or "" is supplied, return type as Container.
	if api.InstanceType(name) == api.InstanceTypeContainer || name == "" {
		return Container, nil
	}

	// If "virtual-machine" is supplied, return type as VM.
	if api.InstanceType(name) == api.InstanceTypeVM {
		return VM, nil
	}

	return -1, fmt.Errorf("Invalid instance type")
}

// String converts the internal representation of instance type to a string used in API requests.
// Returns empty string if value is not a valid instance type.
func (instanceType Type) String() string {
	if instanceType == Container {
		return string(api.InstanceTypeContainer)
	}

	if instanceType == VM {
		return string(api.InstanceTypeVM)
	}

	return ""
}
