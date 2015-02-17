package main

import (
	"crypto/rand"
	"io"
	"net/http"
	"os"
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
	operationsCmd,
	operationCmd,
	operationWait,
	operationWebsocket,
	networksCmd,
	networkCmd,
	api10Cmd,
	listCmd,
	certificatesCmd,
	certificateFingerprintCmd,
}

/* Some interesting filesystems */
const (
	tmpfsSuperMagic = 0x01021994
	ext4SuperMagic  = 0xEF53
	xfsSuperMagic   = 0x58465342
	nfsSuperMagic   = 0x6969
)

/*
 * Based on: https://groups.google.com/forum/#!topic/golang-nuts/Jel8Bb-YwX8
 * there is really no better way to do this, which is unfortunate.
 */
func CharsToString(ca [65]int8) string {
	s := make([]byte, len(ca))
	var lens int
	for ; lens < len(ca); lens++ {
		if ca[lens] == 0 {
			break
		}
		s[lens] = uint8(ca[lens])
	}
	return string(s[0:lens])
}

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

		env["kernel_version"] = CharsToString(uname.Release)
		body["environment"] = env
		config := []shared.Jmap{shared.Jmap{"key": "trust-password", "value": d.hasPwd()}}
		body["config"] = config
	} else {
		body["auth"] = "untrusted"
	}

	return SyncResponse(true, body)
}

type apiPut struct {
	Config []shared.Jmap `json:"config"`
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

	for _, elt := range req.Config {
		key, err := elt.GetString("key")
		if err != nil {
			continue
		}
		if key == "trust-password" {
			password, err := elt.GetString("value")
			if err != nil {
				continue
			}

			shared.Debugf("setting new password")
			salt := make([]byte, PW_SALT_BYTES)
			_, err = io.ReadFull(rand.Reader, salt)
			if err != nil {
				return InternalError(err)
			}

			hash, err := scrypt.Key([]byte(password), salt, 1<<14, 8, 1, PW_HASH_BYTES)
			if err != nil {
				return InternalError(err)
			}

			passfname := shared.VarPath("adminpwd")
			passOut, err := os.OpenFile(passfname, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
			defer passOut.Close()
			if err != nil {
				return InternalError(err)
			}

			_, err = passOut.Write(salt)
			if err != nil {
				return InternalError(err)
			}

			_, err = passOut.Write(hash)
			if err != nil {
				return InternalError(err)
			}
		}
	}

	return EmptySyncResponse
}

var api10Cmd = Command{name: "", untrustedGet: true, get: api10Get, put: api10Put}
