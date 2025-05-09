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
//go:generate mapper stmt -e image objects
//go:generate mapper stmt -e image objects-by-ID
//go:generate mapper stmt -e image objects-by-Project
//go:generate mapper stmt -e image objects-by-Project-and-Cached
//go:generate mapper stmt -e image objects-by-Project-and-Public
//go:generate mapper stmt -e image objects-by-Fingerprint
//go:generate mapper stmt -e image objects-by-Cached
//go:generate mapper stmt -e image objects-by-AutoUpdate
//
//go:generate mapper method -i -e image GetMany
//go:generate mapper method -i -e image GetOne
//go:generate goimports -w images.mapper.go
//go:generate goimports -w images.interface.mapper.go

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
	ID          *int
	Project     *string
	Fingerprint *string
	Public      *bool
	Cached      *bool
	AutoUpdate  *bool
}

// ImageSourceProtocol maps image source protocol codes to human-readable names.
var ImageSourceProtocol = map[int]string{
	0: "lxd",
	1: "direct",
	2: "simplestreams",
}

// GetImageSource returns the image source with the given ID.
func GetImageSource(ctx context.Context, tx *sql.Tx, imageID int) (int, api.ImageSource, error) {
	q := `SELECT id, server, protocol, certificate, alias FROM images_source WHERE image_id=?`
	type imagesSource struct {
		ID          int
		Server      string
		Protocol    int
		Certificate string
		Alias       string
	}

	sources := []imagesSource{}
	err := query.Scan(ctx, tx, q, func(scan func(dest ...any) error) error {
		s := imagesSource{}

		err := scan(&s.ID, &s.Server, &s.Protocol, &s.Certificate, &s.Alias)
		if err != nil {
			return err
		}

		sources = append(sources, s)

		return nil
	}, imageID)
	if err != nil {
		return -1, api.ImageSource{}, err
	}

	if len(sources) == 0 {
		return -1, api.ImageSource{}, api.StatusErrorf(http.StatusNotFound, "Image source not found")
	}

	source := sources[0]

	protocol, found := ImageSourceProtocol[source.Protocol]
	if !found {
		return -1, api.ImageSource{}, fmt.Errorf("Invalid protocol: %d", source.Protocol)
	}

	result := api.ImageSource{
		Server:      source.Server,
		Protocol:    protocol,
		Certificate: source.Certificate,
		Alias:       source.Alias,
	}

	return source.ID, result, nil
}
