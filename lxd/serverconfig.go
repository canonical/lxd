package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"

	"golang.org/x/crypto/scrypt"

	"github.com/lxc/lxd/shared"
)

const (
	PW_SALT_BYTES = 32
	PW_HASH_BYTES = 64
)

func setTrustPassword(d *Daemon, password string) error {
	shared.Debugf("setting new password")
	var value = password
	if password != "" {
		buf := make([]byte, PW_SALT_BYTES)
		_, err := io.ReadFull(rand.Reader, buf)
		if err != nil {
			return err
		}

		hash, err := scrypt.Key([]byte(password), buf, 1<<14, 8, 1, PW_HASH_BYTES)
		if err != nil {
			return err
		}

		buf = append(buf, hash...)
		value = hex.EncodeToString(buf)
	}

	err := setServerConfig(d, "core.trust_password", value)
	if err != nil {
		return err
	}

	return nil
}

func ValidServerConfigKey(k string) bool {
	switch k {
	case "core.trust_password":
		return true
	case "core.lvm_vg_name":
		return true
	case "core.lvm_thinpool_name":
		return true
	}

	return false
}

func setServerConfig(d *Daemon, key string, value string) error {
	tx, err := shared.DbBegin(d.db)
	if err != nil {
		return err
	}

	_, err = tx.Exec("DELETE FROM config WHERE key=?", key)
	if err != nil {
		tx.Rollback()
		return err
	}

	if value != "" {
		str := `INSERT INTO config (key, value) VALUES (?, ?);`
		stmt, err := tx.Prepare(str)
		if err != nil {
			tx.Rollback()
			return err
		}
		defer stmt.Close()
		_, err = stmt.Exec(key, value)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	err = shared.TxCommit(tx)
	if err != nil {
		return err
	}
	return nil
}

// returns value, exists, error
// Check 'exists' before looking at value. if exists == false, value is meaningless.
func getServerConfigValue(d *Daemon, key string) (string, bool, error) {
	var value string
	q := "SELECT value from config where key=?"
	arg1 := []interface{}{key}
	arg2 := []interface{}{&value}
	err := shared.DbQueryRowScan(d.db, q, arg1, arg2)
	switch {
	case err == sql.ErrNoRows:
		return "", false, nil
	case err != nil:
		return "", false, err
	default:
		return value, true, nil
	}
}

func getServerConfig(d *Daemon) (map[string]interface{}, error) {
	config := make(map[string]interface{})
	q := "SELECT key, value FROM config"
	rows, err := shared.DbQuery(d.db, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		rows.Scan(&key, &value)
		config[key] = value
	}

	return config, nil
}

func setLVMVolumeGroupNameConfig(d *Daemon, vgname string) error {
	if vgname != "" {
		err := shared.LVMCheckVolumeGroup(vgname)
		if err != nil {
			return err
		}
	}

	err := setServerConfig(d, "core.lvm_vg_name", vgname)
	if err != nil {
		return err
	}

	return nil
}

func setLVMThinPoolNameConfig(d *Daemon, poolname string) error {
	vgname, vgnameIsSet, err := getServerConfigValue(d, "core.lvm_vg_name")
	if err != nil {
		return fmt.Errorf("Error getting lvm_vg_name config")
	}
	if !vgnameIsSet {
		return fmt.Errorf("Can not set lvm_thinpool_name without lvm_vg_name set.")
	}

	if poolname != "" {
		poolExists, err := shared.LVMThinPoolLVExists(vgname, poolname)
		if err != nil {
			return fmt.Errorf("Error checking for thin pool '%s' in '%s': %v", poolname, vgname, err)
		}
		if !poolExists {
			return fmt.Errorf("Pool '%s' does not exist in Volume Group '%s'", poolname, vgname)
		}
	}

	err = setServerConfig(d, "core.lvm_thinpool_name", poolname)
	if err != nil {
		return err
	}

	return nil
}
