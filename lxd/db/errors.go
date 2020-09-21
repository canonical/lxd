package db

import (
	"fmt"
)

var (
	// ErrAlreadyDefined hapens when the given entry already exists,
	// for example a container.
	ErrAlreadyDefined = fmt.Errorf("The record already exists")

	// ErrNoSuchObject is in the case of joins (and probably other) queries,
	// we don't get back sql.ErrNoRows when no rows are returned, even though we do
	// on selects without joins. Instead, you can use this error to
	// propagate up and generate proper 404s to the client when something
	// isn't found so we don't abuse sql.ErrNoRows any more than we
	// already do.
	ErrNoSuchObject = fmt.Errorf("No such object")
)
