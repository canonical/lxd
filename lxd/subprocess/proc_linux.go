//go:build linux && cgo

package subprocess

import (
	"syscall"

	"github.com/canonical/lxd/lxd/idmap"
)

// SetUserns allows running inside of a user namespace.
// If enableSetgroups is true, the process is allowed to use the setgroups syscall inside the user namespace.
func (p *Process) SetUserns(userns *idmap.IdmapSet, enableSetgroups bool) {
	p.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER,
		Credential: &syscall.Credential{
			Uid: uint32(0),
			Gid: uint32(0),
		},
		UidMappings:                userns.ToUidMappings(),
		GidMappings:                userns.ToGidMappings(),
		GidMappingsEnableSetgroups: enableSetgroups,
	}
}
