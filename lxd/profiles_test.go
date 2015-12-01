package main

import (
	"database/sql"
	"testing"
)

func Test_removing_a_profile_deletes_associated_configuration_entries(t *testing.T) {
	var db *sql.DB
	var err error

	d := &Daemon{}
	err = initializeDbObject(d, ":memory:")
	db = d.db

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
		t.Fatal(err)
	}

	// Delete the profile we just created with dbProfileDelete
	err = dbProfileDelete(db, "theprofile")
	if err != nil {
		t.Fatal(err)
	}

	// Make sure there are 0 profiles_devices entries left.
	devices, err := dbDevices(d.db, "theprofile", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 0 {
		t.Errorf("Deleting a profile didn't delete the related profiles_devices! There are %d left", len(devices))
	}

	// Make sure there are 0 profiles_config entries left.
	config, err := dbProfileConfig(d.db, "theprofile")
	if err == nil {
		t.Fatal("found the profile!")
	}

	if len(config) != 0 {
		t.Errorf("Deleting a profile didn't delete the related profiles_config! There are %d left", len(config))
	}
}
