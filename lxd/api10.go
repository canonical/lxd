package main

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"syscall"

	"github.com/lxc/lxd/shared"
	"golang.org/x/crypto/scrypt"
	"gopkg.in/lxc/go-lxc.v2"
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

/* Some interesting filesystems */
const (
	tmpfsSuperMagic = 0x01021994
	ext4SuperMagic  = 0xEF53
	xfsSuperMagic   = 0x58465342
	nfsSuperMagic   = 0x6969
)

func api10Get(d *Daemon, r *http.Request) Response {
	body := shared.Jmap{"api_compat": shared.APICompat}

	if d.isTrustedClient(r) {
		body["auth"] = "trusted"

		uname := syscall.Utsname{}
		if err := syscall.Uname(&uname); err != nil {
			return InternalError(err)
		}

		fs := syscall.Statfs_t{}
		if err := syscall.Statfs(d.lxcpath, &fs); err != nil {
			return InternalError(err)
		}

		env := shared.Jmap{"lxc_version": lxc.Version(), "driver": "lxc"}

		switch fs.Type {
		case btrfsSuperMagic:
			env["backing_fs"] = "btrfs"
		case tmpfsSuperMagic:
			env["backing_fs"] = "tmpfs"
		case ext4SuperMagic:
			env["backing_fs"] = "ext4"
		case xfsSuperMagic:
			env["backing_fs"] = "xfs"
		case nfsSuperMagic:
			env["backing_fs"] = "nfs"
		default:
			env["backing_fs"] = fs.Type
		}

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
		config := shared.Jmap{"trust-password": d.hasPwd()}
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

func api10Put(d *Daemon, r *http.Request) Response {
	req := apiPut{}

	if err := shared.ReadToJSON(r.Body, &req); err != nil {
		return BadRequest(err)
	}

	for key, value := range req.Config {
		if key == "trust-password" {
			password, _ := value.(string)

			shared.Debugf("setting new password")
			salt := make([]byte, PW_SALT_BYTES)
			_, err := io.ReadFull(rand.Reader, salt)
			if err != nil {
				return InternalError(err)
			}

			hash, err := scrypt.Key([]byte(password), salt, 1<<14, 8, 1, PW_HASH_BYTES)
			if err != nil {
				return InternalError(err)
			}

			dbHash := hex.EncodeToString(append(salt, hash...))

			tx, err := d.db.Begin()
			if err != nil {
				return InternalError(err)
			}

			_, err = tx.Exec("DELETE FROM config WHERE key=\"core.trust_password\"")
			if err != nil {
				tx.Rollback()
				return InternalError(err)
			}

			str := `INSERT INTO config (key, value) VALUES ("core.trust_password", ?);`
			stmt, err := tx.Prepare(str)
			if err != nil {
				tx.Rollback()
				return InternalError(err)
			}
			defer stmt.Close()
			_, err = stmt.Exec(dbHash)
			if err != nil {
				tx.Rollback()
				return InternalError(err)
			}

			err = shared.TxCommit(tx)
			if err != nil {
				return InternalError(err)
			}
		}
	}

	return EmptySyncResponse
}

var api10Cmd = Command{name: "", untrustedGet: true, get: api10Get, put: api10Put}
