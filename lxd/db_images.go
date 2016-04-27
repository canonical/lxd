package main

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared"
)

var dbImageSourceProtocol = map[int]string{
	0: "lxd",
	1: "direct",
	2: "simplestreams",
}

func dbImagesGet(db *sql.DB, public bool) ([]string, error) {
	q := "SELECT fingerprint FROM images"
	if public == true {
		q = "SELECT fingerprint FROM images WHERE public=1"
	}

	var fp string
	inargs := []interface{}{}
	outfmt := []interface{}{fp}
	dbResults, err := dbQueryScan(db, q, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	results := []string{}
	for _, r := range dbResults {
		results = append(results, r[0].(string))
	}

	return results, nil
}

func dbImagesGetExpired(db *sql.DB, expiry int64) ([]string, error) {
	q := `SELECT fingerprint FROM images WHERE cached=1 AND creation_date<=strftime('%s', date('now', '-` + fmt.Sprintf("%d", expiry) + ` day'))`

	var fp string
	inargs := []interface{}{}
	outfmt := []interface{}{fp}
	dbResults, err := dbQueryScan(db, q, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	results := []string{}
	for _, r := range dbResults {
		results = append(results, r[0].(string))
	}

	return results, nil
}

func dbImageSourceInsert(db *sql.DB, imageId int, server string, protocol string, certificate string, alias string) error {
	stmt := `INSERT INTO images_source (image_id, server, protocol, certificate, alias) values (?, ?, ?, ?, ?)`

	protocolInt := -1
	for protoInt, protoString := range dbImageSourceProtocol {
		if protoString == protocol {
			protocolInt = protoInt
		}
	}

	if protocolInt == -1 {
		return fmt.Errorf("Invalid protocol: %s", protocol)
	}

	_, err := dbExec(db, stmt, imageId, server, protocolInt, certificate, alias)
	return err
}

func dbImageSourceGet(db *sql.DB, imageId int) (int, shared.ImageSource, error) {
	q := `SELECT id, server, protocol, certificate, alias FROM images_source WHERE image_id=?`

	id := 0
	protocolInt := -1
	result := shared.ImageSource{}

	arg1 := []interface{}{imageId}
	arg2 := []interface{}{&id, &result.Server, &protocolInt, &result.Certificate, &result.Alias}
	err := dbQueryRowScan(db, q, arg1, arg2)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, shared.ImageSource{}, NoSuchObjectError
		}

		return -1, shared.ImageSource{}, err
	}

	protocol, found := dbImageSourceProtocol[protocolInt]
	if !found {
		return -1, shared.ImageSource{}, fmt.Errorf("Invalid protocol: %d", protocolInt)
	}

	result.Protocol = protocol

	return id, result, nil

}

// dbImageGet gets an ImageBaseInfo object from the database.
// The argument fingerprint will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one image with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func dbImageGet(db *sql.DB, fingerprint string, public bool, strictMatching bool) (int, *shared.ImageInfo, error) {
	var err error
	var create, expire, used, upload *time.Time // These hold the db-returned times

	// The object we'll actually return
	image := shared.ImageInfo{}
	id := -1
	arch := -1

	// These two humongous things will be filled by the call to DbQueryRowScan
	outfmt := []interface{}{&id, &image.Fingerprint, &image.Filename,
		&image.Size, &image.Cached, &image.Public, &image.AutoUpdate, &arch,
		&create, &expire, &used, &upload}

	var query string

	var inargs []interface{}
	if strictMatching {
		inargs = []interface{}{fingerprint}
		query = `
        SELECT
            id, fingerprint, filename, size, cached, public, auto_update, architecture,
            creation_date, expiry_date, last_use_date, upload_date
        FROM
            images
        WHERE fingerprint = ?`
	} else {
		inargs = []interface{}{fingerprint + "%"}
		query = `
        SELECT
            id, fingerprint, filename, size, cached, public, auto_update, architecture,
            creation_date, expiry_date, last_use_date, upload_date
        FROM
            images
        WHERE fingerprint LIKE ?`
	}

	if public {
		query = query + " AND public=1"
	}

	err = dbQueryRowScan(db, query, inargs, outfmt)

	if err != nil {
		return -1, nil, err // Likely: there are no rows for this fingerprint
	}

	// Some of the dates can be nil in the DB, let's process them.
	if create != nil {
		image.CreationDate = *create
	} else {
		image.CreationDate = time.Time{}
	}

	if expire != nil {
		image.ExpiryDate = *expire
	} else {
		image.ExpiryDate = time.Time{}
	}

	if used != nil {
		image.LastUsedDate = *used
	} else {
		image.LastUsedDate = time.Time{}
	}

	image.Architecture, _ = shared.ArchitectureName(arch)

	// The upload date is enforced by NOT NULL in the schema, so it can never be nil.
	image.UploadDate = *upload

	// Get the properties
	q := "SELECT key, value FROM images_properties where image_id=?"
	var key, value, name, desc string
	inargs = []interface{}{id}
	outfmt = []interface{}{key, value}
	results, err := dbQueryScan(db, q, inargs, outfmt)
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
	results, err = dbQueryScan(db, q, inargs, outfmt)
	if err != nil {
		return -1, nil, err
	}

	aliases := []shared.ImageAlias{}
	for _, r := range results {
		name = r[0].(string)
		desc = r[0].(string)
		a := shared.ImageAlias{Name: name, Description: desc}
		aliases = append(aliases, a)
	}

	image.Aliases = aliases

	_, source, err := dbImageSourceGet(db, id)
	if err == nil {
		image.Source = &source
	}

	return id, &image, nil
}

