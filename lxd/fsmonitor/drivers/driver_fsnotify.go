package drivers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	fsn "github.com/fsnotify/fsnotify"

	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/log15"
)

var fsnotifyLoaded bool

type fsnotify struct {
	common

	watcher *fsn.Watcher
}

func (d *fsnotify) load(ctx context.Context) error {
	if fsnotifyLoaded {
		return nil
	}

	var err error

	d.watcher, err = fsn.NewWatcher()
	if err != nil {
		return fmt.Errorf("Failed to initialize fsnotify: %w", err)
	}

	err = d.watchFSTree(d.prefixPath)
	if err != nil {
		d.watcher.Close()
		fsnotifyLoaded = false
		return fmt.Errorf("Failed to watch directory %q: %w", d.prefixPath, err)
	}

	go d.getEvents(ctx)

	fsnotifyLoaded = true

	return nil
}

func (d *fsnotify) getEvents(ctx context.Context) {
	for {
		select {
		// Clean up if context is done
		case <-ctx.Done():
			d.watcher.Close()
			fsnotifyLoaded = false
			return
		case event := <-d.watcher.Events:
			// Only consider create and remove events
			if event.Op&fsn.Create == 0 && event.Op&fsn.Remove == 0 {
				continue
			}

			// If there's a new event for a directory, watch it if it's a create event.
			// Always call the handlers in case a watched file is inside the newly created or
			// now deleted directory, otherwise we'll miss the event.
			stat, err := os.Lstat(event.Name)
			if err == nil && stat.IsDir() {
				if event.Op&fsn.Create != 0 {
					d.watchFSTree(event.Name)
				}

				// Check whether there's a watch on a specific file or directory.
				d.mu.Lock()
				for path := range d.watches {
					var action Event

					if event.Op&fsn.Create != 0 {
						action = Add
					} else {
						action = Remove
					}

					for identifier, f := range d.watches[path] {
						ret := f(path, action.String())
						if !ret {
							delete(d.watches[path], identifier)

							if len(d.watches[path]) == 0 {
								delete(d.watches, path)
							}
						}
					}
				}
				d.mu.Unlock()
				continue
			}

			// Check whether there's a watch on a specific file or directory.
			d.mu.Lock()
			for path := range d.watches {
				if event.Name != path {
					continue
				}

				var action Event

				if event.Op&fsn.Create != 0 {
					action = Add
				} else {
					action = Remove
				}

				for identifier, f := range d.watches[path] {
					ret := f(path, action.String())
					if !ret {
						delete(d.watches[path], identifier)

						if len(d.watches[path]) == 0 {
							delete(d.watches, path)
						}
					}
				}

				break
			}
			d.mu.Unlock()
		case err := <-d.watcher.Errors:
			d.logger.Error("Received event error", log.Ctx{"err": err})
		}
	}
}

func (d *fsnotify) watchFSTree(path string) error {
	if !shared.PathExists(path) {
		return errors.New("Path doesn't exist")
	}

	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		// Check for errors here as we only care about directories. Files and symlinks aren't of interest for this.
		if err != nil {
			d.logger.Warn("Error visiting path", log.Ctx{"path": path, "err": err})
			return nil
		}

		// Ignore files and symlinks.
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		// Only watch on real paths.
		err = d.watcher.Add(path)
		if err != nil {
			d.logger.Warn("Failed to watch path", log.Ctx{"path": path, "err": err})
			return nil
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to watch directory tree: %w", err)
	}

	return nil
}
