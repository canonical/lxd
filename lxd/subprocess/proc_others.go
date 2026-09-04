//go:build (!linux || !cgo) && !windows

package subprocess

import (
	"github.com/canonical/lxd/lxd/idmap"
)

// SetUserns allows running inside of a user namespace.
// If enableSetgroups is true, the process is allowed to use the setgroups syscall inside the user namespace.
func (p *Process) SetUserns(userns *idmap.IdmapSet, enableSetgroups bool) {
	return
}
