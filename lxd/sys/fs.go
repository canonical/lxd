package sys

import (
	"os"
	"path/filepath"
)

// Make sure all our directories are available.
func (s *OS) initDirs() error {
	dirs := []struct {
		path string
		mode os.FileMode
	}{
		{s.VarDir, 0711},
		{s.CacheDir, 0700},
		{filepath.Join(s.VarDir, "containers"), 0711},
		{filepath.Join(s.VarDir, "devices"), 0711},
		{filepath.Join(s.VarDir, "devlxd"), 0755},
		{filepath.Join(s.VarDir, "images"), 0700},
		{s.LogDir, 0700},
		{filepath.Join(s.VarDir, "security"), 0700},
		{filepath.Join(s.VarDir, "shmounts"), 0711},
		{filepath.Join(s.VarDir, "snapshots"), 0700},
		{filepath.Join(s.VarDir, "networks"), 0711},
		{filepath.Join(s.VarDir, "disks"), 0700},
		{filepath.Join(s.VarDir, "storage-pools"), 0711},
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir.path, dir.mode); err != nil {
			return err
		}
	}
	return nil
}
