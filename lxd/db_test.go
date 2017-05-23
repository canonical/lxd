package main

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
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
    INSERT INTO profiles_devices_config (profile_device_id, key, value) VALUES (1, 'devicekey', 'devicevalue');
    `

type dbTestSuite struct {
	suite.Suite

	db *sql.DB
}

func (s *dbTestSuite) SetupTest() {
	s.db = s.CreateTestDb()
}

func (s *dbTestSuite) TearDownTest() {
	s.db.Close()
}

// Initialize a test in-memory DB.
func (s *dbTestSuite) CreateTestDb() (db *sql.DB) {
	// Setup logging if main() hasn't been called/when testing
	if logger.Log == nil {
		var err error
		logger.Log, err = logging.GetLogger("", "", true, true, nil)
		s.Nil(err)
	}

	var err error
	d := &Daemon{MockMode: true}
	err = initializeDbObject(d, ":memory:")
	s.Nil(err)
	db = d.db

	_, err = db.Exec(DB_FIXTURES)
	s.Nil(err)
	return // db is a named output param
}

func TestDBTestSuite(t *testing.T) {
	suite.Run(t, new(dbTestSuite))
}

func (s *dbTestSuite) Test_deleting_a_container_cascades_on_related_tables() {
	var err error
	var count int
	var statements string

	// Drop the container we just created.
	statements = `DELETE FROM containers WHERE name = 'thename';`

	_, err = s.db.Exec(statements)
	s.Nil(err, "Error deleting container!")

	// Make sure there are 0 container_profiles entries left.
	statements = `SELECT count(*) FROM containers_profiles;`
	err = s.db.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting a container didn't delete the profile association!")

	// Make sure there are 0 containers_config entries left.
	statements = `SELECT count(*) FROM containers_config;`
	err = s.db.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting a container didn't delete the associated container_config!")

	// Make sure there are 0 containers_devices entries left.
	statements = `SELECT count(*) FROM containers_devices;`
	err = s.db.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting a container didn't delete the associated container_devices!")

	// Make sure there are 0 containers_devices_config entries left.
	statements = `SELECT count(*) FROM containers_devices_config;`
	err = s.db.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting a container didn't delete the associated container_devices_config!")
}

func (s *dbTestSuite) Test_deleting_a_profile_cascades_on_related_tables() {
	var err error
	var count int
	var statements string

	// Drop the profile we just created.
	statements = `DELETE FROM profiles WHERE name = 'theprofile';`

	_, err = s.db.Exec(statements)
	s.Nil(err)

	// Make sure there are 0 container_profiles entries left.
	statements = `SELECT count(*) FROM containers_profiles WHERE profile_id = 2;`
	err = s.db.QueryRow(statements).Scan(&count)
	s.Equal(count, 0, "Deleting a profile didn't delete the container association!")

	// Make sure there are 0 profiles_devices entries left.
	statements = `SELECT count(*) FROM profiles_devices WHERE profile_id == 2;`
	err = s.db.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting a profile didn't delete the related profiles_devices!")

	// Make sure there are 0 profiles_config entries left.
	statements = `SELECT count(*) FROM profiles_config WHERE profile_id == 2;`
	err = s.db.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting a profile didn't delete the related profiles_config! There are %d left")

	// Make sure there are 0 profiles_devices_config entries left.
	statements = `SELECT count(*) FROM profiles_devices_config WHERE profile_device_id == 3;`
	err = s.db.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting a profile didn't delete the related profiles_devices_config!")
}

func (s *dbTestSuite) Test_deleting_an_image_cascades_on_related_tables() {
	var err error
	var count int
	var statements string

	// Drop the image we just created.
	statements = `DELETE FROM images;`

	_, err = s.db.Exec(statements)
	s.Nil(err)
	// Make sure there are 0 images_aliases entries left.
	statements = `SELECT count(*) FROM images_aliases;`
	err = s.db.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting an image didn't delete the image alias association!")

	// Make sure there are 0 images_properties entries left.
	statements = `SELECT count(*) FROM images_properties;`
	err = s.db.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting an image didn't delete the related images_properties!")
}

func (s *dbTestSuite) Test_initializing_db_is_idempotent() {
	var db *sql.DB
	var err error

	// This calls "createDb" once already.
	d := &Daemon{MockMode: true}
	err = initializeDbObject(d, ":memory:")
	db = d.db
	defer db.Close()

	// Let's call it a second time.
	err = createDb(db)
	s.Nil(err)
}

func (s *dbTestSuite) Test_get_schema_returns_0_on_uninitialized_db() {
	var db *sql.DB
	var err error

	db, err = sql.Open("sqlite3", ":memory:")
	s.Nil(err)
	result := dbGetSchema(db)
	s.Equal(0, result, "getSchema should return 0 on uninitialized db!")
}

func (s *dbTestSuite) Test_running_dbUpdateFromV6_adds_on_delete_cascade() {
	// Upgrading the database schema with updateFromV6 adds ON DELETE CASCADE
	// to sqlite tables that require it, and conserve the data.

	var err error
	var count int

	d := &Daemon{MockMode: true}
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
	s.Nil(err)

	// Run the upgrade from V6 code
	err = dbUpdateFromV6(5, 6, d.db)
	s.Nil(err)

	// Make sure the inserted data is still there.
	statements = `SELECT count(*) FROM containers_config;`
	err = d.db.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 1, "There should be exactly one entry in containers_config!")

	// Drop the container.
	statements = `DELETE FROM containers WHERE name = 'thename';`

	_, err = d.db.Exec(statements)
	s.Nil(err)

	// Make sure there are 0 container_profiles entries left.
	statements = `SELECT count(*) FROM containers_profiles;`
	err = d.db.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting a container didn't delete the profile association!")
}

func (s *dbTestSuite) Test_run_database_upgrades_with_some_foreign_keys_inconsistencies() {
	var db *sql.DB
	var err error
	var count int
	var statements string

	db, err = sql.Open("sqlite3", ":memory:")
	defer db.Close()
	s.Nil(err)

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
	s.Nil(err)

	// Now that we have a consistent schema, let's remove the container entry
	// *without* the ON DELETE CASCADE in place.
	statements = `DELETE FROM containers;`
	_, err = db.Exec(statements)
	s.Nil(err)

	// The "foreign key" on containers_config now points to nothing.
	// Let's run the schema upgrades.
	d := &Daemon{MockMode: true}
	d.db = db
	daemonConfigInit(db)

	err = dbUpdatesApplyAll(d.db, false, nil)
	s.Nil(err)

	result := dbGetSchema(db)
	s.Equal(result, dbGetLatestSchema(), "The schema is not at the latest version after update!")

	// Make sure there are 0 containers_config entries left.
	statements = `SELECT count(*) FROM containers_config;`
	err = db.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "updateDb did not delete orphaned child entries after adding ON DELETE CASCADE!")
}

func (s *dbTestSuite) Test_dbImageGet_finds_image_for_fingerprint() {
	var err error
	var result *api.Image

	_, result, err = dbImageGet(s.db, "fingerprint", false, false)
	s.Nil(err)
	s.NotNil(result)
	s.Equal(result.Filename, "filename")
	s.Equal(result.CreatedAt.UTC(), time.Unix(1431547174, 0).UTC())
	s.Equal(result.ExpiresAt.UTC(), time.Unix(1431547175, 0).UTC())
	s.Equal(result.UploadedAt.UTC(), time.Unix(1431547176, 0).UTC())
}

func (s *dbTestSuite) Test_dbImageGet_for_missing_fingerprint() {
	var err error

	_, _, err = dbImageGet(s.db, "unknown", false, false)
	s.Equal(err, sql.ErrNoRows)
}

func (s *dbTestSuite) Test_dbImageExists_true() {
	var err error

	exists, err := dbImageExists(s.db, "fingerprint")
	s.Nil(err)
	s.True(exists)
}

func (s *dbTestSuite) Test_dbImageExists_false() {
	var err error

	exists, err := dbImageExists(s.db, "foobar")
	s.Nil(err)
	s.False(exists)
}

func (s *dbTestSuite) Test_dbImageAliasGet_alias_exists() {
	var err error

	_, alias, err := dbImageAliasGet(s.db, "somealias", true)
	s.Nil(err)
	s.Equal(alias.Target, "fingerprint")
}

func (s *dbTestSuite) Test_dbImageAliasGet_alias_does_not_exists() {
	var err error

	_, _, err = dbImageAliasGet(s.db, "whatever", true)
	s.Equal(err, NoSuchObjectError)
}

func (s *dbTestSuite) Test_dbImageAliasAdd() {
	var err error

	err = dbImageAliasAdd(s.db, "Chaosphere", 1, "Someone will like the name")
	s.Nil(err)

	_, alias, err := dbImageAliasGet(s.db, "Chaosphere", true)
	s.Nil(err)
	s.Equal(alias.Target, "fingerprint")
}

func (s *dbTestSuite) Test_dbImageSourceGetCachedFingerprint() {
	imageID, _, err := dbImageGet(s.db, "fingerprint", false, false)
	s.Nil(err)

	err = dbImageSourceInsert(s.db, imageID, "server.remote", "simplestreams", "", "test")
	s.Nil(err)

	fingerprint, err := dbImageSourceGetCachedFingerprint(s.db, "server.remote", "simplestreams", "test")
	s.Nil(err)
	s.Equal(fingerprint, "fingerprint")
}

func (s *dbTestSuite) Test_dbImageSourceGetCachedFingerprint_no_match() {
	imageID, _, err := dbImageGet(s.db, "fingerprint", false, false)
	s.Nil(err)

	err = dbImageSourceInsert(s.db, imageID, "server.remote", "simplestreams", "", "test")
	s.Nil(err)

	_, err = dbImageSourceGetCachedFingerprint(s.db, "server.remote", "lxd", "test")
	s.Equal(err, NoSuchObjectError)
}

func (s *dbTestSuite) Test_dbContainerConfig() {
	var err error
	var result map[string]string
	var expected map[string]string

	_, err = s.db.Exec("INSERT INTO containers_config (container_id, key, value) VALUES (1, 'something', 'something else');")
	s.Nil(err)

	result, err = dbContainerConfig(s.db, 1)
	s.Nil(err)

	expected = map[string]string{"thekey": "thevalue", "something": "something else"}

	for key, value := range expected {
		s.Equal(result[key], value,
			fmt.Sprintf("Mismatching value for key %s: %s != %s", key, result[key], value))
	}
}

func (s *dbTestSuite) Test_dbProfileConfig() {
	var err error
	var result map[string]string
	var expected map[string]string

	_, err = s.db.Exec("INSERT INTO profiles_config (profile_id, key, value) VALUES (2, 'something', 'something else');")
	s.Nil(err)

	result, err = dbProfileConfig(s.db, "theprofile")
	s.Nil(err)

	expected = map[string]string{"thekey": "thevalue", "something": "something else"}

	for key, value := range expected {
		s.Equal(result[key], value,
			fmt.Sprintf("Mismatching value for key %s: %s != %s", key, result[key], value))
	}
}

func (s *dbTestSuite) Test_dbContainerProfiles() {
	var err error
	var result []string
	var expected []string

	expected = []string{"theprofile"}
	result, err = dbContainerProfiles(s.db, 1)
	s.Nil(err)

	for i := range expected {
		s.Equal(expected[i], result[i],
			fmt.Sprintf("Mismatching contents for profile list: %s != %s", result[i], expected[i]))
	}
}

func (s *dbTestSuite) Test_dbDevices_profiles() {
	var err error
	var result types.Devices
	var subresult types.Device
	var expected types.Device

	result, err = dbDevices(s.db, "theprofile", true)
	s.Nil(err)

	expected = types.Device{"type": "nic", "devicekey": "devicevalue"}
	subresult = result["devicename"]

	for key, value := range expected {
		s.Equal(subresult[key], value,
			fmt.Sprintf("Mismatching value for key %s: %v != %v", key, subresult[key], value))
	}
}

func (s *dbTestSuite) Test_dbDevices_containers() {
	var err error
	var result types.Devices
	var subresult types.Device
	var expected types.Device

	result, err = dbDevices(s.db, "thename", false)
	s.Nil(err)

	expected = types.Device{"type": "nic", "configkey": "configvalue"}
	subresult = result["somename"]

	for key, value := range expected {
		s.Equal(subresult[key], value,
			fmt.Sprintf("Mismatching value for key %s: %s != %s", key, subresult[key], value))
	}
}
