// +build linux,cgo,!agent

package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/osarch"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t images.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -p db -e image objects
//go:generate mapper stmt -p db -e image objects-by-Project
//go:generate mapper stmt -p db -e image objects-by-Project-and-Public
//go:generate mapper stmt -p db -e image objects-by-Project-and-Fingerprint
//go:generate mapper stmt -p db -e image objects-by-Fingerprint
//go:generate mapper stmt -p db -e image objects-by-Cached
//
//go:generate mapper method -p db -e image List
//go:generate mapper method -p db -e image Get

// Image is a value object holding db-related details about an image.
type Image struct {
	ID           int
	Project      string `db:"primary=yes&join=projects.name"`
	Fingerprint  string `db:"primary=yes&comparison=like"`
	Type         int
	Filename     string
	Size         int64
	Public       bool
	Architecture int
	CreationDate time.Time
	ExpiryDate   time.Time
	UploadDate   time.Time
	Cached       bool
	LastUseDate  time.Time
	AutoUpdate   int
}

// ImageFilter can be used to filter results yielded by GetImages.
type ImageFilter struct {
	Project     string
	Fingerprint string // Matched with LIKE
	Public      bool
	Cached      bool
}

// ImageSourceProtocol maps image source protocol codes to human-readable names.
var ImageSourceProtocol = map[int]string{
	0: "lxd",
	1: "direct",
	2: "simplestreams",
}

// GetLocalImagesFingerprints returns the fingerprints of all local images.
func (c *ClusterTx) GetLocalImagesFingerprints() ([]string, error) {
	q := `
SELECT images.fingerprint
  FROM images_nodes
  JOIN images ON images.id = images_nodes.image_id
 WHERE node_id = ?
`
	return query.SelectStrings(c.tx, q, c.nodeID)
}

// GetImagesFingerprints returns the names of all images (optionally only the public ones).
func (c *Cluster) GetImagesFingerprints(project string, public bool) ([]string, error) {
	q := `
SELECT fingerprint
  FROM images
  JOIN projects ON projects.id = images.project_id
 WHERE projects.name = ?
`
	if public == true {
		q += " AND public=1"
	}

	var fingerprints []string

	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasImages(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has images")
		}
		if !enabled {
			project = "default"
		}
		fingerprints, err = query.SelectStrings(tx.tx, q, project)
		return err
	})
	if err != nil {
		return nil, err
	}

	return fingerprints, nil
}

// ExpiredImage used to store expired image info.
type ExpiredImage struct {
	Fingerprint string
	ProjectName string
}

// GetExpiredImages returns the names and project name of all images that have expired since the given time.
func (c *Cluster) GetExpiredImages(expiry int64) ([]ExpiredImage, error) {
	var images []Image
	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		images, err = tx.GetImages(ImageFilter{Cached: true})
		return err
	})
	if err != nil {
		return nil, err
	}

	results := []ExpiredImage{}
	for _, r := range images {
		// Figure out the expiry
		timestamp := r.UploadDate
		if !r.LastUseDate.IsZero() {
			timestamp = r.LastUseDate
		}

		imageExpiry := timestamp
		imageExpiry = imageExpiry.Add(time.Duration(expiry*24) * time.Hour)

		// Check if expired
		if imageExpiry.After(time.Now()) {
			continue
		}

		result := ExpiredImage{
			Fingerprint: r.Fingerprint,
			ProjectName: r.Project,
		}

		results = append(results, result)
	}

	return results, nil
}

// CreateImageSource inserts a new image source.
func (c *Cluster) CreateImageSource(id int, server string, protocol string, certificate string, alias string) error {
	stmt := `INSERT INTO images_source (image_id, server, protocol, certificate, alias) values (?, ?, ?, ?, ?)`

	protocolInt := -1
	for protoInt, protoString := range ImageSourceProtocol {
		if protoString == protocol {
			protocolInt = protoInt
		}
	}

	if protocolInt == -1 {
		return fmt.Errorf("Invalid protocol: %s", protocol)
	}

	err := exec(c, stmt, id, server, protocolInt, certificate, alias)
	return err
}

