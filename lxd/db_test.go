package main

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logging"
)

const DB_FIXTURES string = `
    INSERT INTO containers (name, architecture, type) VALUES ('thename', 1, 1);
    INSERT INTO profiles (name) VALUES ('theprofile');
    INSERT INTO containers_profiles (container_id, profile_id) VALUES (1, 2);
    INSERT INTO containers_config (container_id, key, value) VALUES (1, 'thekey', 'thevalue');
    INSERT INTO containers_devices (container_id, name, type) VALUES (1, 'somename', 1);
    INSERT INTO containers_devices_config (key, value, container_device_id) VALUES ('configkey', 'configvalue', 1);
    INSERT INTO images (fingerprint, filename, size, architecture, creation_date, expiry_date, upload_date) VALUES ('fingerprint', 'filename', 1024, 0,  1431547174,  1431547175,  1431547176);
    INSERT INTO images_aliases (name, image_id, description) VALUES ('somealias', 1, 'some description');
    INSERT INTO images_properties (image_id, type, key, value) VALUES (1, 0, 'thekey', 'some value');
    INSERT INTO profiles_config (profile_id, key, value) VALUES (2, 'thekey', 'thevalue');
    INSERT INTO profiles_devices (profile_id, name, type) VALUES (2, 'devicename', 1);
    INSERT INTO profiles_devices_config (profile_device_id, key, value) VALUES (2, 'devicekey', 'devicevalue');
    `

//  This Helper will initialize a test in-memory DB.
func createTestDb(t *testing.T) (db *sql.DB) {
	// Setup logging if main() hasn't been called/when testing
	if shared.Log == nil {
		var err error
		shared.Log, err = logging.GetLogger("", "", true, true, nil)
		if err != nil {
			t.Fatal(err)
		}
	}

	var err error
	d := &Daemon{IsMock: true}
	err = initializeDbObject(d, ":memory:")
	db = d.db

	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(DB_FIXTURES)
	if err != nil {
		t.Fatal(err)
	}
	return // db is a named output param
}

func Test_deleting_a_container_cascades_on_related_tables(t *testing.T) {
	var db *sql.DB
	var err error
	var count int
	var statements string

	// Insert a container and a related profile.
	db = createTestDb(t)
	defer db.Close()

	// Drop the container we just created.
	statements = `DELETE FROM containers WHERE name = 'thename';`

	_, err = db.Exec(statements)
	if err != nil {
		t.Errorf("Error deleting container! %s", err)
	}

	// Make sure there are 0 container_profiles entries left.
	statements = `SELECT count(*) FROM containers_profiles;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Errorf("Deleting a container didn't delete the profile association! There are %d left", count)
	}

	// Make sure there are 0 containers_config entries left.
	statements = `SELECT count(*) FROM containers_config;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Errorf("Deleting a container didn't delete the associated container_config! There are %d left", count)
	}

	// Make sure there are 0 containers_devices entries left.
	statements = `SELECT count(*) FROM containers_devices;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Errorf("Deleting a container didn't delete the associated container_devices! There are %d left", count)
	}

	// Make sure there are 0 containers_devices_config entries left.
	statements = `SELECT count(*) FROM containers_devices_config;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Errorf("Deleting a container didn't delete the associated container_devices_config! There are %d left", count)
	}

}

