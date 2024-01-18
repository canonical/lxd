//go:build (!linux || !cgo) && !windows

package subprocess

import (
	"github.com/canonical/lxd/lxd/idmap"
)

// SetUserns allows running inside of a user namespace.
func (p *Process) SetUserns(userns *idmap.IdmapSet) {
	return
}