// GetImageSource returns the image source with the given ID.
func (c *Cluster) GetImageSource(imageID int) (int, api.ImageSource, error) {
	q := `SELECT id, server, protocol, certificate, alias FROM images_source WHERE image_id=?`
	sources := []struct {
		ID          int
		Server      string
		Protocol    int
		Certificate string
		Alias       string
	}{}
	dest := func(i int) []interface{} {
		sources = append(sources, struct {
			ID          int
			Server      string
			Protocol    int
			Certificate string
			Alias       string
		}{})
		return []interface{}{
			&sources[i].ID,
			&sources[i].Server,
			&sources[i].Protocol,
			&sources[i].Certificate,
			&sources[i].Alias,
		}

	}
	err := c.Transaction(func(tx *ClusterTx) error {
		stmt, err := tx.tx.Prepare(q)
		if err != nil {
			return err
		}
		defer stmt.Close()
		err = query.SelectObjects(stmt, dest, imageID)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return -1, api.ImageSource{}, err
	}
	if len(sources) == 0 {
		return -1, api.ImageSource{}, ErrNoSuchObject
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

// ImageSourceGetCachedFingerprint tries to find a source entry of a locally
// cached image that matches the given remote details (server, protocol and
// alias). Return the fingerprint linked to the matching entry, if any.
func (c *Cluster) ImageSourceGetCachedFingerprint(server string, protocol string, alias string, typeName string, architecture int) (string, error) {
	imageType := instancetype.Any
	if typeName != "" {
		var err error
		imageType, err = instancetype.New(typeName)
		if err != nil {
			return "", err
		}
	}

	protocolInt := -1
	for protoInt, protoString := range ImageSourceProtocol {
		if protoString == protocol {
			protocolInt = protoInt
		}
	}

	if protocolInt == -1 {
		return "", fmt.Errorf("Invalid protocol: %s", protocol)
	}

	q := `SELECT images.fingerprint
			FROM images_source
			INNER JOIN images
			ON images_source.image_id=images.id
			WHERE server=? AND protocol=? AND alias=? AND auto_update=1 AND images.architecture=?
`

	args := []interface{}{server, protocolInt, alias, architecture}
	if imageType != instancetype.Any {
		q += "AND images.type=?\n"
		args = append(args, imageType)
	}

	q += "ORDER BY creation_date DESC"

	var fingerprints []string
	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		fingerprints, err = query.SelectStrings(tx.tx, q, args...)
		return err
	})
	if err != nil {
		return "", err
	}
	if len(fingerprints) == 0 {
		return "", ErrNoSuchObject
	}

	return fingerprints[0], nil
}

// ImageExists returns whether an image with the given fingerprint exists.
func (c *Cluster) ImageExists(project string, fingerprint string) (bool, error) {
	table := "images JOIN projects ON projects.id = images.project_id"
	where := "projects.name = ? AND fingerprint=?"

	var exists bool
	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasImages(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has images")
		}
		if !enabled {
			project = "default"
		}
		count, err := query.Count(tx.tx, table, where, project, fingerprint)
		if err != nil {
			return err
		}
		exists = count > 0
		return nil
	})
	if err != nil {
		return false, err
	}

	return exists, nil
}

// ImageIsReferencedByOtherProjects returns true if the image with the given
// fingerprint is referenced by projects other than the given one.
func (c *Cluster) ImageIsReferencedByOtherProjects(project string, fingerprint string) (bool, error) {
	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasImages(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has images")
		}
		if !enabled {
			project = "default"
		}
		return nil
	})
	if err != nil {
		return false, err
	}

	var referenced bool
	query := `
SELECT COUNT(*) > 0
  FROM images
  JOIN projects ON projects.id = images.project_id
 WHERE projects.name != ? AND fingerprint=?
`
	inargs := []interface{}{project, fingerprint}
	outargs := []interface{}{&referenced}
	err = dbQueryRowScan(c, query, inargs, outargs)
	if err == sql.ErrNoRows {
		return referenced, nil
	}

	return referenced, err
}

