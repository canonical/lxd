package main

import (
	"database/sql"
	"fmt"
	"testing"
)

func Test_deleting_a_container_cascades_on_related_tables(t *testing.T) {
	var db *sql.DB
	var err error
	var count int

	db, err = initializeDbObject(":memory:")
	defer db.Close()

	if err != nil {
		t.Error(err)
	}

	// Insert a container and a related profile.
	statements := `
    INSERT INTO containers (name, architecture, type) VALUES ('thename', 1, 1);
    INSERT INTO profiles (name) VALUES ('theprofile');
    INSERT INTO containers_profiles (container_id, profile_id) VALUES (1, 2);
    INSERT INTO containers_config (container_id, key, value) VALUES (1, 'thekey', 'thevalue');
    INSERT INTO containers_devices (container_id, name) VALUES (1, 'somename');
    INSERT INTO containers_devices_config (key, value, container_device_id) VALUES ('configkey', 'configvalue', 1);`

	_, err = db.Exec(statements)
	if err != nil {
		t.Error(err)
	}

	// Drop the container we just created.
	statements = `DELETE FROM containers WHERE name = 'thename';`

	_, err = db.Exec(statements)
	if err != nil {
		t.Error(fmt.Sprintf("Error deleting container! %s", err))
	}

	// Make sure there are 0 container_profiles entries left.
	statements = `SELECT count(*) FROM containers_profiles;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Error(fmt.Sprintf("Deleting a container didn't delete the profile association! There are %d left", count))
	}

	// Make sure there are 0 containers_config entries left.
	statements = `SELECT count(*) FROM containers_config;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Error(fmt.Sprintf("Deleting a container didn't delete the associated container_config! There are %d left", count))
	}

	// Make sure there are 0 containers_devices entries left.
	statements = `SELECT count(*) FROM containers_devices;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Error(fmt.Sprintf("Deleting a container didn't delete the associated container_devices! There are %d left", count))
	}

	// Make sure there are 0 containers_devices_config entries left.
	statements = `SELECT count(*) FROM containers_devices_config;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Error(fmt.Sprintf("Deleting a container didn't delete the associated container_devices_config! There are %d left", count))
	}

}

func Test_deleting_a_profile_cascades_on_related_tables(t *testing.T) {
	var db *sql.DB
	var err error
	var count int

	db, err = initializeDbObject(":memory:")
	defer db.Close()

	if err != nil {
		t.Error(err)
	}

	// Insert a container and a related profile. Dont't forget that the profile
	// we insert is profile ID 2 (there is a default profile already).
	statements := `
    INSERT INTO containers (name, architecture, type) VALUES ('thename', 1, 1);
    INSERT INTO profiles (name) VALUES ('theprofile');
    INSERT INTO containers_profiles (container_id, profile_id) VALUES (1, 2);
    INSERT INTO profiles_devices (name, profile_id) VALUES ('somename', 2);
    INSERT INTO profiles_config (key, value, profile_id) VALUES ('thekey', 'thevalue', 2);
    INSERT INTO profiles_devices_config (profile_device_id, key, value) VALUES (1, 'something', 'boring');`

	_, err = db.Exec(statements)
	if err != nil {
		t.Error(err)
	}

	// Drop the profile we just created.
	statements = `DELETE FROM profiles WHERE name = 'theprofile';`

	_, err = db.Exec(statements)
	if err != nil {
		t.Error(fmt.Sprintf("Error deleting profile! %s", err))
	}

	// Make sure there are 0 container_profiles entries left.
	statements = `SELECT count(*) FROM containers_profiles;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Error(fmt.Sprintf("Deleting a profile didn't delete the container association! There are %d left", count))
	}

	// Make sure there are 0 profiles_devices entries left.
	statements = `SELECT count(*) FROM profiles_devices WHERE profile_id != 1;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Error(fmt.Sprintf("Deleting a profile didn't delete the related profiles_devices! There are %d left", count))
	}

	// Make sure there are 0 profiles_config entries left.
	statements = `SELECT count(*) FROM profiles_config;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Error(fmt.Sprintf("Deleting a profile didn't delete the related profiles_config! There are %d left", count))
	}

	// Make sure there are 0 profiles_devices_config entries left.
	statements = `SELECT count(*) FROM profiles_devices_config WHERE profeil_device_id != 1;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Error(fmt.Sprintf("Deleting a profile didn't delete the related profiles_devices_config! There are %d left", count))
	}

}

func Test_deleting_an_image_cascades_on_related_tables(t *testing.T) {
	var db *sql.DB
	var err error
	var count int

	db, err = initializeDbObject(":memory:")
	defer db.Close()

	if err != nil {
		t.Error(err)
	}

	statements := `
    INSERT INTO images (fingerprint, filename, size, architecture, creation_date, expiry_date, upload_date) VALUES ('fingerprint', 'filename', 0, 0, 0, 0, 0);
    INSERT INTO images_aliases (name, image_id, description) VALUES ('somename', 1, 'some description');
    INSERT INTO images_properties (image_id, type, key, value) VALUES (1, 0, 'thekey', 'some value');`

	_, err = db.Exec(statements)
	if err != nil {
		t.Error(err)
	}

	// Drop the image we just created.
	statements = `DELETE FROM images;`

	_, err = db.Exec(statements)
	if err != nil {
		t.Error(fmt.Sprintf("Error deleting image! %s", err))
	}

	// Make sure there are 0 images_aliases entries left.
	statements = `SELECT count(*) FROM images_aliases;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Error(fmt.Sprintf("Deleting an image didn't delete the image alias association! There are %d left", count))
	}

	// Make sure there are 0 images_properties entries left.
	statements = `SELECT count(*) FROM images_properties;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Error(fmt.Sprintf("Deleting an image didn't delete the related images_properties! There are %d left", count))
	}
}

func Test_initializing_db_is_indempotent(t *testing.T) {
	var db *sql.DB
	var err error

	// This calls "createDb" once already.
	db, err = initializeDbObject(":memory:")
	defer db.Close()

	// Let's call it a second time.
	err = createDb(db)
	if err != nil {
		t.Error("The database schema is not indempotent.")
	}
}

func Test_get_schema_returns_0_on_uninitialized_db(t *testing.T) {
	var db *sql.DB
	var err error

	db, err = sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Error(err)
	}
	var result int = getSchema(db)

	if result != 0 {
		t.Error("getSchema should return 0 on uninitialized db!")
	}
}
