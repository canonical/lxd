package main

import (
	"io/ioutil"
	"os"
	"time"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"golang.org/x/net/context"

	log "github.com/lxc/lxd/shared/log15"
)

// This task function expires logs when executed. It's started by the Daemon
// and will run once every 24h.
func expireLogsTask(state *state.State) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		opRun := func(op *operation) error {
			return expireLogs(ctx, state)
		}

		op, err := operationCreate(state.Cluster, "", operationClassTask, db.OperationLogsExpire, nil, nil, opRun, nil, nil)
		if err != nil {
			logger.Error("Failed to start log expiry operation", log.Ctx{"err": err})
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
	entries, err := ioutil.ReadDir(state.OS.LogDir)
	if err != nil {
		return err
	}

	// FIXME: our DB APIs don't yet support cancellation, se we need to run
	//        them in a goroutine and abort this task if the context gets
	//        cancelled.
	var containers []string
	ch := make(chan struct{})
	go func() {
		containers, err = state.Cluster.ContainersNodeList(db.CTypeRegular)
		ch <- struct{}{}
	}()
	select {
	case <-ctx.Done():
		return nil // Context expired
	case <-ch:
	}

	if err != nil {
		return err
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

		// We only care about container directories
		if !entry.IsDir() {
			continue
		}

		// Check if the container still exists
		if shared.StringInSlice(entry.Name(), containers) {
			// Remove any log file which wasn't modified in the past 48 hours
			logs, err := ioutil.ReadDir(shared.LogPath(entry.Name()))
			if err != nil {
				return err
			}

			for _, logfile := range logs {
				path := shared.LogPath(entry.Name(), logfile.Name())

				// Always keep the LXC config
				if logfile.Name() == "lxc.conf" {
					continue
				}

				// Deal with directories (snapshots)
				if logfile.IsDir() {
					newest := newestFile(path, logfile)
					if time.Since(newest).Hours() >= 48 {
						os.RemoveAll(path)
						if err != nil {
							return err
						}
					}

					continue
				}

				// Individual files
				if time.Since(logfile.ModTime()).Hours() >= 48 {
					err := os.Remove(path)
					if err != nil {
						return err
					}
				}
			}
		} else {
			// Empty directory if unchanged in the past 24 hours
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
