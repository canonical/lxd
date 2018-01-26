package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/osarch"
)

var ImageSourceProtocol = map[int]string{
	0: "lxd",
	1: "direct",
	2: "simplestreams",
}

func (c *Cluster) ImagesGet(public bool) ([]string, error) {
	q := "SELECT fingerprint FROM images"
	if public == true {
		q = "SELECT fingerprint FROM images WHERE public=1"
	}

	var fp string
	inargs := []interface{}{}
	outfmt := []interface{}{fp}
	dbResults, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	results := []string{}
	for _, r := range dbResults {
		results = append(results, r[0].(string))
	}

	return results, nil
}

func (c *Cluster) ImagesGetExpired(expiry int64) ([]string, error) {
	q := `SELECT fingerprint, last_use_date, upload_date FROM images WHERE cached=1`

	var fpStr string
	var useStr string
	var uploadStr string

	inargs := []interface{}{}
	outfmt := []interface{}{fpStr, useStr, uploadStr}
	dbResults, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	results := []string{}
	for _, r := range dbResults {
		// Figure out the expiry
		timestamp := r[2]
		if r[1] != "" {
			timestamp = r[1]
		}

		var imageExpiry time.Time
		err = imageExpiry.UnmarshalText([]byte(timestamp.(string)))
		if err != nil {
			return []string{}, err
		}
		imageExpiry = imageExpiry.Add(time.Duration(expiry*24) * time.Hour)

		// Check if expired
		if imageExpiry.After(time.Now()) {
			continue
		}

		results = append(results, r[0].(string))
	}

	return results, nil
}

func (c *Cluster) ImageSourceInsert(imageId int, server string, protocol string, certificate string, alias string) error {
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

	_, err := exec(c.db, stmt, imageId, server, protocolInt, certificate, alias)
	return err
}

func (c *Cluster) ImageSourceGet(imageId int) (int, api.ImageSource, error) {
	q := `SELECT id, server, protocol, certificate, alias FROM images_source WHERE image_id=?`

	id := 0
	protocolInt := -1
	result := api.ImageSource{}

	arg1 := []interface{}{imageId}
	arg2 := []interface{}{&id, &result.Server, &protocolInt, &result.Certificate, &result.Alias}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, api.ImageSource{}, NoSuchObjectError
		}

		return -1, api.ImageSource{}, err
	}

	protocol, found := ImageSourceProtocol[protocolInt]
	if !found {
		return -1, api.ImageSource{}, fmt.Errorf("Invalid protocol: %d", protocolInt)
	}

	result.Protocol = protocol

	return id, result, nil

}

