package sys

import (
	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
)

// OS is a high-level facade for accessing all operating-system
// level functionality that LXD uses.
type OS struct {

	// Caches of system characteristics detected at Init() time.
	Architectures []int  // Cache of detected system architectures
	LxcPath       string // Path to the $LXD_DIR/containers directory
	BackingFS     string // Backing filesystem of $LXD_DIR/containers
	IdmapSet      *idmap.IdmapSet

	MockMode bool // If true some APIs will be mocked (for testing)
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

	s.BackingFS, err = util.FilesystemDetect(s.LxcPath)
	if err != nil {
		logger.Error("Error detecting backing fs", log.Ctx{"err": err})
	}

	s.IdmapSet = util.GetIdmapSet()

	return nil
}
