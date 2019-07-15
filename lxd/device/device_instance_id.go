package device

// instanceIdentifier is an interface that allows us to identify an Instance and its properties.
type instanceIdentifier interface {
	Name() string
	Type() string
	Project() string
}
