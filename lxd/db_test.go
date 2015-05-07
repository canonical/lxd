package main

import (
    "fmt"
    "testing"
    "database/sql"
)

func Test_deleting_a_container_cascades_on_related_tables(t *testing.T){
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

func Test_deleting_a_profile_cascades_on_related_tables(t *testing.T){
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

    // Check that there is exactly one containers_profile entry.
    statements = `SELECT count(*) FROM containers_profiles;`
    err = db.QueryRow(statements).Scan(&count)

    if count != 1 {
        t.Error(fmt.Sprintf("Wut? There's an exata containers_profile! There are %d", count))
    }

    // Drop the profile we just created.
    statements = `DELETE FROM profiles WHERE name = 'theprofile';`

    _, err = db.Exec(statements)
    if err != nil {
        t.Error(fmt.Sprintf("Error deleting profile! %s", err))
    }

    // Make sure there is 0 container_profiles entries left.
    statements = `SELECT count(*) FROM containers_profiles;`
    err = db.QueryRow(statements).Scan(&count)

    if count != 0 {
        t.Error(fmt.Sprintf("Deleting a profile didn't delete the container association! There are %d left", count))
    }

    // Make sure there is 0 profiles_devices entries left.
    statements = `SELECT count(*) FROM profiles_devices WHERE profile_id != 1;`
    err = db.QueryRow(statements).Scan(&count)

    if count != 0 {
        t.Error(fmt.Sprintf("Deleting a profile didn't delete the related profiles_devices! There are %d left", count))
    }

    // Make sure there is 0 profiles_config entries left.
    statements = `SELECT count(*) FROM profiles_config;`
    err = db.QueryRow(statements).Scan(&count)

    if count != 0 {
        t.Error(fmt.Sprintf("Deleting a profile didn't delete the related profiles_config! There are %d left", count))
    }

    // Make sure there is 0 profiles_devices_config entries left.
    statements = `SELECT count(*) FROM profiles_devices_config WHERE profeil_device_id != 1;`
    err = db.QueryRow(statements).Scan(&count)

    if count != 0 {
        t.Error(fmt.Sprintf("Deleting a profile didn't delete the related profiles_devices_config! There are %d left", count))
    }

}
