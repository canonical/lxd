//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/osarch"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t images.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e image objects
//go:generate mapper stmt -e image objects-by-ID
//go:generate mapper stmt -e image objects-by-Project
//go:generate mapper stmt -e image objects-by-Project-and-Fingerprint
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

// ToAPI converts the Image to an api.Image, making extra database queries as necessary.
// If the profileProject is non-empty then its used to populate the image's profiles using the effective profile project.
func (img *Image) ToAPI(ctx context.Context, tx *sql.Tx, profileProject string) (*api.Image, error) {
	var err error

	// Initialise API image struct.
	image := api.Image{
		Fingerprint: img.Fingerprint,
		Filename:    img.Filename,
		Size:        img.Size,
		Cached:      img.Cached,
		Public:      img.Public,
		AutoUpdate:  img.AutoUpdate,
		Project:     img.Project,
		CreatedAt:   img.CreationDate.Time,
		ExpiresAt:   img.ExpiryDate.Time,
		LastUsedAt:  img.LastUseDate.Time,
		UploadedAt:  img.UploadDate,
		Type:        instancetype.Type(img.Type).String(),
	}

	// Add architecture.
	image.Architecture, _ = osarch.ArchitectureName(img.Architecture)

	// Add properties.
	image.Properties, err = query.SelectConfig(ctx, tx, "images_properties", "image_id=?", img.ID)
	if err != nil {
		return nil, err
	}

	// Add aliases.
	image.Aliases = make([]api.ImageAlias, 0)

	{
		q := "SELECT name, description FROM images_aliases WHERE image_id=?"
		err = query.Scan(ctx, tx, q, func(scan func(dest ...any) error) error {
			alias := api.ImageAlias{}

			err := scan(&alias.Name, &alias.Description)
			if err != nil {
				return err
			}

			image.Aliases = append(image.Aliases, alias)
			return nil
		}, img.ID)
		if err != nil {
			return nil, err
		}
	}

	// Add source info.
	_, source, err := GetImageSource(ctx, tx, img.ID)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil, err
	} else if err == nil {
		// Only populate UpdateSource if image source found.
		image.UpdateSource = source
		image.UpdateSource.ImageType = image.Type
	}

	// Get effective project profiles.
	if profileProject != "" {
		enabled, err := ProjectHasProfiles(context.Background(), tx, profileProject)
		if err != nil {
			return nil, err
		}

		if !enabled {
			profileProject = api.ProjectDefaultName
		}

		// Get the profiles
		image.Profiles = make([]string, 0)

		q := `
		SELECT profiles.name FROM profiles
		JOIN images_profiles ON images_profiles.profile_id = profiles.id
		JOIN projects ON profiles.project_id = projects.id
		WHERE images_profiles.image_id = ? AND projects.name = ?`

		err = query.Scan(ctx, tx, q, func(scan func(dest ...any) error) error {
			var profileName string
			err := scan(&profileName)
			if err != nil {
				return err
			}

			image.Profiles = append(image.Profiles, profileName)
			return nil
		}, img.ID, profileProject)
		if err != nil {
			return nil, err
		}
	}

	return &image, nil
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
	1: "direct", // Deprecated: kept for backward compatibility with existing data.
	2: "simplestreams",
}

// imageSourceProtocolCode maps image source protocol names to database codes.
var imageSourceProtocolCode = map[string]int{
	"lxd":           0,
	"direct":        1, // Deprecated: kept for backward compatibility with existing data.
	"simplestreams": 2,
}

// GetImageSource returns the image source with the given ID.
func GetImageSource(ctx context.Context, tx *sql.Tx, imageID int) (int, *api.ImageSource, error) {
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
		return -1, nil, err
	}

	if len(sources) == 0 {
		return -1, nil, api.StatusErrorf(http.StatusNotFound, "Image source not found")
	}

	source := sources[0]

	protocol, found := ImageSourceProtocol[source.Protocol]
	if !found {
		return -1, nil, fmt.Errorf("Invalid protocol: %d", source.Protocol)
	}

	result := &api.ImageSource{
		Server:      source.Server,
		Protocol:    protocol,
		Certificate: source.Certificate,
		Alias:       source.Alias,
	}

	return source.ID, result, nil
}

// GetCachedImageWithSource gets a cached image with the given fingerprint and source details.
func GetCachedImageWithSource(ctx context.Context, tx *sql.Tx, fingerprint string, server string, protocol string, alias string, certificate string) (int, *api.Image, error) {
	protocolCode, ok := imageSourceProtocolCode[protocol]
	if !ok {
		return -1, nil, api.StatusErrorf(http.StatusBadRequest, "Unknown protocol %q", protocol)
	}

	q := `
SELECT
	images.id,
	projects.name,
	images.fingerprint,
	images.type,
	images.size,
	images.public,
	images.architecture,
	images.creation_date,
	images.expiry_date,
	images.upload_date,
	images.cached,
	images.last_use_date,
	images.auto_update
FROM images
JOIN projects ON images.project_id = projects.id
JOIN images_source ON images.id = images_source.image_id
WHERE images.cached = 1 AND images.fingerprint = ? AND images_source.server = ? AND images_source.protocol = ? AND images_source.alias = ? AND images_source.certificate = ?`

	var image Image
	row := tx.QueryRowContext(ctx, q, fingerprint, server, protocolCode, alias, certificate)
	err := row.Scan(&image.ID, &image.Project, &image.Fingerprint, &image.Type, &image.Size, &image.Public, &image.Architecture, &image.CreationDate, &image.ExpiryDate, &image.UploadDate, &image.Cached, &image.LastUseDate, &image.AutoUpdate)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return -1, nil, api.StatusErrorf(http.StatusNotFound, "Image not found")
		}

		return -1, nil, fmt.Errorf("Failed getting cached image with source: %w", err)
	}

	apiImage, err := image.ToAPI(ctx, tx, "")
	if err != nil {
		return -1, nil, fmt.Errorf("Failed getting image API details: %w", err)
	}

	return image.ID, apiImage, nil
}
