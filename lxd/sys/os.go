package sys

import (
	"github.com/lxc/lxd/lxd/util"
)

// OS is a high-level facade for accessing all operating-system
// level functionality that LXD uses.
type OS struct {
	Architectures []int // Cache of detected system architectures
}

// Init our internal data structures.
func (s *OS) Init() error {
	var err error

	s.Architectures, err = util.GetArchitectures()
	if err != nil {
		return err
	}

	return nil
}