// GetImage gets an Image object from the database.
// If strictMatching is false, The fingerprint argument will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one image with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func (c *Cluster) GetImage(project, fingerprint string, public bool, strictMatching bool) (int, *api.Image, error) {
	profileProject := project
	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasImages(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has images")
		}
		if !enabled {
			project = "default"
		}
		return nil
	})
	if err != nil {
		return -1, nil, err
	}

	var create, expire, used, upload *time.Time // These hold the db-returned times

	// The object we'll actually return
	image := api.Image{}
	id := -1
	arch := -1
	imageType := -1

	// These two humongous things will be filled by the call to DbQueryRowScan
	outfmt := []interface{}{&id, &image.Fingerprint, &image.Filename,
		&image.Size, &image.Cached, &image.Public, &image.AutoUpdate, &arch,
		&create, &expire, &used, &upload, &imageType}

	inargs := []interface{}{project}
	query := `
        SELECT
            images.id, fingerprint, filename, size, cached, public, auto_update, architecture,
            creation_date, expiry_date, last_use_date, upload_date, type
        FROM images
        JOIN projects ON projects.id = images.project_id
       WHERE projects.name = ?`
	if strictMatching {
		inargs = append(inargs, fingerprint)
		query += " AND fingerprint = ?"
	} else {
		inargs = append(inargs, fingerprint+"%")
		query += " AND fingerprint LIKE ?"
	}

	if public {
		query += " AND public=1"
	}

	err = dbQueryRowScan(c, query, inargs, outfmt)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, ErrNoSuchObject
		}

		return -1, nil, err // Likely: there are no rows for this fingerprint
	}

	// Validate we only have a single match
	if !strictMatching {
		query = `
SELECT COUNT(images.id)
  FROM images
  JOIN projects ON projects.id = images.project_id
 WHERE projects.name = ?
   AND fingerprint LIKE ?
`
		count := 0
		outfmt := []interface{}{&count}

		err = dbQueryRowScan(c, query, inargs, outfmt)
		if err != nil {
			return -1, nil, err
		}

		if count > 1 {
			return -1, nil, fmt.Errorf("Partial fingerprint matches more than one image")
		}
	}

	err = c.imageFill(id, &image, create, expire, used, upload, arch, imageType)
	if err != nil {
		return -1, nil, errors.Wrapf(err, "Fill image details")
	}

	err = c.imageFillProfiles(id, &image, profileProject)
	if err != nil {
		return -1, nil, errors.Wrapf(err, "Fill image profiles")
	}

	return id, &image, nil
}

// GetImageFromAnyProject returns an image matching the given fingerprint, if
// it exists in any project.
func (c *Cluster) GetImageFromAnyProject(fingerprint string) (int, *api.Image, error) {
	var create, expire, used, upload *time.Time // These hold the db-returned times

	// The object we'll actually return
	image := api.Image{}
	id := -1
	arch := -1
	imageType := -1

	// These two humongous things will be filled by the call to DbQueryRowScan
	outfmt := []interface{}{&id, &image.Fingerprint, &image.Filename,
		&image.Size, &image.Cached, &image.Public, &image.AutoUpdate, &arch,
		&create, &expire, &used, &upload, &imageType}

	inargs := []interface{}{fingerprint}
	query := `
        SELECT
            images.id, fingerprint, filename, size, cached, public, auto_update, architecture,
            creation_date, expiry_date, last_use_date, upload_date, type
        FROM images
        WHERE fingerprint = ?
        LIMIT 1`

	err := dbQueryRowScan(c, query, inargs, outfmt)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, ErrNoSuchObject
		}

		return -1, nil, err // Likely: there are no rows for this fingerprint
	}

	err = c.imageFill(id, &image, create, expire, used, upload, arch, imageType)
	if err != nil {
		return -1, nil, errors.Wrapf(err, "Fill image details")
	}

	return id, &image, nil
}

