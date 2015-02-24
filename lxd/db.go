package main

import (
	"database/sql"
	"fmt"

	"github.com/lxc/lxd/shared"
	_ "github.com/mattn/go-sqlite3"
)

const DB_CURRENT_VERSION int = 2

func createDb(p string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", p)
	if err != nil {
		return nil, fmt.Errorf("Error creating database: %s\n", err)
	}
	stmt := `
CREATE TABLE certificates (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    fingerprint VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    certificate TEXT NOT NULL,
    UNIQUE (fingerprint)
);
CREATE TABLE containers (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    architecture INTEGER NOT NULL,
    type INTEGER NOT NULL,
    UNIQUE (name)
);
CREATE TABLE containers_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (container_id) REFERENCES containers (id),
    UNIQUE (container_id, key)
);
CREATE TABLE images (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    fingerprint VARCHAR(255) NOT NULL,
    filename VARCHAR(255) NOT NULL,
    size INTEGER NOT NULL,
    public INTEGER NOT NULL DEFAULT 0,
    architecture INTEGER NOT NULL,
    creation_date DATETIME,
    expiry_date DATETIME,
    upload_date DATETIME NOT NULL,
    UNIQUE (fingerprint)
);
CREATE TABLE images_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    image_id INTEGER NOT NULL,
    description VARCHAR(255),
    FOREIGN KEY (image_id) REFERENCES images (id),
    UNIQUE (name)
);
CREATE TABLE images_properties (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (image_id) REFERENCES images (id)
);
CREATE TABLE schema (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
);
INSERT INTO schema (version, updated_at) values (?, strftime("%s"));`

	_, err = db.Exec(stmt, DB_CURRENT_VERSION)
	return db, err
}

func getSchema(db *sql.DB) (int, error) {
	rows, err := db.Query("SELECT max(version) FROM schema")
	if err != nil {
		return 0, nil
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		rows.Scan(&v)
		return v, nil
	}
	return 0, nil
}

/* Yeah we can do htis in a more clever way */
func updateFromV1(db *sql.DB) error {
	// v1..v2 adds images aliases
	stmt := `
CREATE TABLE images_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    image_id INTEGER NOT NULL,
    description VARCHAR(255),
    FOREIGN KEY (image_id) REFERENCES images (id),
    UNIQUE (name)
);
INSERT INTO schema (version, updated_at) values (?, strftime("%s"));`
	_, err := db.Exec(stmt, 2)
	return err
}

func updateFromV0(db *sql.DB) error {
	// v0..v1 adds schema table
	stmt := `
CREATE TABLE schema (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
);
INSERT INTO schema (version, updated_at) values (?, strftime("%s"));`
	_, err := db.Exec(stmt, 1)
	return err
}

func updateDb(db *sql.DB, prev_version int) error {
	if prev_version < 0 || prev_version > DB_CURRENT_VERSION {
		return fmt.Errorf("Bad database version: %d\n", prev_version)
	}
	if prev_version == DB_CURRENT_VERSION {
		return nil
	}
	var err error
	if prev_version < 1 {
		err = updateFromV0(db)
		if err != nil {
			return err
		}
	}
	if prev_version < 2 {
		err = updateFromV1(db)
		if err != nil {
			return err
		}
	}
	return nil
}

func initDb(d *Daemon) error {
	dbpath := shared.VarPath("lxd.db")
	var db *sql.DB
	var err error
	if !shared.PathExists(dbpath) {
		db, err = createDb(dbpath)
		if err != nil {
			return err
		}
	} else {
		db, err = sql.Open("sqlite3", dbpath)
		if err != nil {
			fmt.Printf("Error opening lxd database\n")
			return err
		}
	}

	d.db = db

	v, err := getSchema(db)
	if err != nil {
		return fmt.Errorf("Bad database, or database too new for this lxd version")
	}

	if v != DB_CURRENT_VERSION {
		err = updateDb(db, v)
		if err != nil {
			return err
		}
	}

	return nil
}

func dbImageGet(d *Daemon, name string) (int, error) {
	rows, err := d.db.Query("SELECT id FROM images WHERE fingerprint=?", name)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		rows.Scan(&id)
		return id, nil
	}
	return 0, fmt.Errorf("No such image")
}

func dbImageGetById(d *Daemon, id int) (string, error) {
	rows, err := d.db.Query("SELECT fingerprint FROM images WHERE id=?", id)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	for rows.Next() {
		var fp string
		rows.Scan(&fp)
		return fp, nil
	}
	return "", fmt.Errorf("No such image")
}

func dbAliasGet(d *Daemon, name string) (int, int, error) {
	rows, err := d.db.Query("SELECT id, image_id FROM images_aliases WHERE name=?", name)
	if err != nil {
		return 0, 0, err
	}

	for rows.Next() {
		var id int
		var imageid int
		rows.Scan(&id, &imageid)
		return id, imageid, nil
	}
	return 0, 0, fmt.Errorf("No such image")
}

func dbAddAlias(d *Daemon, name string, tgt int, desc string) error {
	stmt := `INSERT into images_aliases (name, image_id, description) values (?, ?, ?)`
	_, err := d.db.Exec(stmt, name, tgt, desc)
	return err
}
