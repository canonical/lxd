package main

import (
	"database/sql"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared"
)

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

// dbImageGet gets an ImageBaseInfo object from the database.
// The argument fingerprint will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one image with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func dbImageGet(db *sql.DB, fingerprint string, public bool, strictMatching bool) (*shared.ImageBaseInfo, error) {
	var err error
	var create, expire, upload *time.Time // These hold the db-returned times

	// The object we'll actually return
	image := new(shared.ImageBaseInfo)

	// These two humongous things will be filled by the call to DbQueryRowScan
	outfmt := []interface{}{&image.Id, &image.Fingerprint, &image.Filename,
		&image.Size, &image.Public, &image.Architecture,
		&create, &expire, &upload}

	var query string

	var inargs []interface{}
	if strictMatching {
		inargs = []interface{}{fingerprint}
		query = `
        SELECT
            id, fingerprint, filename, size, public, architecture,
            creation_date, expiry_date, upload_date
        FROM
            images
        WHERE fingerprint = ?`
	} else {
		inargs = []interface{}{fingerprint + "%"}
		query = `
        SELECT
            id, fingerprint, filename, size, public, architecture,
            creation_date, expiry_date, upload_date
        FROM
            images
        WHERE fingerprint LIKE ?`
	}

	if public {
		query = query + " AND public=1"
	}

	err = dbQueryRowScan(db, query, inargs, outfmt)

	if err != nil {
		return nil, err // Likely: there are no rows for this fingerprint
	}

	// Some of the dates can be nil in the DB, let's process them.
	if create != nil {
		image.CreationDate = create.Unix()
	} else {
		image.CreationDate = 0
	}
	if expire != nil {
		image.ExpiryDate = expire.Unix()
	} else {
		image.ExpiryDate = 0
	}
	// The upload date is enforced by NOT NULL in the schema, so it can never be nil.
	image.UploadDate = upload.Unix()

	return image, nil
}

func dbImageDelete(db *sql.DB, id int) error {
	tx, err := dbBegin(db)
	if err != nil {
		return err
	}

	_, _ = tx.Exec("DELETE FROM images_aliases WHERE image_id=?", id)
	_, _ = tx.Exec("DELETE FROM images_properties WHERE image_id=?", id)
	_, _ = tx.Exec("DELETE FROM images WHERE id=?", id)

	if err := txCommit(tx); err != nil {
		return err
	}

	return nil
}

// Get an image's fingerprint for a given alias name.
func dbImageAliasGet(db *sql.DB, name string) (fingerprint string, err error) {
	q := `
        SELECT
            fingerprint
        FROM images AS i JOIN images_aliases AS a
        ON a.image_id == i.id
        WHERE name=?`

	inargs := []interface{}{name}
	outfmt := []interface{}{&fingerprint}

	err = dbQueryRowScan(db, q, inargs, outfmt)

	if err == sql.ErrNoRows {
		return "", NoSuchObjectError
	}
	if err != nil {
		return "", err
	}
	return fingerprint, nil
}

func dbImageSetPublic(db *sql.DB, id int, public bool) error {
	var err error

	if public {
		_, err = dbExec(db, "UPDATE images SET public=1 WHERE id=?", id)
	} else {
		_, err = dbExec(db, "UPDATE images SET public=0 WHERE id=?", id)
	}

	return err
}

// Insert an alias into the database.
func dbImageAliasAdd(db *sql.DB, name string, imageID int, desc string) error {
	stmt := `INSERT into images_aliases (name, image_id, description) values (?, ?, ?)`
	_, err := dbExec(db, stmt, name, imageID, desc)
	return err
}

func dbImageLastAccessUpdate(db *sql.DB, fingerprint string) error {
	stmt := `UPDATE images SET last_use_date=strftime("%s") WHERE fingerprint=?`
	_, err := dbExec(db, stmt, fingerprint)
	return err
}

func dbImageLastAccessInit(db *sql.DB, fingerprint string) error {
	stmt := `UPDATE images SET cached=1, last_use_date=strftime("%s") WHERE fingerprint=?`
	_, err := dbExec(db, stmt, fingerprint)
	return err
}

func dbImageExpiryGet(db *sql.DB) (string, error) {
	q := `SELECT value FROM config WHERE key='images.remote_cache_expiry'`
	arg1 := []interface{}{}
	var expiry string
	arg2 := []interface{}{&expiry}
	err := dbQueryRowScan(db, q, arg1, arg2)
	switch err {
	case sql.ErrNoRows:
		return "10", nil
	case nil:
		return expiry, nil
	default:
		return "", err
	}
}