func Test_deleting_a_profile_cascades_on_related_tables(t *testing.T) {
	var db *sql.DB
	var err error
	var count int
	var statements string

	// Insert a container and a related profile.
	db = createTestDb(t)
	defer db.Close()

	// Drop the profile we just created.
	statements = `DELETE FROM profiles WHERE name = 'theprofile';`

	_, err = db.Exec(statements)
	if err != nil {
		t.Errorf("Error deleting profile! %s", err)
	}

	// Make sure there are 0 container_profiles entries left.
	statements = `SELECT count(*) FROM containers_profiles WHERE profile_id = 2;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Errorf("Deleting a profile didn't delete the container association! There are %d left", count)
	}

	// Make sure there are 0 profiles_devices entries left.
	statements = `SELECT count(*) FROM profiles_devices WHERE profile_id == 2;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Errorf("Deleting a profile didn't delete the related profiles_devices! There are %d left", count)
	}

	// Make sure there are 0 profiles_config entries left.
	statements = `SELECT count(*) FROM profiles_config WHERE profile_id == 2;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Errorf("Deleting a profile didn't delete the related profiles_config! There are %d left", count)
	}

	// Make sure there are 0 profiles_devices_config entries left.
	statements = `SELECT count(*) FROM profiles_devices_config WHERE profile_device_id == 2;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Errorf("Deleting a profile didn't delete the related profiles_devices_config! There are %d left", count)
	}

}

func Test_deleting_an_image_cascades_on_related_tables(t *testing.T) {
	var db *sql.DB
	var err error
	var count int
	var statements string

	db = createTestDb(t)
	defer db.Close()

	// Drop the image we just created.
	statements = `DELETE FROM images;`

	_, err = db.Exec(statements)
	if err != nil {
		t.Errorf("Error deleting image! %s", err)
	}

	// Make sure there are 0 images_aliases entries left.
	statements = `SELECT count(*) FROM images_aliases;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Errorf("Deleting an image didn't delete the image alias association! There are %d left", count)
	}

	// Make sure there are 0 images_properties entries left.
	statements = `SELECT count(*) FROM images_properties;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Errorf("Deleting an image didn't delete the related images_properties! There are %d left", count)
	}
}

func Test_initializing_db_is_indempotent(t *testing.T) {
	var db *sql.DB
	var err error

	// This calls "createDb" once already.
	d := &Daemon{IsMock: true}
	err = initializeDbObject(d, ":memory:")
	db = d.db

	defer db.Close()

	// Let's call it a second time.
	err = createDb(db)
	if err != nil {
		t.Errorf("The database schema is not indempotent, err='%s'", err)
	}
}

func Test_get_schema_returns_0_on_uninitialized_db(t *testing.T) {
	var db *sql.DB
	var err error

	db, err = sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Error(err)
	}
	result := dbGetSchema(db)

	if result != 0 {
		t.Error("getSchema should return 0 on uninitialized db!")
	}
}

func Test_running_dbUpdateFromV6_adds_on_delete_cascade(t *testing.T) {
	// Upgrading the database schema with updateFromV6 adds ON DELETE CASCADE
	// to sqlite tables that require it, and conserve the data.

	var err error
	var count int

	d := &Daemon{IsMock: true}
	err = initializeDbObject(d, ":memory:")
	defer d.db.Close()

	statements := `
CREATE TABLE IF NOT EXISTS containers (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    architecture INTEGER NOT NULL,
    type INTEGER NOT NULL,
    power_state INTEGER NOT NULL DEFAULT 0,
    ephemeral INTEGER NOT NULL DEFAULT 0,
    UNIQUE (name)
);
CREATE TABLE IF NOT EXISTS containers_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (container_id) REFERENCES containers (id),
    UNIQUE (container_id, key)
);

INSERT INTO containers (name, architecture, type) VALUES ('thename', 1, 1);
INSERT INTO containers_config (container_id, key, value) VALUES (1, 'thekey', 'thevalue');`

	_, err = d.db.Exec(statements)
	if err != nil {
		t.Error(err)
	}

	// Run the upgrade from V6 code
	err = dbUpdateFromV6(d.db)

	// Make sure the inserted data is still there.
	statements = `SELECT count(*) FROM containers_config;`
	err = d.db.QueryRow(statements).Scan(&count)

	if count != 1 {
		t.Fatalf("There should be exactly one entry in containers_config! There are %d.", count)
	}

	// Drop the container.
	statements = `DELETE FROM containers WHERE name = 'thename';`

	_, err = d.db.Exec(statements)
	if err != nil {
		t.Errorf("Error deleting container! %s", err)
	}

	// Make sure there are 0 container_profiles entries left.
	statements = `SELECT count(*) FROM containers_profiles;`
	err = d.db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Errorf("Deleting a container didn't delete the profile association! There are %d left", count)
	}
}

