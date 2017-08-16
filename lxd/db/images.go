package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/osarch"
)

var ImageSourceProtocol = map[int]string{
	0: "lxd",
	1: "direct",
	2: "simplestreams",
}

func ImagesGet(db *sql.DB, public bool) ([]string, error) {
	q := "SELECT fingerprint FROM images"
	if public == true {
		q = "SELECT fingerprint FROM images WHERE public=1"
	}

	var fp string
	inargs := []interface{}{}
	outfmt := []interface{}{fp}
	dbResults, err := QueryScan(db, q, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	results := []string{}
	for _, r := range dbResults {
		results = append(results, r[0].(string))
	}

	return results, nil
}

func ImagesGetExpired(db *sql.DB, expiry int64) ([]string, error) {
	q := `SELECT fingerprint FROM images WHERE cached=1 AND creation_date<=strftime('%s', date('now', '-` + fmt.Sprintf("%d", expiry) + ` day'))`

	var fp string
	inargs := []interface{}{}
	outfmt := []interface{}{fp}
	dbResults, err := QueryScan(db, q, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	results := []string{}
	for _, r := range dbResults {
		results = append(results, r[0].(string))
	}

	return results, nil
}

func ImageSourceInsert(db *sql.DB, imageId int, server string, protocol string, certificate string, alias string) error {
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

	_, err := Exec(db, stmt, imageId, server, protocolInt, certificate, alias)
	return err
}

func ImageSourceGet(db *sql.DB, imageId int) (int, api.ImageSource, error) {
	q := `SELECT id, server, protocol, certificate, alias FROM images_source WHERE image_id=?`

	id := 0
	protocolInt := -1
	result := api.ImageSource{}

	arg1 := []interface{}{imageId}
	arg2 := []interface{}{&id, &result.Server, &protocolInt, &result.Certificate, &result.Alias}
	err := dbQueryRowScan(db, q, arg1, arg2)
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
func ImageSourceGetCachedFingerprint(db *sql.DB, server string, protocol string, alias string) (string, error) {
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
	err := dbQueryRowScan(db, q, arg1, arg2)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", NoSuchObjectError
		}

		return "", err
	}

	return fingerprint, nil
}

// Whether an image with the given fingerprint exists.
func ImageExists(db *sql.DB, fingerprint string) (bool, error) {
	var exists bool
	var err error
	query := "SELECT COUNT(*) > 0 FROM images WHERE fingerprint=?"
	inargs := []interface{}{fingerprint}
	outargs := []interface{}{&exists}
	err = dbQueryRowScan(db, query, inargs, outargs)
	return exists, err
}

// ImageGet gets an Image object from the database.
// If strictMatching is false, The fingerprint argument will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one image with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func ImageGet(db *sql.DB, fingerprint string, public bool, strictMatching bool) (int, *api.Image, error) {
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

	err = dbQueryRowScan(db, query, inargs, outfmt)
	if err != nil {
		return -1, nil, err // Likely: there are no rows for this fingerprint
	}

	// Validate we only have a single match
	if !strictMatching {
		query = "SELECT COUNT(id) FROM images WHERE fingerprint LIKE ?"
		count := 0
		outfmt := []interface{}{&count}

		err = dbQueryRowScan(db, query, inargs, outfmt)
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
	results, err := QueryScan(db, q, inargs, outfmt)
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
	results, err = QueryScan(db, q, inargs, outfmt)
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

	_, source, err := ImageSourceGet(db, id)
	if err == nil {
		image.UpdateSource = &source
	}

	return id, &image, nil
}

func ImageDelete(db *sql.DB, id int) error {
	_, err := Exec(db, "DELETE FROM images WHERE id=?", id)
	if err != nil {
		return err
	}

	return nil
}

func ImageAliasGet(db *sql.DB, name string, isTrustedClient bool) (int, api.ImageAliasesEntry, error) {
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
	err := dbQueryRowScan(db, q, arg1, arg2)
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

func ImageAliasRename(db *sql.DB, id int, name string) error {
	_, err := Exec(db, "UPDATE images_aliases SET name=? WHERE id=?", name, id)
	return err
}

func ImageAliasDelete(db *sql.DB, name string) error {
	_, err := Exec(db, "DELETE FROM images_aliases WHERE name=?", name)
	return err
}

func ImageAliasesMove(db *sql.DB, source int, destination int) error {
	_, err := Exec(db, "UPDATE images_aliases SET image_id=? WHERE image_id=?", destination, source)
	return err
}

// Insert an alias ento the database.
func ImageAliasAdd(db *sql.DB, name string, imageID int, desc string) error {
	stmt := `INSERT INTO images_aliases (name, image_id, description) values (?, ?, ?)`
	_, err := Exec(db, stmt, name, imageID, desc)
	return err
}

func ImageAliasUpdate(db *sql.DB, id int, imageID int, desc string) error {
	stmt := `UPDATE images_aliases SET image_id=?, description=? WHERE id=?`
	_, err := Exec(db, stmt, imageID, desc, id)
	return err
}

func ImageLastAccessUpdate(db *sql.DB, fingerprint string, date time.Time) error {
	stmt := `UPDATE images SET last_use_date=? WHERE fingerprint=?`
	_, err := Exec(db, stmt, date, fingerprint)
	return err
}

func ImageLastAccessInit(db *sql.DB, fingerprint string) error {
	stmt := `UPDATE images SET cached=1, last_use_date=strftime("%s") WHERE fingerprint=?`
	_, err := Exec(db, stmt, fingerprint)
	return err
}

func ImageUpdate(db *sql.DB, id int, fname string, sz int64, public bool, autoUpdate bool, architecture string, createdAt time.Time, expiresAt time.Time, properties map[string]string) error {
	arch, err := osarch.ArchitectureId(architecture)
	if err != nil {
		arch = 0
	}

	tx, err := Begin(db)
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

	stmt, err = tx.Prepare(`INSERT INTO images_properties (image_id, type, key, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return err
	}

	for key, value := range properties {
		_, err = stmt.Exec(id, 0, key, value)
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

func ImageInsert(db *sql.DB, fp string, fname string, sz int64, public bool, autoUpdate bool, architecture string, createdAt time.Time, expiresAt time.Time, properties map[string]string) error {
	arch, err := osarch.ArchitectureId(architecture)
	if err != nil {
		arch = 0
	}

	tx, err := Begin(db)
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

	stmt, err := tx.Prepare(`INSERT INTO images (fingerprint, filename, size, public, auto_update, architecture, creation_date, expiry_date, upload_date) VALUES (?, ?, ?, ?, ?, ?, ?, ?, strftime("%s"))`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	result, err := stmt.Exec(fp, fname, sz, publicInt, autoUpdateInt, arch, createdAt, expiresAt)
	if err != nil {
		tx.Rollback()
		return err
	}

	if len(properties) > 0 {
		id64, err := result.LastInsertId()
		if err != nil {
			tx.Rollback()
			return err
		}
		id := int(id64)

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

	if err := TxCommit(tx); err != nil {
		return err
	}

	return nil
}

// Get the names of all storage pools on which a given image exists.
func ImageGetPools(db *sql.DB, imageFingerprint string) ([]int64, error) {
	poolID := int64(-1)
	query := "SELECT storage_pool_id FROM storage_volumes WHERE name=? AND type=?"
	inargs := []interface{}{imageFingerprint, StoragePoolVolumeTypeImage}
	outargs := []interface{}{poolID}

	result, err := QueryScan(db, query, inargs, outargs)
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
func ImageGetPoolNamesFromIDs(db *sql.DB, poolIDs []int64) ([]string, error) {
	var poolName string
	query := "SELECT name FROM storage_pools WHERE id=?"

	poolNames := []string{}
	for _, poolID := range poolIDs {
		inargs := []interface{}{poolID}
		outargs := []interface{}{poolName}

		result, err := QueryScan(db, query, inargs, outargs)
		if err != nil {
			return []string{}, err
		}

		for _, r := range result {
			poolNames = append(poolNames, r[0].(string))
		}
	}

	return poolNames, nil
}
