//go:build linux && cgo && !agent

package cluster

import (
	"database/sql"
	"time"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t images.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e image objects version=2
//go:generate mapper stmt -e image objects-by-Project version=2
//go:generate mapper stmt -e image objects-by-Project-and-Cached version=2
//go:generate mapper stmt -e image objects-by-Project-and-Public version=2
//go:generate mapper stmt -e image objects-by-Fingerprint version=2
//go:generate mapper stmt -e image objects-by-Cached version=2
//go:generate mapper stmt -e image objects-by-AutoUpdate version=2
//
//go:generate mapper method -i -e image GetMany version=2
//go:generate mapper method -i -e image GetOne version=2

// Image is a value object holding db-related details about an image.
type Image struct {
	ID           int
	Project      string `db:"primary=yes&join=projects.name"`
	Fingerprint  string `db:"primary=yes"`
	Type         int
	Filename     string
	Size         int64
	Public       bool
	Architecture int
	CreationDate sql.NullTime
	ExpiryDate   sql.NullTime
	UploadDate   time.Time
	Cached       bool
	LastUseDate  sql.NullTime
	AutoUpdate   bool
}

// ImageFilter can be used to filter results yielded by GetImages.
type ImageFilter struct {
	Project     *string
	Fingerprint *string
	Public      *bool
	Cached      *bool
	AutoUpdate  *bool
}
