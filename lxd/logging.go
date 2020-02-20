package main

import (
	"context"
	"io/ioutil"
	"os"
	"time"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"

	log "github.com/lxc/lxd/shared/log15"
)

// This task function expires logs when executed. It's started by the Daemon
// and will run once every 24h.
func expireLogsTask(state *state.State) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		opRun := func(op *operations.Operation) error {
			return expireLogs(ctx, state)
		}

		op, err := operations.OperationCreate(state, "", operations.OperationClassTask, db.OperationLogsExpire, nil, nil, opRun, nil, nil)
		if err != nil {
			logger.Error("Failed to start log expiry operation", log.Ctx{"err": err})
			return
		}

		logger.Infof("Expiring log files")
		_, err = op.Run()
		if err != nil {
			logger.Error("Failed to expire logs", log.Ctx{"err": err})
		}
		logger.Infof("Done expiring log files")
	}

	return f, task.Daily()
}

func expireLogs(ctx context.Context, state *state.State) error {
	// List the instances.
	instances, err := instance.LoadNodeAll(state, instancetype.Any)
	if err != nil {
		return err
	}

	// List the directory.
	entries, err := ioutil.ReadDir(state.OS.LogDir)
	if err != nil {
		return err
	}

	// Build the expected names.
	names := []string{}
	for _, inst := range instances {
		names = append(names, project.Prefix(inst.Project(), inst.Name()))
	}

	newestFile := func(path string, dir os.FileInfo) time.Time {
		newest := dir.ModTime()

		entries, err := ioutil.ReadDir(path)
		if err != nil {
			return newest
		}

		for _, entry := range entries {
			if entry.ModTime().After(newest) {
				newest = entry.ModTime()
			}
		}

		return newest
	}

	for _, entry := range entries {
		// At each iteration we check if we got cancelled in the meantime.
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// We only care about instance directories.
		if !entry.IsDir() {
			continue
		}

		// Check if the container still exists.
		if shared.StringInSlice(entry.Name(), names) {
			// Remove any log file which wasn't modified in the past 48 hours.
			logs, err := ioutil.ReadDir(shared.LogPath(entry.Name()))
			if err != nil {
				return err
			}

			for _, logfile := range logs {
				path := shared.LogPath(entry.Name(), logfile.Name())

				// Always keep the config files.
				if logfile.Name() == "lxc.conf" || logfile.Name() == "qemu.conf" {
					continue
				}

				// Deal with directories (snapshots).
				if logfile.IsDir() {
					newest := newestFile(path, logfile)
					if time.Since(newest).Hours() >= 48 {
						err := os.RemoveAll(path)
						if err != nil {
							return err
						}
					}

					continue
				}

				// Individual files.
				if time.Since(logfile.ModTime()).Hours() >= 48 {
					err := os.Remove(path)
					if err != nil {
						return err
					}
				}
			}
		} else {
			// Empty directory if unchanged in the past 24 hours.
			path := shared.LogPath(entry.Name())
			newest := newestFile(path, entry)
			if time.Since(newest).Hours() >= 24 {
				err := os.RemoveAll(path)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}
