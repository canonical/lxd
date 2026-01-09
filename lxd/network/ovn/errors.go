package ovn

import (
	"errors"

	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
)

// ErrExists indicates that a DB record already exists.
var ErrExists = errors.New("object already exists")

// ErrNotFound indicates that a DB record does not exist.
var ErrNotFound = ovsdbClient.ErrNotFound

// ErrTooMany is returned when one match is expected but multiple are found.
var ErrTooMany = errors.New("too many objects found")