// Fill extra image fields such as properties and alias. This is called after
// fetching a single row from the images table.
func (c *Cluster) imageFill(id int, image *api.Image, create, expire, used, upload *time.Time, arch int, imageType int) error {
	// Some of the dates can be nil in the DB, let's process them.
	if create != nil {
		image.CreatedAt = *create
	} else {
		image.CreatedAt = time.Time{}
	}

	if expire != nil {
		image.ExpiresAt = *expire
	} else {
		image.ExpiresAt = time.Time{}
	}

	if used != nil {
		image.LastUsedAt = *used
	} else {
		image.LastUsedAt = time.Time{}
	}

	image.Architecture, _ = osarch.ArchitectureName(arch)
	image.Type = instancetype.Type(imageType).String()

	// The upload date is enforced by NOT NULL in the schema, so it can never be nil.
	image.UploadedAt = *upload

	// Get the properties
	q := "SELECT key, value FROM images_properties where image_id=?"
	var key, value, name, desc string
	inargs := []interface{}{id}
	outfmt := []interface{}{key, value}
	results, err := queryScan(c, q, inargs, outfmt)
	if err != nil {
		return err
	}

	properties := map[string]string{}
	for _, r := range results {
		key = r[0].(string)
		value = r[1].(string)
		properties[key] = value
	}

	image.Properties = properties

	// Get the aliases
	q = "SELECT name, description FROM images_aliases WHERE image_id=?"
	inargs = []interface{}{id}
	outfmt = []interface{}{name, desc}
	results, err = queryScan(c, q, inargs, outfmt)
	if err != nil {
		return err
	}

	aliases := []api.ImageAlias{}
	for _, r := range results {
		name = r[0].(string)
		desc = r[1].(string)
		a := api.ImageAlias{Name: name, Description: desc}
		aliases = append(aliases, a)
	}

	image.Aliases = aliases

	_, source, err := c.GetImageSource(id)
	if err == nil {
		image.UpdateSource = &source
	}

	return nil
}

func (c *Cluster) imageFillProfiles(id int, image *api.Image, project string) error {
	// Check which project name to use
	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasProfiles(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has profiles")
		}
		if !enabled {
			project = "default"
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Get the profiles
	q := `
SELECT profiles.name FROM profiles
	JOIN images_profiles ON images_profiles.profile_id = profiles.id
	JOIN projects ON profiles.project_id = projects.id
WHERE images_profiles.image_id = ? AND projects.name = ?
`
	var name string
	inargs := []interface{}{id, project}
	outfmt := []interface{}{name}
	results, err := queryScan(c, q, inargs, outfmt)
	if err != nil {
		return err
	}

	profiles := make([]string, 0)
	for _, r := range results {
		name = r[0].(string)
		profiles = append(profiles, name)
	}

	image.Profiles = profiles
	return nil
}

// LocateImage returns the address of an online node that has a local copy of
// the given image, or an empty string if the image is already available on this
// node.
//
// If the image is not available on any online node, an error is returned.
func (c *Cluster) LocateImage(fingerprint string) (string, error) {
	stmt := `
SELECT nodes.address FROM nodes
  LEFT JOIN images_nodes ON images_nodes.node_id = nodes.id
  LEFT JOIN images ON images_nodes.image_id = images.id
WHERE images.fingerprint = ?
`
	var localAddress string // Address of this node
	var addresses []string  // Addresses of online nodes with the image

	err := c.Transaction(func(tx *ClusterTx) error {
		offlineThreshold, err := tx.GetNodeOfflineThreshold()
		if err != nil {
			return err
		}

		localAddress, err = tx.GetLocalNodeAddress()
		if err != nil {
			return err
		}
		allAddresses, err := query.SelectStrings(tx.tx, stmt, fingerprint)
		if err != nil {
			return err
		}
		for _, address := range allAddresses {
			node, err := tx.GetNodeByAddress(address)
			if err != nil {
				return err
			}
			if address != localAddress && node.IsOffline(offlineThreshold) {
				continue
			}
			addresses = append(addresses, address)
		}
		return err
	})
	if err != nil {
		return "", err
	}
	if len(addresses) == 0 {
		return "", fmt.Errorf("image not available on any online node")
	}

	for _, address := range addresses {
		if address == localAddress {
			return "", nil
		}
	}

	return addresses[0], nil
}

// AddImageToLocalNode creates a new entry in the images_nodes table for
// tracking that the local node has the given image.
func (c *Cluster) AddImageToLocalNode(project, fingerprint string) error {
	imageID, _, err := c.GetImage(project, fingerprint, false, true)
	if err != nil {
		return err
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		_, err := tx.tx.Exec("INSERT INTO images_nodes(image_id, node_id) VALUES(?, ?)", imageID, c.nodeID)
		return err
	})
	return err
}

