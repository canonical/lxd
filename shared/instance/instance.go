package instance

// Type indicates the type of instance.
type Type int

const (
	// TypeAny represents any type of instance.
	TypeAny = Type(-1)
	// TypeContainer represents a container instance type.
	TypeContainer = Type(0)
)
