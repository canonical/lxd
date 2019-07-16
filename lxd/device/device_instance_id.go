package device

// InstanceIdentifier is an interface that allows us to identify an Instance and its properties.
type InstanceIdentifier interface {
	Name() string
	Type() string
	Project() string
}