// DeleteImage deletes the image with the given ID.
func (c *Cluster) DeleteImage(id int) error {
	err := exec(c, "DELETE FROM images WHERE id=?", id)
	if err != nil {
		return err
	}

	return nil
}

// GetImageAliases returns the names of the aliases of all images.
func (c *Cluster) GetImageAliases(project string) ([]string, error) {
	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasImages(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has images")
		}
		if !enabled {
			project = "default"
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	q := `
SELECT images_aliases.name
  FROM images_aliases
  JOIN projects ON projects.id=images_aliases.project_id
 WHERE projects.name=?
`
	var name string
	inargs := []interface{}{project}
	outfmt := []interface{}{name}
	results, err := queryScan(c, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, res := range results {
		names = append(names, res[0].(string))
	}
	return names, nil
}

// GetImageAlias returns the alias with the given name in the given project.
func (c *Cluster) GetImageAlias(project, name string, isTrustedClient bool) (int, api.ImageAliasesEntry, error) {
	id := -1
	entry := api.ImageAliasesEntry{}

	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasImages(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has images")
		}
		if !enabled {
			project = "default"
		}
		return nil
	})
	if err != nil {
		return id, entry, err
	}

	q := `SELECT images_aliases.id, images.fingerprint, images.type, images_aliases.description
			 FROM images_aliases
			 INNER JOIN images
			 ON images_aliases.image_id=images.id
                         INNER JOIN projects
                         ON images_aliases.project_id=projects.id
			 WHERE projects.name=? AND images_aliases.name=?`
	if !isTrustedClient {
		q = q + ` AND images.public=1`
	}

	var fingerprint, description string
	var imageType int

	arg1 := []interface{}{project, name}
	arg2 := []interface{}{&id, &fingerprint, &imageType, &description}
	err = dbQueryRowScan(c, q, arg1, arg2)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, entry, ErrNoSuchObject
		}

		return -1, entry, err
	}

	entry.Name = name
	entry.Target = fingerprint
	entry.Description = description
	entry.Type = instancetype.Type(imageType).String()

	return id, entry, nil
}

// RenameImageAlias renames the alias with the given ID.
func (c *Cluster) RenameImageAlias(id int, name string) error {
	err := exec(c, "UPDATE images_aliases SET name=? WHERE id=?", name, id)
	return err
}

// DeleteImageAlias deletes the alias with the given name.
func (c *Cluster) DeleteImageAlias(project, name string) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasImages(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has images")
		}
		if !enabled {
			project = "default"
		}
		return nil
	})
	if err != nil {
		return err
	}

	err = exec(c, `
DELETE
  FROM images_aliases
 WHERE project_id = (SELECT id FROM projects WHERE name = ?) AND name = ?
`, project, name)
	return err
}

