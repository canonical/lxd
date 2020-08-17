// +build linux,cgo,!agent

package sys

import (
	"path/filepath"
	"sync"

	log "github.com/lxc/lxd/shared/log15"

	"github.com/lxc/lxd/lxd/cgroup"
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
	// Directories
	CacheDir string // Cache directory (e.g. /var/cache/lxd/).
	LogDir   string // Log directory (e.g. /var/log/lxd).
	VarDir   string // Data directory (e.g. /var/lib/lxd/).

	// Daemon environment
	Architectures   []int           // Cache of detected system architectures
	BackingFS       string          // Backing filesystem of $LXD_DIR/containers
	ExecPath        string          // Absolute path to the LXD executable
	IdmapSet        *idmap.IdmapSet // Information about user/group ID mapping
	InotifyWatch    InotifyInfo
	LxcPath         string // Path to the $LXD_DIR/containers directory
	MockMode        bool   // If true some APIs will be mocked (for testing)
	Nodev           bool
	RunningInUserNS bool

	// Privilege dropping
	UnprivUser  string
	UnprivUID   uint32
	UnprivGroup string
	UnprivGID   uint32

	// Apparmor features
	AppArmorAdmin     bool
	AppArmorAvailable bool
	AppArmorConfined  bool
	AppArmorStacked   bool
	AppArmorStacking  bool

	// Cgroup features
	CGInfo cgroup.Info

	// Kernel features
	CloseRange              bool
	NativeTerminals         bool
	NetnsGetifaddrs         bool
	PidFds                  bool
	PidFdSetns              bool
	SeccompListener         bool
	SeccompListenerAddfd    bool
	SeccompListenerContinue bool
	Shiftfs                 bool
	UeventInjection         bool
	VFS3Fscaps              bool

	// LXC features
	LXCFeatures map[string]bool
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

	// Detect if it is possible to run daemons as an unprivileged user and group.
	for _, user := range []string{"lxd", "nobody"} {
		uid, err := shared.UserId(user)
		if err != nil {
			continue
		}

		s.UnprivUser = user
		s.UnprivUID = uint32(uid)
		break
	}

	for _, group := range []string{"lxd", "nogroup"} {
		gid, err := shared.GroupId(group)
		if err != nil {
			continue
		}

		s.UnprivGroup = group
		s.UnprivGID = uint32(gid)
		break
	}

	s.IdmapSet = util.GetIdmapSet()
	s.ExecPath = util.GetExecPath()
	s.RunningInUserNS = shared.RunningInUserNS()

	s.initAppArmor()
	s.CGInfo = cgroup.GetInfo()

	return nil
}
