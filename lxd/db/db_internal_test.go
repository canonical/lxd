//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

const fixtures string = `
    INSERT INTO instances (node_id, name, architecture, type, project_id, description) VALUES (1, 'thename', 1, 1, 1, '');
    INSERT INTO profiles (name, project_id, description) VALUES ('theprofile', 1, '');
    INSERT INTO instances_profiles (instance_id, profile_id) VALUES (1, 2);
    INSERT INTO instances_config (instance_id, key, value) VALUES (1, 'thekey', 'thevalue');
    INSERT INTO instances_devices (instance_id, name, type) VALUES (1, 'somename', 1);
    INSERT INTO instances_devices_config (key, value, instance_device_id) VALUES ('configkey', 'configvalue', 1);
    INSERT INTO images (fingerprint, filename, size, architecture, creation_date, expiry_date, upload_date, auto_update, project_id) VALUES ('fingerprint', 'filename', 1024, 0,  1431547174,  1431547175,  1431547176, 1, 1);
    INSERT INTO images_aliases (name, image_id, description, project_id, description) VALUES ('somealias', 1, 'some description', 1, '');
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

// SetupTest initializes a test database and a transaction, and then populates it with predefined data.
func (s *dbTestSuite) SetupTest() {
	s.db, s.cleanup = s.CreateTestDb()

	tx, commit := s.CreateTestTx()
	defer commit()

	_, err := tx.Exec(fixtures)
	s.Nil(err)
}

// TearDownTest cleans up resources and data used in the test after completion.
func (s *dbTestSuite) TearDownTest() {
	s.cleanup()
}

// Initializes a test in-memory DB.
func (s *dbTestSuite) CreateTestDb() (*Cluster, func()) {
	var err error

	// Setup logging if main() hasn't been called/when testing
	if logger.Log == nil {
		err = logger.InitLogger("", "", true, true, nil)
		s.Nil(err)
	}

	db, cleanup := NewTestCluster(s.T())
	return db, cleanup
}

// Enters a transaction on the test in-memory DB.
func (s *dbTestSuite) CreateTestTx() (*sql.Tx, func()) {
	tx, err := s.db.DB().Begin()
	s.Nil(err)
	commit := func() {
		s.Nil(tx.Commit())
	}

	return tx, commit
}

// TestDBTestSuite executes all tests within the dbTestSuite.
func TestDBTestSuite(t *testing.T) {
	suite.Run(t, new(dbTestSuite))
}

// Test_deleting_a_container_cascades_on_related_tables checks
// if deleting a container removes all its associated entries from related tables.
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

// Test_deleting_a_profile_cascades_on_related_tables ensures that deleting a
// profile removes all its associated entries from related tables.
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

// Test_deleting_an_image_cascades_on_related_tables ensures removal of image-related entries upon image deletion.
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

// Test_ImageGet_finds_image_for_fingerprint checks if the correct image data is fetched using a specific fingerprint.
func (s *dbTestSuite) Test_ImageGet_finds_image_for_fingerprint() {
	var err error
	var result *api.Image
	project := "default"

	_, result, err = s.db.GetImage("fingerprint", cluster.ImageFilter{Project: &project})
	s.Nil(err)
	s.NotNil(result)
	s.Equal(result.Filename, "filename")
	s.Equal(result.CreatedAt.UTC(), time.Unix(1431547174, 0).UTC())
	s.Equal(result.ExpiresAt.UTC(), time.Unix(1431547175, 0).UTC())
	s.Equal(result.UploadedAt.UTC(), time.Unix(1431547176, 0).UTC())
}

// Test_ImageGet_for_missing_fingerprint validates the error handling when attempting to fetch a non-existent image.
func (s *dbTestSuite) Test_ImageGet_for_missing_fingerprint() {
	project := "default"
	var err error

	_, _, err = s.db.GetImage("unknown", cluster.ImageFilter{Project: &project})
	s.True(api.StatusErrorCheck(err, http.StatusNotFound))
}

// Test_ImageExists_true verifies the function correctly identifies the existence of a specified image.
func (s *dbTestSuite) Test_ImageExists_true() {
	var err error

	exists, err := s.db.ImageExists("default", "fingerprint")
	s.Nil(err)
	s.True(exists)
}

// Test_ImageExists_false confirms the function accurately reports the non-existence of a specified image.
func (s *dbTestSuite) Test_ImageExists_false() {
	var err error

	exists, err := s.db.ImageExists("default", "foobar")
	s.Nil(err)
	s.False(exists)
}

// Test_GetImageAlias_alias_exists checks if the function correctly retrieves an existing image alias.
func (s *dbTestSuite) Test_GetImageAlias_alias_exists() {
	_ = s.db.Transaction(context.Background(), func(ctx context.Context, tx *ClusterTx) error {
		_, alias, err := tx.GetImageAlias(ctx, "default", "somealias", true)
		s.Nil(err)
		s.Equal(alias.Target, "fingerprint")

		return nil
	})
}

// Test_GetImageAlias_alias_does_not_exists ensures the function properly handles a missing image alias request.
func (s *dbTestSuite) Test_GetImageAlias_alias_does_not_exists() {
	_ = s.db.Transaction(context.Background(), func(ctx context.Context, tx *ClusterTx) error {
		_, _, err := tx.GetImageAlias(ctx, "default", "whatever", true)
		s.True(api.StatusErrorCheck(err, http.StatusNotFound))

		return nil
	})
}

// Test_CreateImageAlias validates successful image alias creation and its subsequent retrieval.
func (s *dbTestSuite) Test_CreateImageAlias() {
	_ = s.db.Transaction(context.Background(), func(ctx context.Context, tx *ClusterTx) error {
		err := tx.CreateImageAlias(ctx, "default", "Chaosphere", 1, "Someone will like the name")
		s.Nil(err)

		_, alias, err := tx.GetImageAlias(ctx, "default", "Chaosphere", true)
		s.Nil(err)
		s.Equal(alias.Target, "fingerprint")

		return nil
	})
}

// Test_GetCachedImageSourceFingerprint verifies the fingerprint retrieval of a cached image source.
func (s *dbTestSuite) Test_GetCachedImageSourceFingerprint() {
	project := "default"
	imageID, _, err := s.db.GetImage("fingerprint", cluster.ImageFilter{Project: &project})
	s.Nil(err)

	err = s.db.CreateImageSource(imageID, "server.remote", "simplestreams", "", "test")
	s.Nil(err)

	_ = s.db.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		fingerprint, err := tx.GetCachedImageSourceFingerprint(ctx, "server.remote", "simplestreams", "test", "container", 0)
		s.Nil(err)
		s.Equal(fingerprint, "fingerprint")
		return nil
	})
}

// Test_GetCachedImageSourceFingerprint_no_match ensures that an error is returned for non-matching cached image source.
func (s *dbTestSuite) Test_GetCachedImageSourceFingerprint_no_match() {
	project := "default"
	imageID, _, err := s.db.GetImage("fingerprint", cluster.ImageFilter{Project: &project})
	s.Nil(err)

	err = s.db.CreateImageSource(imageID, "server.remote", "simplestreams", "", "test")
	s.Nil(err)

	_ = s.db.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		_, err = tx.GetCachedImageSourceFingerprint(ctx, "server.remote", "lxd", "test", "container", 0)
		s.True(api.StatusErrorCheck(err, http.StatusNotFound))
		return nil
	})
}
