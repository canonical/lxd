package dqlite

/*
import (
	"fmt"
	"os"

	"github.com/pkg/errors"
)

// Ensure that the configured directory exists and is accessible.
func ensureDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("no data dir provided in config")
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(dir, 0700); err != nil {
				return errors.Wrap(err, "failed to create data dir")
			}
			return nil
		}
		return errors.Wrap(err, "failed to access data dir")
	}
	if !info.IsDir() {
		return fmt.Errorf("data dir '%s' is not a directory", dir)
	}
	return nil
}
*/
