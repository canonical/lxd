package db

import (
	"fmt"
)

var (
	// ErrNoClusterMember is used to indicate no cluster member has been found for a resource.
	ErrNoClusterMember = fmt.Errorf("No cluster member found")
)
