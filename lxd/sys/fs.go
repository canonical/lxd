package sys

import (
	"os"

	"github.com/lxc/lxd/shared"
)

// Make sure all our directories are available.
func (s *OS) initDirs() error {
	dirs := []struct {
		path string
		mode os.FileMode
	}{
		{shared.VarPath(), 0711},
		{shared.CachePath(), 0700},
		{shared.VarPath("containers"), 0711},
		{shared.VarPath("devices"), 0711},
		{shared.VarPath("devlxd"), 0755},
		{shared.VarPath("images"), 0700},
		{shared.LogPath(), 0700},
		{shared.VarPath("security"), 0700},
		{shared.VarPath("shmounts"), 0711},
		{shared.VarPath("snapshots"), 0700},
		{shared.VarPath("networks"), 0711},
		{shared.VarPath("disks"), 0700},
		{shared.VarPath("storage-pools"), 0711},
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir.path, dir.mode); err != nil {
			return err
		}
	}
	return nil
}
