package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"syscall"

	"golang.org/x/crypto/scrypt"
	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"
)

var api10 = []Command{
	containersCmd,
	containerCmd,
	containerStateCmd,
	containerFileCmd,
	containerSnapshotsCmd,
	containerSnapshotCmd,
	containerExecCmd,
	aliasCmd,
	aliasesCmd,
	imageCmd,
	imagesCmd,
	imagesExportCmd,
	imagesSecretCmd,
	operationsCmd,
	operationCmd,
	operationWait,
	operationWebsocket,
	networksCmd,
	networkCmd,
	api10Cmd,
	certificatesCmd,
	certificateFingerprintCmd,
	profilesCmd,
	profileCmd,
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

func api10Get(d *Daemon, r *http.Request) Response {
	body := shared.Jmap{"api_compat": shared.APICompat}

	if d.isTrustedClient(r) {
		body["auth"] = "trusted"

		uname := syscall.Utsname{}
		if err := syscall.Uname(&uname); err != nil {
			return InternalError(err)
		}

		backing_fs, err := shared.GetFilesystem(d.lxcpath)
		if err != nil {
			return InternalError(err)
		}

		env := shared.Jmap{
			"lxc_version": lxc.Version(),
			"lxd_version": shared.Version,
			"driver":      "lxc",
			"backing_fs":  backing_fs}

		/*
		 * Based on: https://groups.google.com/forum/#!topic/golang-nuts/Jel8Bb-YwX8
		 * there is really no better way to do this, which is
		 * unfortunate. Also, we ditch the more accepted CharsToString
		 * version in that thread, since it doesn't seem as portable,
		 * viz. github issue #206.
		 */
		kernelVersion := ""
		for _, c := range uname.Release {
			if c == 0 {
				break
			}
			kernelVersion += string(byte(c))
		}

		env["kernel_version"] = kernelVersion
		body["environment"] = env

		serverConfig, err := getServerConfig(d)
		if err != nil {
			return InternalError(err)
		}

		config := shared.Jmap{}

		for key, value := range serverConfig {
			if key == "core.trust_password" {
				config[key] = true
			} else {
				config[key] = value
			}
		}

		body["config"] = config
	} else {
		body["auth"] = "untrusted"
	}

	return SyncResponse(true, body)
}

type apiPut struct {
	Config shared.Jmap `json:"config"`
}

const (
	PW_SALT_BYTES = 32
	PW_HASH_BYTES = 64
)

func setTrustPassword(d *Daemon, password string) error {

	shared.Debugf("setting new password")
	var value = password
	if password != "" {
		salt := make([]byte, PW_SALT_BYTES)
		_, err := io.ReadFull(rand.Reader, salt)
		if err != nil {
			return err
		}

		hash, err := scrypt.Key([]byte(password), salt, 1<<14, 8, 1, PW_HASH_BYTES)
		if err != nil {
			return err
		}

		rawvalue := append(salt, hash...)
		value = hex.EncodeToString(rawvalue)
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

func api10Put(d *Daemon, r *http.Request) Response {
	req := apiPut{}

	if err := shared.ReadToJSON(r.Body, &req); err != nil {
		return BadRequest(err)
	}

	for key, value := range req.Config {
		if key == "core.trust_password" {
			err := setTrustPassword(d, value.(string))
			if err != nil {
				return InternalError(err)
			}
		} else if ValidServerConfigKey(key) {
			err := setServerConfig(d, key, value.(string))
			if err != nil {
				return InternalError(err)
			}
		} else {
			return BadRequest(fmt.Errorf("Bad server config key: '%s'", key))
		}
	}

	return EmptySyncResponse
}

var api10Cmd = Command{name: "", untrustedGet: true, get: api10Get, put: api10Put}
