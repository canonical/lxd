package drivers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	in "k8s.io/utils/inotify"

	"github.com/canonical/lxd/lxd/fsmonitor"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

type inotify struct {
	common

	watcher *in.Watcher
}

var inotifyEventToFSMonitorEvent = map[uint32]fsmonitor.Event{
	in.InCreate:     fsmonitor.EventAdd,
	in.InDelete:     fsmonitor.EventRemove,
	in.InDeleteSelf: fsmonitor.EventRemove,
	in.InCloseWrite: fsmonitor.EventWrite,
	in.InMovedTo:    fsmonitor.EventRename,
}

var fsMonitorEventToINotifyEvent = map[fsmonitor.Event]uint32{
	fsmonitor.EventAdd:    in.InCreate,
	fsmonitor.EventRemove: in.InDelete | in.InDeleteSelf,
	fsmonitor.EventWrite:  in.InCloseWrite,
	fsmonitor.EventRename: in.InMovedTo,
}

var errIgnoreEvent = errors.New("Intentionally ignored event")

func (d *inotify) toFSMonitorEvent(mask uint32) (fsmonitor.Event, error) {
	for knownINotifyEvent, event := range inotifyEventToFSMonitorEvent {
		if mask&knownINotifyEvent != 0 {
			return event, nil
		}
	}

	if mask&in.InIgnored != 0 || mask&(in.InUnmount|in.InIsdir) != 0 {
		return -1, errIgnoreEvent
	}

	return -1, fmt.Errorf(`Unknown inotify event "%d"`, mask)
}

func (d *inotify) eventMask() (uint32, error) {
	// in.Create is required so that we can determine when a new directory is created and set up a watcher on it and
	// it's subdirectories.
	mask := in.InCreate
	for _, e := range d.events {
		inotifyEvent, ok := fsMonitorEventToINotifyEvent[e]
		if !ok {
			return 0, fmt.Errorf(`Unknown fsmonitor event "%d"`, e)
		}

		// Skip in.InCreate as it is already part of the mask.
		if inotifyEvent&in.InCreate != 0 {
			continue
		}

		mask = mask | inotifyEvent
	}

	return mask, nil
}

// DriverName returns the name of the driver.
func (d *inotify) DriverName() string {
	return "inotify"
}

func (d *inotify) load(ctx context.Context) error {
	var err error

	d.watcher, err = in.NewWatcher()
	if err != nil {
		return fmt.Errorf("Failed to initialize: %w", err)
	}

	err = d.watchFSTree(d.prefixPath)
	if err != nil {
		_ = d.watcher.Close()
		return fmt.Errorf("Failed to watch directory %q: %w", d.prefixPath, err)
	}

	go d.getEvents(ctx)

	return nil
}

func (d *inotify) getEvents(ctx context.Context) {
	for {
		select {
		// Clean up if context is done.
		case <-ctx.Done():
			_ = d.watcher.Close()
			return
		case event := <-d.watcher.Event:
			event.Name = filepath.Clean(event.Name)
			action, err := d.toFSMonitorEvent(event.Mask)
			if err != nil {
				if !errors.Is(err, errIgnoreEvent) {
					logger.Warn("Failed to match inotify event, skipping", logger.Ctx{"err": err})
				}

				continue
			}

			// New event for a directory.
			if event.Mask&in.InIsdir != 0 {
				// If it's a create event, then setup watches on any sub-directories.
				if action == fsmonitor.EventAdd {
					_ = d.watchFSTree(event.Name)
				}

				// Check whether there's a watch on the directory.
				d.mu.Lock()
				for path := range d.watches {
					// Always call the handlers that have a prefix of the event path,
					// in case a watched file is inside the newly created or now deleted
					// directory, otherwise we'll miss the event. The handlers themselves are
					// expected to check the state of the specific path they are interested in.
					if !strings.HasPrefix(path, event.Name) {
						continue
					}

					for identifier, f := range d.watches[path] {
						ret := f(path, action)
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

				for identifier, f := range d.watches[path] {
					ret := f(path, action)
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
		case err := <-d.watcher.Error:
			d.logger.Error("Received event error", logger.Ctx{"err": err})
		}
	}
}

func (d *inotify) watchFSTree(path string) error {
	if !shared.PathExists(path) {
		return errors.New("Path doesn't exist")
	}

	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		// Check for errors here as we only care about directories. Files and symlinks aren't of interest for this.
		if err != nil {
			if os.IsPermission(err) {
				return nil
			}

			d.logger.Warn("Error visiting path", logger.Ctx{"path": path, "err": err})
			return nil
		}

		// Ignore files and symlinks.
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		mask, err := d.eventMask()
		if err != nil {
			return fmt.Errorf("Failed to get an inotify event mask: %w", err)
		}

		err = d.watcher.AddWatch(path, mask)
		if err != nil {
			d.logger.Warn("Failed to watch path", logger.Ctx{"path": path, "err": err})
			return nil
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to watch directory tree: %w", err)
	}

	return nil
}