// MoveImageAlias changes the image ID associated with an alias.
func (c *Cluster) MoveImageAlias(source int, destination int) error {
	err := exec(c, "UPDATE images_aliases SET image_id=? WHERE image_id=?", destination, source)
	return err
}

// CreateImageAlias inserts an alias ento the database.
func (c *Cluster) CreateImageAlias(project, name string, imageID int, desc string) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasImages(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has images")
		}
		if !enabled {
			project = "default"
		}
		return nil
	})
	if err != nil {
		return err
	}

	stmt := `
INSERT INTO images_aliases (name, image_id, description, project_id)
     VALUES (?, ?, ?, (SELECT id FROM projects WHERE name = ?))
`
	err = exec(c, stmt, name, imageID, desc, project)
	return err
}

// UpdateImageAlias updates the alias with the given ID.
func (c *Cluster) UpdateImageAlias(id int, imageID int, desc string) error {
	stmt := `UPDATE images_aliases SET image_id=?, description=? WHERE id=?`
	err := exec(c, stmt, imageID, desc, id)
	return err
}

// CopyDefaultImageProfiles copies default profiles from id to new_id.
func (c *Cluster) CopyDefaultImageProfiles(id int, newID int) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		// Delete all current associations.
		_, err := tx.tx.Exec("DELETE FROM images_profiles WHERE image_id=?", newID)
		if err != nil {
			return err
		}

		// Copy the entries over.
		_, err = tx.tx.Exec("INSERT INTO images_profiles (image_id, profile_id) SELECT ?, profile_id FROM images_profiles WHERE image_id=?", newID, id)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// UpdateImageLastUseDate updates the last_use_date field of the image with the
// given fingerprint.
func (c *Cluster) UpdateImageLastUseDate(fingerprint string, date time.Time) error {
	stmt := `UPDATE images SET last_use_date=? WHERE fingerprint=?`
	err := exec(c, stmt, date, fingerprint)
	return err
}

// InitImageLastUseDate inits the last_use_date field of the image with the given fingerprint.
func (c *Cluster) InitImageLastUseDate(fingerprint string) error {
	stmt := `UPDATE images SET cached=1, last_use_date=strftime("%s") WHERE fingerprint=?`
	err := exec(c, stmt, fingerprint)
	return err
}