func Test_run_database_upgrades_with_some_foreign_keys_inconsistencies(t *testing.T) {
	var db *sql.DB
	var err error
	var count int
	var statements string

	db, err = sql.Open("sqlite3", ":memory:")
	defer db.Close()

	if err != nil {
		t.Fatal(err)
	}

	// This schema is a part of schema rev 1.
	statements = `
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
CREATE TABLE schema (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    version INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (version)
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
);
CREATE TABLE certificates (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    fingerprint VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    certificate TEXT NOT NULL,
    UNIQUE (fingerprint)
);
INSERT INTO schema (version, updated_at) values (1, "now");
INSERT INTO containers (name, architecture, type) VALUES ('thename', 1, 1);
INSERT INTO containers_config (container_id, key, value) VALUES (1, 'thekey', 'thevalue');`

	_, err = db.Exec(statements)
	if err != nil {
		t.Fatal("Error creating schema!")
	}

	// Now that we have a consistent schema, let's remove the container entry
	// *without* the ON DELETE CASCADE in place.
	statements = `DELETE FROM containers;`
	_, err = db.Exec(statements)
	if err != nil {
		t.Fatal("Error truncating the container table!")
	}

	// The "foreign key" on containers_config now points to nothing.
	// Let's run the schema upgrades.
	d := &Daemon{IsMock: true}
	d.db = db
	err = dbUpdate(d, 1)

	if err != nil {
		t.Error("Error upgrading database schema!")
		t.Fatal(err)
	}

	result := dbGetSchema(db)
	if result != DB_CURRENT_VERSION {
		t.Fatal(fmt.Sprintf("The schema is not at the latest version after update! Found: %d, should be: %d", result, DB_CURRENT_VERSION))
	}

	// Make sure there are 0 containers_config entries left.
	statements = `SELECT count(*) FROM containers_config;`
	err = db.QueryRow(statements).Scan(&count)

	if count != 0 {
		t.Fatal("updateDb did not delete orphaned child entries after adding ON DELETE CASCADE!")
	}

}

func Test_dbImageGet_finds_image_for_fingerprint(t *testing.T) {
	var db *sql.DB
	var err error
	var result *shared.ImageBaseInfo

	db = createTestDb(t)
	defer db.Close()

	result, err = dbImageGet(db, "fingerprint", false, false)

	if err != nil {
		t.Fatal(err)
	}

	if result == nil {
		t.Fatal("No image returned!")
	}

	if result.Filename != "filename" {
		t.Fatal("Filename should be set.")
	}

	if result.CreationDate != 1431547174 {
		t.Fatal(result.CreationDate)
	}

	if result.ExpiryDate != 1431547175 { // It was short lived
		t.Fatal(result.ExpiryDate)
	}

	if result.UploadDate != 1431547176 {
		t.Fatal(result.UploadDate)
	}
}

func Test_dbImageGet_for_missing_fingerprint(t *testing.T) {
	var db *sql.DB
	var err error

	db = createTestDb(t)
	defer db.Close()

	_, err = dbImageGet(db, "unknown", false, false)

	if err != sql.ErrNoRows {
		t.Fatal("Wrong err type returned")
	}
}

func Test_dbImageAliasGet_alias_exists(t *testing.T) {
	var db *sql.DB
	var err error
	var result string

	db = createTestDb(t)
	defer db.Close()

	result, err = dbImageAliasGet(db, "somealias")

	if err != nil {
		t.Fatal(err)
	}

	if result != "fingerprint" {
		t.Fatal("Fingerprint is not the expected fingerprint!")
	}

}