// Try to find a source entry of a locally cached image that matches
// the given remote details (server, protocol and alias). Return the
// fingerprint linked to the matching entry, if any.
func (c *Cluster) ImageSourceGetCachedFingerprint(server string, protocol string, alias string) (string, error) {
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
			WHERE server=? AND protocol=? AND alias=? AND auto_update=1
			ORDER BY creation_date DESC`

	fingerprint := ""

	arg1 := []interface{}{server, protocolInt, alias}
	arg2 := []interface{}{&fingerprint}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", NoSuchObjectError
		}

		return "", err
	}

	return fingerprint, nil
}

// Whether an image with the given fingerprint exists.
func (c *Cluster) ImageExists(fingerprint string) (bool, error) {
	var exists bool
	var err error
	query := "SELECT COUNT(*) > 0 FROM images WHERE fingerprint=?"
	inargs := []interface{}{fingerprint}
	outargs := []interface{}{&exists}
	err = dbQueryRowScan(c.db, query, inargs, outargs)
	return exists, err
}

// ImageGet gets an Image object from the database.
// If strictMatching is false, The fingerprint argument will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one image with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func (c *Cluster) ImageGet(fingerprint string, public bool, strictMatching bool) (int, *api.Image, error) {
	var err error
	var create, expire, used, upload *time.Time // These hold the db-returned times

	// The object we'll actually return
	image := api.Image{}
	id := -1
	arch := -1

	// These two humongous things will be filled by the call to DbQueryRowScan
	outfmt := []interface{}{&id, &image.Fingerprint, &image.Filename,
		&image.Size, &image.Cached, &image.Public, &image.AutoUpdate, &arch,
		&create, &expire, &used, &upload}

	var inargs []interface{}
	query := `
        SELECT
            id, fingerprint, filename, size, cached, public, auto_update, architecture,
            creation_date, expiry_date, last_use_date, upload_date
        FROM images`
	if strictMatching {
		inargs = []interface{}{fingerprint}
		query += " WHERE fingerprint = ?"
	} else {
		inargs = []interface{}{fingerprint + "%"}
		query += " WHERE fingerprint LIKE ?"
	}

	if public {
		query += " AND public=1"
	}

	err = dbQueryRowScan(c.db, query, inargs, outfmt)
	if err != nil {
		return -1, nil, err // Likely: there are no rows for this fingerprint
	}

	// Validate we only have a single match
	if !strictMatching {
		query = "SELECT COUNT(id) FROM images WHERE fingerprint LIKE ?"
		count := 0
		outfmt := []interface{}{&count}

		err = dbQueryRowScan(c.db, query, inargs, outfmt)
		if err != nil {
			return -1, nil, err
		}

		if count > 1 {
			return -1, nil, fmt.Errorf("Partial fingerprint matches more than one image")
		}
	}

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

	// The upload date is enforced by NOT NULL in the schema, so it can never be nil.
	image.UploadedAt = *upload

	// Get the properties
	q := "SELECT key, value FROM images_properties where image_id=?"
	var key, value, name, desc string
	inargs = []interface{}{id}
	outfmt = []interface{}{key, value}
	results, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return -1, nil, err
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
	results, err = queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return -1, nil, err
	}

	aliases := []api.ImageAlias{}
	for _, r := range results {
		name = r[0].(string)
		desc = r[1].(string)
		a := api.ImageAlias{Name: name, Description: desc}
		aliases = append(aliases, a)
	}

	image.Aliases = aliases

	_, source, err := c.ImageSourceGet(id)
	if err == nil {
		image.UpdateSource = &source
	}

	return id, &image, nil
}

// ImageLocate returns the address of an online node that has a local copy of
// the given image, or an empty string if the image is already available on this
// node.
//
// If the image is not available on any online node, an error is returned.
func (c *Cluster) ImageLocate(fingerprint string) (string, error) {
	stmt := `
SELECT nodes.address FROM nodes
  LEFT JOIN images_nodes ON images_nodes.node_id = nodes.id
  LEFT JOIN images ON images_nodes.image_id = images.id
WHERE images.fingerprint = ?
`
	var localAddress string // Address of this node
	var addresses []string  // Addresses of online nodes with the image

	err := c.Transaction(func(tx *ClusterTx) error {
		offlineThreshold, err := tx.NodeOfflineThreshold()
		if err != nil {
			return err
		}

		localAddress, err = tx.NodeAddress()
		if err != nil {
			return err
		}
		allAddresses, err := query.SelectStrings(tx.tx, stmt, fingerprint)
		if err != nil {
			return err
		}
		for _, address := range allAddresses {
			node, err := tx.NodeByAddress(address)
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

// ImageAssociateNode creates a new entry in the images_nodes table for
// tracking that the current node has the given image.
func (c *Cluster) ImageAssociateNode(fingerprint string) error {
	imageID, _, err := c.ImageGet(fingerprint, false, true)
	if err != nil {
		return err
	}

	err = c.Transaction(func(tx *ClusterTx) error {
		_, err := tx.tx.Exec("INSERT INTO images_nodes(image_id, node_id) VALUES(?, ?)", imageID, c.nodeID)
		return err
	})
	return err
}

func (c *Cluster) ImageDelete(id int) error {
	_, err := exec(c.db, "DELETE FROM images WHERE id=?", id)
	if err != nil {
		return err
	}

	return nil
}

func (c *Cluster) ImageAliasesGet() ([]string, error) {
	q := "SELECT name FROM images_aliases"
	var name string
	inargs := []interface{}{}
	outfmt := []interface{}{name}
	results, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, res := range results {
		names = append(names, res[0].(string))
	}
	return names, nil
}

func (c *Cluster) ImageAliasGet(name string, isTrustedClient bool) (int, api.ImageAliasesEntry, error) {
	q := `SELECT images_aliases.id, images.fingerprint, images_aliases.description
			 FROM images_aliases
			 INNER JOIN images
			 ON images_aliases.image_id=images.id
			 WHERE images_aliases.name=?`
	if !isTrustedClient {
		q = q + ` AND images.public=1`
	}

	var fingerprint, description string
	id := -1
	entry := api.ImageAliasesEntry{}

	arg1 := []interface{}{name}
	arg2 := []interface{}{&id, &fingerprint, &description}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, entry, NoSuchObjectError
		}

		return -1, entry, err
	}

	entry.Name = name
	entry.Target = fingerprint
	entry.Description = description

	return id, entry, nil
}

func (c *Cluster) ImageAliasRename(id int, name string) error {
	_, err := exec(c.db, "UPDATE images_aliases SET name=? WHERE id=?", name, id)
	return err
}

func (c *Cluster) ImageAliasDelete(name string) error {
	_, err := exec(c.db, "DELETE FROM images_aliases WHERE name=?", name)
	return err
}

func (c *Cluster) ImageAliasesMove(source int, destination int) error {
	_, err := exec(c.db, "UPDATE images_aliases SET image_id=? WHERE image_id=?", destination, source)
	return err
}

// Insert an alias ento the database.
func (c *Cluster) ImageAliasAdd(name string, imageID int, desc string) error {
	stmt := `INSERT INTO images_aliases (name, image_id, description) values (?, ?, ?)`
	_, err := exec(c.db, stmt, name, imageID, desc)
	return err
}

func (c *Cluster) ImageAliasUpdate(id int, imageID int, desc string) error {
	stmt := `UPDATE images_aliases SET image_id=?, description=? WHERE id=?`
	_, err := exec(c.db, stmt, imageID, desc, id)
	return err
}

func (c *Cluster) ImageLastAccessUpdate(fingerprint string, date time.Time) error {
	stmt := `UPDATE images SET last_use_date=? WHERE fingerprint=?`
	_, err := exec(c.db, stmt, date, fingerprint)
	return err
}

func (c *Cluster) ImageLastAccessInit(fingerprint string) error {
	stmt := `UPDATE images SET cached=1, last_use_date=strftime("%s") WHERE fingerprint=?`
	_, err := exec(c.db, stmt, fingerprint)
	return err
}

func (c *Cluster) ImageUpdate(id int, fname string, sz int64, public bool, autoUpdate bool, architecture string, createdAt time.Time, expiresAt time.Time, properties map[string]string) error {
	arch, err := osarch.ArchitectureId(architecture)
	if err != nil {
		arch = 0
	}

	tx, err := begin(c.db)
	if err != nil {
		return err
	}

	publicInt := 0
	if public {
		publicInt = 1
	}

	autoUpdateInt := 0
	if autoUpdate {
		autoUpdateInt = 1
	}

	stmt, err := tx.Prepare(`UPDATE images SET filename=?, size=?, public=?, auto_update=?, architecture=?, creation_date=?, expiry_date=? WHERE id=?`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(fname, sz, publicInt, autoUpdateInt, arch, createdAt, expiresAt, id)
	if err != nil {
		tx.Rollback()
		return err
	}

	_, err = tx.Exec(`DELETE FROM images_properties WHERE image_id=?`, id)
	if err != nil {
		tx.Rollback()
		return err
	}

	stmt2, err := tx.Prepare(`INSERT INTO images_properties (image_id, type, key, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt2.Close()

	for key, value := range properties {
		_, err = stmt2.Exec(id, 0, key, value)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	if err := TxCommit(tx); err != nil {
		return err
	}

	return nil
}

func (c *Cluster) ImageInsert(fp string, fname string, sz int64, public bool, autoUpdate bool, architecture string, createdAt time.Time, expiresAt time.Time, properties map[string]string) error {
	arch, err := osarch.ArchitectureId(architecture)
	if err != nil {
		arch = 0
	}

	tx, err := begin(c.db)
	if err != nil {
		return err
	}

	publicInt := 0
	if public {
		publicInt = 1
	}

	autoUpdateInt := 0
	if autoUpdate {
		autoUpdateInt = 1
	}

	stmt, err := tx.Prepare(`INSERT INTO images (fingerprint, filename, size, public, auto_update, architecture, creation_date, expiry_date, upload_date) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	result, err := stmt.Exec(fp, fname, sz, publicInt, autoUpdateInt, arch, createdAt, expiresAt, time.Now().UTC())
	if err != nil {
		tx.Rollback()
		return err
	}

	id64, err := result.LastInsertId()
	if err != nil {
		tx.Rollback()
		return err
	}
	id := int(id64)

	if len(properties) > 0 {
		pstmt, err := tx.Prepare(`INSERT INTO images_properties (image_id, type, key, value) VALUES (?, 0, ?, ?)`)
		if err != nil {
			tx.Rollback()
			return err
		}
		defer pstmt.Close()

		for k, v := range properties {
			// we can assume, that there is just one
			// value per key
			_, err = pstmt.Exec(id, k, v)
			if err != nil {
				tx.Rollback()
				return err
			}
		}

	}

	_, err = tx.Exec("INSERT INTO images_nodes(image_id, node_id) VALUES(?, ?)", id, c.nodeID)
	if err != nil {
		tx.Rollback()
		return err
	}

	if err := TxCommit(tx); err != nil {
		return err
	}

	return nil
}

// Get the names of all storage pools on which a given image exists.
func (c *Cluster) ImageGetPools(imageFingerprint string) ([]int64, error) {
	poolID := int64(-1)
	query := "SELECT storage_pool_id FROM storage_volumes WHERE node_id=? AND name=? AND type=?"
	inargs := []interface{}{c.nodeID, imageFingerprint, StoragePoolVolumeTypeImage}
	outargs := []interface{}{poolID}

	result, err := queryScan(c.db, query, inargs, outargs)
	if err != nil {
		return []int64{}, err
	}

	poolIDs := []int64{}
	for _, r := range result {
		poolIDs = append(poolIDs, r[0].(int64))
	}

	return poolIDs, nil
}

// Get the names of all storage pools on which a given image exists.
func (c *Cluster) ImageGetPoolNamesFromIDs(poolIDs []int64) ([]string, error) {
	var poolName string
	query := "SELECT name FROM storage_pools WHERE id=?"

	poolNames := []string{}
	for _, poolID := range poolIDs {
		inargs := []interface{}{poolID}
		outargs := []interface{}{poolName}

		result, err := queryScan(c.db, query, inargs, outargs)
		if err != nil {
			return []string{}, err
		}

		for _, r := range result {
			poolNames = append(poolNames, r[0].(string))
		}
	}

	return poolNames, nil
}

// ImageUploadedAt updates the upload_date column and an image row.
func (c *Cluster) ImageUploadedAt(id int, uploadedAt time.Time) error {
	_, err := exec(c.db, "UPDATE images SET upload_date=? WHERE id=?", uploadedAt, id)
	return err
}
