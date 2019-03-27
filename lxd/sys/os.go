package sys

import (
	"path/filepath"
	"sync"

	log "github.com/lxc/lxd/shared/log15"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
)

// InotifyTargetInfo records the inotify information associated with a given
// inotify target
type InotifyTargetInfo struct {
	Mask uint32
	Wd   int
	Path string
}

// InotifyInfo records the inotify information associated with a given
// inotify instance
type InotifyInfo struct {
	Fd int
	sync.RWMutex
	Targets map[string]*InotifyTargetInfo
}

// OS is a high-level facade for accessing all operating-system
// level functionality that LXD uses.
type OS struct {
	VarDir   string // Data directory (e.g. /var/lib/lxd/).
	CacheDir string // Cache directory (e.g. /var/cache/lxd/).
	LogDir   string // Log directory (e.g. /var/log/lxd).

	// Caches of system characteristics detected at Init() time.
	Architectures           []int           // Cache of detected system architectures
	LxcPath                 string          // Path to the $LXD_DIR/containers directory
	BackingFS               string          // Backing filesystem of $LXD_DIR/containers
	IdmapSet                *idmap.IdmapSet // Information about user/group ID mapping
	ExecPath                string          // Absolute path to the LXD executable
	RunningInUserNS         bool
	AppArmorAvailable       bool
	AppArmorStacking        bool
	AppArmorStacked         bool
	AppArmorAdmin           bool
	AppArmorConfined        bool
	CGroupBlkioController   bool
	CGroupCPUController     bool
	CGroupCPUacctController bool
	CGroupCPUsetController  bool
	CGroupDevicesController bool
	CGroupFreezerController bool
	CGroupMemoryController  bool
	CGroupNetPrioController bool
	CGroupPidsController    bool
	CGroupSwapAccounting    bool
	InotifyWatch            InotifyInfo
	NetnsGetifaddrs         bool
	UeventInjection         bool
	VFS3Fscaps              bool
	Shiftfs                 bool

	MockMode bool // If true some APIs will be mocked (for testing)
}

// DefaultOS returns a fresh uninitialized OS instance with default values.
func DefaultOS() *OS {
	newOS := &OS{
		VarDir:   shared.VarPath(),
		CacheDir: shared.CachePath(),
		LogDir:   shared.LogPath(),
	}
	newOS.InotifyWatch.Fd = -1
	newOS.InotifyWatch.Targets = make(map[string]*InotifyTargetInfo)
	return newOS
}

// Init our internal data structures.
func (s *OS) Init() error {
	err := s.initDirs()
	if err != nil {
		return err
	}

	s.Architectures, err = util.GetArchitectures()
	if err != nil {
		return err
	}

	s.LxcPath = filepath.Join(s.VarDir, "containers")

	s.BackingFS, err = util.FilesystemDetect(s.LxcPath)
	if err != nil {
		logger.Error("Error detecting backing fs", log.Ctx{"err": err})
	}

	s.IdmapSet = util.GetIdmapSet()
	s.ExecPath = util.GetExecPath()
	s.RunningInUserNS = shared.RunningInUserNS()

	s.initAppArmor()
	s.initCGroup()

	return nil
}
