// +build linux,cgo,!agent

package db

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
)

const fixtures string = `
    INSERT INTO instances (node_id, name, architecture, type, project_id) VALUES (1, 'thename', 1, 1, 1);
    INSERT INTO profiles (name, project_id) VALUES ('theprofile', 1);
    INSERT INTO instances_profiles (instance_id, profile_id) VALUES (1, 2);
    INSERT INTO instances_config (instance_id, key, value) VALUES (1, 'thekey', 'thevalue');
    INSERT INTO instances_devices (instance_id, name, type) VALUES (1, 'somename', 1);
    INSERT INTO instances_devices_config (key, value, instance_device_id) VALUES ('configkey', 'configvalue', 1);
    INSERT INTO images (fingerprint, filename, size, architecture, creation_date, expiry_date, upload_date, auto_update, project_id) VALUES ('fingerprint', 'filename', 1024, 0,  1431547174,  1431547175,  1431547176, 1, 1);
    INSERT INTO images_aliases (name, image_id, description, project_id) VALUES ('somealias', 1, 'some description', 1);
    INSERT INTO images_properties (image_id, type, key, value) VALUES (1, 0, 'thekey', 'some value');
    INSERT INTO profiles_config (profile_id, key, value) VALUES (2, 'thekey', 'thevalue');
    INSERT INTO profiles_devices (profile_id, name, type) VALUES (2, 'devicename', 1);
    INSERT INTO profiles_devices_config (profile_device_id, key, value) VALUES (1, 'devicekey', 'devicevalue');
    `

type dbTestSuite struct {
	suite.Suite

	db      *Cluster
	cleanup func()
}

func (s *dbTestSuite) SetupTest() {
	s.db, s.cleanup = s.CreateTestDb()

	tx, commit := s.CreateTestTx()
	defer commit()

	_, err := tx.Exec(fixtures)
	s.Nil(err)
}

func (s *dbTestSuite) TearDownTest() {
	s.cleanup()
}

// Initialize a test in-memory DB.
func (s *dbTestSuite) CreateTestDb() (*Cluster, func()) {
	var err error

	// Setup logging if main() hasn't been called/when testing
	if logger.Log == nil {
		logger.Log, err = logging.GetLogger("", "", true, true, nil)
		s.Nil(err)
	}

	db, cleanup := NewTestCluster(s.T())
	return db, cleanup
}

// Enter a transaction on the test in-memory DB.
func (s *dbTestSuite) CreateTestTx() (*sql.Tx, func()) {
	tx, err := s.db.DB().Begin()
	s.Nil(err)
	commit := func() {
		s.Nil(tx.Commit())
	}
	return tx, commit
}

func TestDBTestSuite(t *testing.T) {
	suite.Run(t, new(dbTestSuite))
}

func (s *dbTestSuite) Test_deleting_a_container_cascades_on_related_tables() {
	var err error
	var count int
	var statements string

	// Drop the container we just created.
	statements = `DELETE FROM instances WHERE name = 'thename';`

	tx, commit := s.CreateTestTx()
	defer commit()

	_, err = tx.Exec(statements)
	s.Nil(err, "Error deleting container!")

	// Make sure there are 0 container_profiles entries left.
	statements = `SELECT count(*) FROM instances_profiles;`
	err = tx.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting a container didn't delete the profile association!")

	// Make sure there are 0 containers_config entries left.
	statements = `SELECT count(*) FROM instances_config;`
	err = tx.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting a container didn't delete the associated container_config!")

	// Make sure there are 0 containers_devices entries left.
	statements = `SELECT count(*) FROM instances_devices;`
	err = tx.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting a container didn't delete the associated container_devices!")

	// Make sure there are 0 containers_devices_config entries left.
	statements = `SELECT count(*) FROM instances_devices_config;`
	err = tx.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting a container didn't delete the associated container_devices_config!")
}