func dbImageDelete(db *sql.DB, id int) error {
	tx, err := dbBegin(db)
	if err != nil {
		return err
	}

	_, _ = tx.Exec("DELETE FROM images_aliases WHERE image_id=?", id)
	_, _ = tx.Exec("DELETE FROM images_properties WHERE image_id=?", id)
	_, _ = tx.Exec("DELETE FROM images_source WHERE image_id=?", id)
	_, _ = tx.Exec("DELETE FROM images WHERE id=?", id)

	if err := txCommit(tx); err != nil {
		return err
	}

	return nil
}

func dbImageAliasGet(db *sql.DB, name string, isTrustedClient bool) (int, shared.ImageAliasesEntry, error) {
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

	arg1 := []interface{}{name}
	arg2 := []interface{}{&id, &fingerprint, &description}
	err := dbQueryRowScan(db, q, arg1, arg2)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, shared.ImageAliasesEntry{}, NoSuchObjectError
		}

		return -1, shared.ImageAliasesEntry{}, err
	}

	return id, shared.ImageAliasesEntry{Name: name, Target: fingerprint, Description: description}, nil
}

func dbImageAliasRename(db *sql.DB, id int, name string) error {
	_, err := dbExec(db, "UPDATE images_aliases SET name=? WHERE id=?", name, id)
	return err
}

func dbImageAliasDelete(db *sql.DB, name string) error {
	_, err := dbExec(db, "DELETE FROM images_aliases WHERE name=?", name)
	return err
}

func dbImageAliasesMove(db *sql.DB, source int, destination int) error {
	_, err := dbExec(db, "UPDATE images_aliases SET image_id=? WHERE image_id=?", destination, source)
	return err
}

// Insert an alias ento the database.
func dbImageAliasAdd(db *sql.DB, name string, imageID int, desc string) error {
	stmt := `INSERT INTO images_aliases (name, image_id, description) values (?, ?, ?)`
	_, err := dbExec(db, stmt, name, imageID, desc)
	return err
}

func dbImageAliasUpdate(db *sql.DB, id int, imageID int, desc string) error {
	stmt := `UPDATE images_aliases SET image_id=?, description=? WHERE id=?`
	_, err := dbExec(db, stmt, imageID, desc, id)
	return err
}

func dbImageLastAccessUpdate(db *sql.DB, fingerprint string, date time.Time) error {
	stmt := `UPDATE images SET last_use_date=? WHERE fingerprint=?`
	_, err := dbExec(db, stmt, date, fingerprint)
	return err
}

func dbImageLastAccessInit(db *sql.DB, fingerprint string) error {
	stmt := `UPDATE images SET cached=1, last_use_date=strftime("%s") WHERE fingerprint=?`
	_, err := dbExec(db, stmt, fingerprint)
	return err
}

func dbImageUpdate(db *sql.DB, id int, fname string, sz int64, public bool, autoUpdate bool, architecture string, creationDate time.Time, expiryDate time.Time, properties map[string]string) error {
	arch, err := shared.ArchitectureId(architecture)
	if err != nil {
		arch = 0
	}

	tx, err := dbBegin(db)
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

	_, err = stmt.Exec(fname, sz, publicInt, autoUpdateInt, arch, creationDate, expiryDate, id)
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

	if err := txCommit(tx); err != nil {
		return err
	}

	return nil
}

func dbImageInsert(db *sql.DB, fp string, fname string, sz int64, public bool, autoUpdate bool, architecture string, creationDate time.Time, expiryDate time.Time, properties map[string]string) error {
	arch, err := shared.ArchitectureId(architecture)
	if err != nil {
		arch = 0
	}

	tx, err := dbBegin(db)
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

	result, err := stmt.Exec(fp, fname, sz, publicInt, autoUpdateInt, arch, creationDate, expiryDate)
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

	if err := txCommit(tx); err != nil {
		return err
	}

	return nil
}