// UpdateImage updates the image with the given ID.
func (c *Cluster) UpdateImage(id int, fname string, sz int64, public bool, autoUpdate bool, architecture string, createdAt time.Time, expiresAt time.Time, properties map[string]string, project string, profileIds []int64) error {
	arch, err := osarch.ArchitectureId(architecture)
	if err != nil {
		arch = 0
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		publicInt := 0
		if public {
			publicInt = 1
		}

		autoUpdateInt := 0
		if autoUpdate {
			autoUpdateInt = 1
		}

		stmt, err := tx.tx.Prepare(`UPDATE images SET filename=?, size=?, public=?, auto_update=?, architecture=?, creation_date=?, expiry_date=? WHERE id=?`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		_, err = stmt.Exec(fname, sz, publicInt, autoUpdateInt, arch, createdAt, expiresAt, id)
		if err != nil {
			return err
		}

		_, err = tx.tx.Exec(`DELETE FROM images_properties WHERE image_id=?`, id)
		if err != nil {
			return err
		}

		stmt2, err := tx.tx.Prepare(`INSERT INTO images_properties (image_id, type, key, value) VALUES (?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt2.Close()

		for key, value := range properties {
			_, err = stmt2.Exec(id, 0, key, value)
			if err != nil {
				return err
			}
		}

		if project != "" && profileIds != nil {
			enabled, err := tx.ProjectHasProfiles(project)
			if err != nil {
				return err
			}
			if !enabled {
				project = "default"
			}
			q := `DELETE FROM images_profiles
				WHERE image_id = ? AND profile_id IN (
					SELECT profiles.id FROM profiles
					JOIN projects ON profiles.project_id = projects.id
					WHERE projects.name = ?
				)`
			_, err = tx.tx.Exec(q, id, project)
			if err != nil {
				return err
			}

			stmt3, err := tx.tx.Prepare(`INSERT INTO images_profiles (image_id, profile_id) VALUES (?, ?)`)
			if err != nil {
				return err
			}
			defer stmt3.Close()

			for _, profileID := range profileIds {
				_, err = stmt3.Exec(id, profileID)
				if err != nil {
					return err
				}
			}
		}

		return nil
	})
	return err
}

// CreateImage creates a new image.
func (c *Cluster) CreateImage(project, fp string, fname string, sz int64, public bool, autoUpdate bool, architecture string, createdAt time.Time, expiresAt time.Time, properties map[string]string, typeName string) error {
	profileProject := project
	err := c.Transaction(func(tx *ClusterTx) error {
		enabled, err := tx.ProjectHasImages(project)
		if err != nil {
			return errors.Wrap(err, "Check if project has images")
		}
		if !enabled {
			project = "default"
		}
		return nil
	})
	if err != nil {
		return err
	}

	arch, err := osarch.ArchitectureId(architecture)
	if err != nil {
		arch = 0
	}

	imageType := instancetype.Any
	if typeName != "" {
		var err error
		imageType, err = instancetype.New(typeName)
		if err != nil {
			return err
		}
	}

	if imageType == -1 {
		return fmt.Errorf("Invalid image type: %v", typeName)
	}

	defaultProfileID, _, err := c.GetProfile(profileProject, "default")
	if err != nil {
		return err
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		publicInt := 0
		if public {
			publicInt = 1
		}

		autoUpdateInt := 0
		if autoUpdate {
			autoUpdateInt = 1
		}

		stmt, err := tx.tx.Prepare(`INSERT INTO images (project_id, fingerprint, filename, size, public, auto_update, architecture, creation_date, expiry_date, upload_date, type) VALUES ((SELECT id FROM projects WHERE name = ?), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		result, err := stmt.Exec(project, fp, fname, sz, publicInt, autoUpdateInt, arch, createdAt, expiresAt, time.Now().UTC(), imageType)
		if err != nil {
			return err
		}

		id64, err := result.LastInsertId()
		if err != nil {
			return err
		}
		id := int(id64)

		if len(properties) > 0 {
			pstmt, err := tx.tx.Prepare(`INSERT INTO images_properties (image_id, type, key, value) VALUES (?, 0, ?, ?)`)
			if err != nil {
				return err
			}
			defer pstmt.Close()

			for k, v := range properties {
				// we can assume, that there is just one
				// value per key
				_, err = pstmt.Exec(id, k, v)
				if err != nil {
					return err
				}
			}

		}

		_, err = tx.tx.Exec("INSERT INTO images_profiles(image_id, profile_id) VALUES(?, ?)", id, defaultProfileID)
		if err != nil {
			return err
		}

		_, err = tx.tx.Exec("INSERT INTO images_nodes(image_id, node_id) VALUES(?, ?)", id, c.nodeID)
		if err != nil {
			return err
		}

		return nil
	})
	return err
}

// GetPoolsWithImage get the names of all storage pools on which a given image exists.
func (c *Cluster) GetPoolsWithImage(imageFingerprint string) ([]int64, error) {
	poolID := int64(-1)
	query := "SELECT storage_pool_id FROM storage_volumes WHERE node_id=? AND name=? AND type=?"
	inargs := []interface{}{c.nodeID, imageFingerprint, StoragePoolVolumeTypeImage}
	outargs := []interface{}{poolID}

	result, err := queryScan(c, query, inargs, outargs)
	if err != nil {
		return []int64{}, err
	}

	poolIDs := []int64{}
	for _, r := range result {
		poolIDs = append(poolIDs, r[0].(int64))
	}

	return poolIDs, nil
}

// GetPoolNamesFromIDs get the names of all storage pools on which a given image exists.
func (c *Cluster) GetPoolNamesFromIDs(poolIDs []int64) ([]string, error) {
	var poolName string
	query := "SELECT name FROM storage_pools WHERE id=?"

	poolNames := []string{}
	for _, poolID := range poolIDs {
		inargs := []interface{}{poolID}
		outargs := []interface{}{poolName}

		result, err := queryScan(c, query, inargs, outargs)
		if err != nil {
			return []string{}, err
		}

		for _, r := range result {
			poolNames = append(poolNames, r[0].(string))
		}
	}

	return poolNames, nil
}

// UpdateImageUploadDate updates the upload_date column and an image row.
func (c *Cluster) UpdateImageUploadDate(id int, uploadedAt time.Time) error {
	err := exec(c, "UPDATE images SET upload_date=? WHERE id=?", uploadedAt, id)
	return err
}

// GetImagesOnLocalNode returns all images that the local LXD node has.
func (c *Cluster) GetImagesOnLocalNode() (map[string][]string, error) {
	return c.GetImagesOnNode(c.nodeID)
}

// GetImagesOnNode returns all images that the node with the given id has.
func (c *Cluster) GetImagesOnNode(id int64) (map[string][]string, error) {
	images := make(map[string][]string) // key is fingerprint, value is list of projects
	err := c.Transaction(func(tx *ClusterTx) error {
		stmt := `
    SELECT images.fingerprint, projects.name FROM images
      LEFT JOIN images_nodes ON images.id = images_nodes.image_id
			LEFT JOIN nodes ON images_nodes.node_id = nodes.id
			LEFT JOIN projects ON images.project_id = projects.id
    WHERE nodes.id = ?
		`
		rows, err := tx.tx.Query(stmt, id)
		if err != nil {
			return err
		}

		var fingerprint string
		var projectName string
		for rows.Next() {
			err := rows.Scan(&fingerprint, &projectName)
			if err != nil {
				return err
			}

			images[fingerprint] = append(images[fingerprint], projectName)
		}

		return rows.Err()
	})
	return images, err
}

// GetNodesWithImage returns the addresses of online nodes which already have the image.
func (c *Cluster) GetNodesWithImage(fingerprint string) ([]string, error) {
	q := `
SELECT DISTINCT nodes.address FROM nodes
  LEFT JOIN images_nodes ON images_nodes.node_id = nodes.id
  LEFT JOIN images ON images_nodes.image_id = images.id
WHERE images.fingerprint = ?
	`
	return c.getNodesByImageFingerprint(q, fingerprint)
}

// GetNodesWithoutImage returns the addresses of online nodes which don't have the image.
func (c *Cluster) GetNodesWithoutImage(fingerprint string) ([]string, error) {
	q := `
SELECT DISTINCT nodes.address FROM nodes WHERE nodes.address NOT IN (
  SELECT DISTINCT nodes.address FROM nodes
    LEFT JOIN images_nodes ON images_nodes.node_id = nodes.id
    LEFT JOIN images ON images_nodes.image_id = images.id
  WHERE images.fingerprint = ?)
`
	return c.getNodesByImageFingerprint(q, fingerprint)
}

func (c *Cluster) getNodesByImageFingerprint(stmt, fingerprint string) ([]string, error) {
	var addresses []string // Addresses of online nodes with the image
	err := c.Transaction(func(tx *ClusterTx) error {
		offlineThreshold, err := tx.GetNodeOfflineThreshold()
		if err != nil {
			return err
		}

		allAddresses, err := query.SelectStrings(tx.tx, stmt, fingerprint)
		if err != nil {
			return err
		}
		for _, address := range allAddresses {
			node, err := tx.GetNodeByAddress(address)
			if err != nil {
				return err
			}
			if node.IsOffline(offlineThreshold) {
				continue
			}
			addresses = append(addresses, address)
		}
		return err
	})
	return addresses, err
}
