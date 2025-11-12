package api

// DevLXDOperation is a devLXD representation of LXD background operation.
type DevLXDOperation struct {
	// UUID of the operation
	// Example: 6916c8a6-9b7d-4abd-90b3-aedfec7ec7da
	ID string `json:"id" yaml:"id"`

	// Status name
	// Example: Running
	Status string `json:"status" yaml:"status"`

	// Status code
	// Example: 103
	StatusCode StatusCode `json:"status_code" yaml:"status_code"`

	// Operation error message
	// Example: Some error message
	Err string `json:"err" yaml:"err"`
}
