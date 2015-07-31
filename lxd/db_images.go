package main

import (
	"database/sql"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/lxc/lxd/shared"
)

// dbImageGet gets an ImageBaseInfo object from the database.
// The argument fingerprint will be queried with a LIKE query, means you can
// pass a shortform and will get the full fingerprint.
// There can never be more than one image with a given fingerprint, as it is
// enforced by a UNIQUE constraint in the schema.
func dbImageGet(db *sql.DB, fingerprint string, public bool) (*shared.ImageBaseInfo, error) {
	var err error
	var create, expire, upload *time.Time // These hold the db-returned times

	// The object we'll actually return
	image := new(shared.ImageBaseInfo)

	// These two humongous things will be filled by the call to DbQueryRowScan
	inargs := []interface{}{fingerprint + "%"}
	outfmt := []interface{}{&image.Id, &image.Fingerprint, &image.Filename,
		&image.Size, &image.Public, &image.Architecture,
		&create, &expire, &upload}

	query := `
        SELECT
            id, fingerprint, filename, size, public, architecture,
            creation_date, expiry_date, upload_date
        FROM
            images
        WHERE fingerprint like ?`

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
	_, _ = tx.Exec("DELETE FROM images_properties WHERE image_id?", id)
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

// Insert an alias into the database.
func dbImageAliasAdd(db *sql.DB, name string, imageID int, desc string) error {
	stmt := `INSERT into images_aliases (name, image_id, description) values (?, ?, ?)`
	_, err := dbExec(db, stmt, name, imageID, desc)
	return err
}
