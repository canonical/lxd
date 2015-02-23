package main

import (
	"database/sql"
	"fmt"

	"github.com/lxc/lxd/shared"
	_ "github.com/mattn/go-sqlite3"
)

func createDb(p string) error {
	db, err := sql.Open("sqlite3", p)
	if err != nil {
		return fmt.Errorf("Error creating database: %s\n", err)
	}
	defer db.Close()
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
CREATE TABLE images_properties (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (image_id) REFERENCES images (id)
);`

	_, err = db.Exec(stmt)
	return err
}

func initDb(d *Daemon) error {
	dbpath := shared.VarPath("lxd.db")
	if !shared.PathExists(dbpath) {
		err := createDb(dbpath)
		if err != nil {
			return err
		}
	}

	/* TODO - scheck schema and update if necessary */

	/* Open our persistant db connection */
	db, err := sql.Open("sqlite3", dbpath)
	if err != nil {
		fmt.Printf("Error opening lxd database\n")
		return err
	}

	d.db = db

	return nil
}