func (s *dbTestSuite) Test_deleting_a_profile_cascades_on_related_tables() {
	var err error
	var count int
	var statements string

	// Drop the profile we just created.
	statements = `DELETE FROM profiles WHERE name = 'theprofile';`

	tx, commit := s.CreateTestTx()
	defer commit()

	_, err = tx.Exec(statements)
	s.Nil(err)

	// Make sure there are 0 container_profiles entries left.
	statements = `SELECT count(*) FROM instances_profiles WHERE profile_id = 2;`
	err = tx.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting a profile didn't delete the container association!")

	// Make sure there are 0 profiles_devices entries left.
	statements = `SELECT count(*) FROM profiles_devices WHERE profile_id == 2;`
	err = tx.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting a profile didn't delete the related profiles_devices!")

	// Make sure there are 0 profiles_config entries left.
	statements = `SELECT count(*) FROM profiles_config WHERE profile_id == 2;`
	err = tx.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting a profile didn't delete the related profiles_config! There are %d left")

	// Make sure there are 0 profiles_devices_config entries left.
	statements = `SELECT count(*) FROM profiles_devices_config WHERE profile_device_id == 3;`
	err = tx.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting a profile didn't delete the related profiles_devices_config!")
}

func (s *dbTestSuite) Test_deleting_an_image_cascades_on_related_tables() {
	var err error
	var count int
	var statements string

	// Drop the image we just created.
	statements = `DELETE FROM images;`

	tx, commit := s.CreateTestTx()
	defer commit()

	_, err = tx.Exec(statements)
	s.Nil(err)
	// Make sure there are 0 images_aliases entries left.
	statements = `SELECT count(*) FROM images_aliases;`
	err = tx.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting an image didn't delete the image alias association!")

	// Make sure there are 0 images_properties entries left.
	statements = `SELECT count(*) FROM images_properties;`
	err = tx.QueryRow(statements).Scan(&count)
	s.Nil(err)
	s.Equal(count, 0, "Deleting an image didn't delete the related images_properties!")
}

func (s *dbTestSuite) Test_ImageGet_finds_image_for_fingerprint() {
	var err error
	var result *api.Image

	_, result, err = s.db.GetImage("default", "fingerprint", false)
	s.Nil(err)
	s.NotNil(result)
	s.Equal(result.Filename, "filename")
	s.Equal(result.CreatedAt.UTC(), time.Unix(1431547174, 0).UTC())
	s.Equal(result.ExpiresAt.UTC(), time.Unix(1431547175, 0).UTC())
	s.Equal(result.UploadedAt.UTC(), time.Unix(1431547176, 0).UTC())
}

func (s *dbTestSuite) Test_ImageGet_for_missing_fingerprint() {
	var err error

	_, _, err = s.db.GetImage("default", "unknown", false)
	s.Equal(err, ErrNoSuchObject)
}

func (s *dbTestSuite) Test_ImageExists_true() {
	var err error

	exists, err := s.db.ImageExists("default", "fingerprint")
	s.Nil(err)
	s.True(exists)
}

func (s *dbTestSuite) Test_ImageExists_false() {
	var err error

	exists, err := s.db.ImageExists("default", "foobar")
	s.Nil(err)
	s.False(exists)
}

func (s *dbTestSuite) Test_GetImageAlias_alias_exists() {
	var err error

	_, alias, err := s.db.GetImageAlias("default", "somealias", true)
	s.Nil(err)
	s.Equal(alias.Target, "fingerprint")
}

func (s *dbTestSuite) Test_GetImageAlias_alias_does_not_exists() {
	var err error

	_, _, err = s.db.GetImageAlias("default", "whatever", true)
	s.Equal(err, ErrNoSuchObject)
}

func (s *dbTestSuite) Test_CreateImageAlias() {
	var err error

	err = s.db.CreateImageAlias("default", "Chaosphere", 1, "Someone will like the name")
	s.Nil(err)

	_, alias, err := s.db.GetImageAlias("default", "Chaosphere", true)
	s.Nil(err)
	s.Equal(alias.Target, "fingerprint")
}

func (s *dbTestSuite) Test_GetCachedImageSourceFingerprint() {
	imageID, _, err := s.db.GetImage("default", "fingerprint", false)
	s.Nil(err)

	err = s.db.CreateImageSource(imageID, "server.remote", "simplestreams", "", "test")
	s.Nil(err)

	fingerprint, err := s.db.GetCachedImageSourceFingerprint("server.remote", "simplestreams", "test", "container", 0)
	s.Nil(err)
	s.Equal(fingerprint, "fingerprint")
}

func (s *dbTestSuite) Test_GetCachedImageSourceFingerprint_no_match() {
	imageID, _, err := s.db.GetImage("default", "fingerprint", false)
	s.Nil(err)

	err = s.db.CreateImageSource(imageID, "server.remote", "simplestreams", "", "test")
	s.Nil(err)

	_, err = s.db.GetCachedImageSourceFingerprint("server.remote", "lxd", "test", "container", 0)
	s.Equal(err, ErrNoSuchObject)
}
