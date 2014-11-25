package main

import (
	"net/http"
	"syscall"

	"github.com/lxc/lxd"
	"gopkg.in/lxc/go-lxc.v2"
)

var api10 = []Command{
	fingerCmd,
	containersCmd,
	containerCmd,
	containerStateCmd,
	operationsCmd,
	operationCmd,
	operationWait,
	networksCmd,
	networkCmd,
	api10Cmd,
	listCmd,
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

func api10Get(d *Daemon, w http.ResponseWriter, r *http.Request) {
	uname := syscall.Utsname{}
	if err := syscall.Uname(&uname); err != nil {
		InternalError(w, err)
		return
	}

	fs := syscall.Statfs_t{}
	if err := syscall.Statfs(d.lxcpath, &fs); err != nil {
		InternalError(w, err)
		return
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

	SyncResponse(true, body, w)
}

var api10Cmd = Command{"", true, api10Get, nil, nil, nil}
