package main

import (
	"crypto/rand"
	"io"
	"net/http"
	"os"
	"syscall"

	"github.com/lxc/lxd"
	"golang.org/x/crypto/scrypt"
	"gopkg.in/lxc/go-lxc.v2"
)

var api10 = []Command{
	fingerCmd,
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
	trustCmd,
	trustFingerprintCmd,
}

/* Some interesting filesystems */
const (
	BTRFS_SUPER_MAGIC = 0x9123683E
	TMPFS_MAGIC       = 0x01021994
	EXT4_SUPER_MAGIC  = 0xEF53
	XFS_SUPER_MAGIC   = 0x58465342
	NFS_SUPER_MAGIC   = 0x6969
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
	uname := syscall.Utsname{}
	if err := syscall.Uname(&uname); err != nil {
		return InternalError(err)
	}

	fs := syscall.Statfs_t{}
	if err := syscall.Statfs(d.lxcpath, &fs); err != nil {
		return InternalError(err)
	}

	env := lxd.Jmap{"lxc_version": lxc.Version(), "driver": "lxc"}

	switch fs.Type {
	case BTRFS_SUPER_MAGIC:
		env["backing_fs"] = "btrfs"
	case TMPFS_MAGIC:
		env["backing_fs"] = "tmpfs"
	case EXT4_SUPER_MAGIC:
		env["backing_fs"] = "ext4"
	case XFS_SUPER_MAGIC:
		env["backing_fs"] = "xfs"
	case NFS_SUPER_MAGIC:
		env["backing_fs"] = "nfs"
	default:
		env["backing_fs"] = fs.Type
	}

	env["kernel_version"] = CharsToString(uname.Release)

	config := []lxd.Jmap{lxd.Jmap{"key": "trust-password", "value": d.hasPwd()}}
	body := lxd.Jmap{"config": config, "environment": env}

	return SyncResponse(true, body)
}

type apiPut struct {
	Config []lxd.Jmap `json:"config"`
}

const (
	PW_SALT_BYTES = 32
	PW_HASH_BYTES = 64
)

func api10Put(d *Daemon, r *http.Request) Response {
	req := apiPut{}

	if err := lxd.ReadToJSON(r.Body, &req); err != nil {
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

			lxd.Debugf("setting new password")
			salt := make([]byte, PW_SALT_BYTES)
			_, err = io.ReadFull(rand.Reader, salt)
			if err != nil {
				return InternalError(err)
			}

			hash, err := scrypt.Key([]byte(password), salt, 1<<14, 8, 1, PW_HASH_BYTES)
			if err != nil {
				return InternalError(err)
			}

			passfname := lxd.VarPath("adminpwd")
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

var api10Cmd = Command{"", true, false, api10Get, api10Put, nil, nil}