func Test_dbImageAliasGet_alias_does_not_exists(t *testing.T) {
	var db *sql.DB
	var err error

	db = createTestDb(t)
	defer db.Close()

	_, err = dbImageAliasGet(db, "whatever")

	if err != NoSuchObjectError {
		t.Fatal("Error should be NoSuchObjectError")
	}

}

func Test_dbImageAliasAdd(t *testing.T) {
	var db *sql.DB
	var err error
	var result string

	db = createTestDb(t)
	defer db.Close()

	err = dbImageAliasAdd(db, "Chaosphere", 1, "Someone will like the name")
	if err != nil {
		t.Fatal("Error inserting Image alias.")
	}

	result, err = dbImageAliasGet(db, "Chaosphere")
	if err != nil {
		t.Fatal(err)
	}

	if result != "fingerprint" {
		t.Fatal("Couldn't retrieve newly created alias.")
	}
}

func Test_dbContainerConfig(t *testing.T) {
	var db *sql.DB
	var err error
	var result map[string]string
	var expected map[string]string

	db = createTestDb(t)
	defer db.Close()

	_, err = db.Exec("INSERT INTO containers_config (container_id, key, value) VALUES (1, 'something', 'something else');")

	result, err = dbContainerConfig(db, 1)
	if err != nil {
		t.Fatal(err)
	}

	expected = map[string]string{"thekey": "thevalue", "something": "something else"}

	for key, value := range expected {
		if result[key] != value {
			t.Errorf("Mismatching value for key %s: %s != %s", key, result[key], value)
		}
	}
}

func Test_dbProfileConfig(t *testing.T) {
	var db *sql.DB
	var err error
	var result map[string]string
	var expected map[string]string

	db = createTestDb(t)
	defer db.Close()

	_, err = db.Exec("INSERT INTO profiles_config (profile_id, key, value) VALUES (2, 'something', 'something else');")

	result, err = dbProfileConfig(db, "theprofile")
	if err != nil {
		t.Fatal(err)
	}

	expected = map[string]string{"thekey": "thevalue", "something": "something else"}

	for key, value := range expected {
		if result[key] != value {
			t.Errorf("Mismatching value for key %s: %s != %s", key, result[key], value)
		}
	}
}

func Test_dbContainerProfiles(t *testing.T) {
	var db *sql.DB
	var err error
	var result []string
	var expected []string

	db = createTestDb(t)
	defer db.Close()

	expected = []string{"theprofile"}
	result, err = dbContainerProfiles(db, 1)
	if err != nil {
		t.Fatal(err)
	}

	for i := range expected {
		if expected[i] != result[i] {
			t.Fatal(fmt.Sprintf("Mismatching contents for profile list: %s != %s", result[i], expected[i]))
		}
	}
}

func Test_dbDevices_profiles(t *testing.T) {
	var db *sql.DB
	var err error
	var result shared.Devices
	var subresult shared.Device
	var expected shared.Device

	db = createTestDb(t)
	defer db.Close()

	result, err = dbDevices(db, "theprofile", true)
	if err != nil {
		t.Fatal(err)
	}

	expected = shared.Device{"type": "nic", "devicekey": "devicevalue"}
	subresult = result["devicename"]

	for key, value := range expected {
		if subresult[key] != value {
			t.Errorf("Mismatching value for key %s: %v != %v", key, subresult[key], value)
		}
	}

}

func Test_dbDevices_containers(t *testing.T) {
	var db *sql.DB
	var err error
	var result shared.Devices
	var subresult shared.Device
	var expected shared.Device

	db = createTestDb(t)
	defer db.Close()

	result, err = dbDevices(db, "thename", false)
	if err != nil {
		t.Fatal(err)
	}

	expected = shared.Device{"type": "nic", "configkey": "configvalue"}
	subresult = result["somename"]

	for key, value := range expected {
		if subresult[key] != value {
			t.Errorf("Mismatching value for key %s: %s != %s", key, subresult[key], value)
		}
	}

}
