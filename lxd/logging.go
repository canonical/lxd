package main

import (
	"context"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/grant-he/lxd/lxd/db"
	"github.com/grant-he/lxd/lxd/instance"
	"github.com/grant-he/lxd/lxd/instance/instancetype"
	"github.com/grant-he/lxd/lxd/operations"
	"github.com/grant-he/lxd/lxd/project"
	"github.com/grant-he/lxd/lxd/state"
	"github.com/grant-he/lxd/lxd/task"
	"github.com/grant-he/lxd/shared"
	"github.com/grant-he/lxd/shared/logger"

	log "github.com/grant-he/lxd/shared/log15"
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
		names = append(names, project.Instance(inst.Project(), inst.Name()))
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

		// Check if the instance still exists.
		if shared.StringInSlice(entry.Name(), names) {
			instDirEntries, err := ioutil.ReadDir(shared.LogPath(entry.Name()))
			if err != nil {
				return err
			}

			for _, instDirEntry := range instDirEntries {
				path := shared.LogPath(entry.Name(), instDirEntry.Name())

				// Deal with directories (snapshots).
				if instDirEntry.IsDir() {
					newest := newestFile(path, instDirEntry)
					if time.Since(newest).Hours() >= 48 {
						err := os.RemoveAll(path)
						if err != nil {
							return err
						}
					}

					continue
				}

				// Only remove old log files (keep other files, such as conf, pid, monitor etc).
				if strings.HasSuffix(instDirEntry.Name(), ".log") || strings.HasSuffix(instDirEntry.Name(), ".log.old") {
					// Remove any log file which wasn't modified in the past 48 hours.
					if time.Since(instDirEntry.ModTime()).Hours() >= 48 {
						err := os.Remove(path)
						if err != nil {
							return err
						}
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
