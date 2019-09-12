package instance

import (
	"fmt"

	"github.com/lxc/lxd/shared/api"
)

// Type indicates the type of instance.
type Type int

const (
	// TypeAny represents any type of instance.
	TypeAny = Type(-1)
	// TypeContainer represents a container instance type.
	TypeContainer = Type(0)
)

// New validates the supplied string against the allowed types of instance and returns the internal
// representation of that type. If empty string is supplied then the type returned is TypeContainer.
// If an invalid name is supplied an error will be returned.
func New(name string) (Type, error) {
	// If "container" or "" is supplied, return type as TypeContainer.
	if api.InstanceType(name) == api.InstanceTypeContainer || name == "" {
		return TypeContainer, nil
	}

	return -1, fmt.Errorf("Invalid instance type")
}

// String converts the internal representation of instance type to a string used in API requests.
// Returns empty string if value is not a valid instance type.
func (instanceType Type) String() string {
	if instanceType == TypeContainer {
		return string(api.InstanceTypeContainer)
	}

	return ""
}
