package db

import (
	"fmt"
)

var (
	// ErrNoClusterMember is used to indicate no cluster member has been found for a resource.
	ErrNoClusterMember = fmt.Errorf("No cluster member found")

	// ErrAlreadyDefined hapens when the given entry already exists,
	// for example a container.
	ErrAlreadyDefined = fmt.Errorf("The record already exists")
)
