package sys

import (
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
)

// OS is a high-level facade for accessing all operating-system
// level functionality that LXD uses.
type OS struct {

	// Caches of system characteristics detected at Init() time.
	Architectures []int  // Cache of detected system architectures
	LxcPath       string // Path to the $LXD_DIR/containers directory
}

// NewOS returns a fresh uninitialized OS instance.
func NewOS() *OS {
	return &OS{}
}

// Init our internal data structures.
func (s *OS) Init() error {
	var err error

	s.Architectures, err = util.GetArchitectures()
	if err != nil {
		return err
	}

	s.LxcPath = shared.VarPath("containers")
	return nil
}
