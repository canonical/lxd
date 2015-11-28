package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

type commandPostContent struct {
	Command     []string          `json:"command"`
	WaitForWS   bool              `json:"wait-for-websocket"`
	Interactive bool              `json:"interactive"`
	Environment map[string]string `json:"environment"`
}

type containerConfigReq struct {
	Profiles []string          `json:"profiles"`
	Config   map[string]string `json:"config"`
	Devices  shared.Devices    `json:"devices"`
	Restore  string            `json:"restore"`
}

type containerStatePutReq struct {
	Action  string `json:"action"`
	Timeout int    `json:"timeout"`
	Force   bool   `json:"force"`
}

type containerPostBody struct {
	Migration bool   `json:"migration"`
	Name      string `json:"name"`
}

type containerPostReq struct {
	Architecture int                  `json:"architecture"`
	Config       map[string]string    `json:"config"`
	Devices      shared.Devices       `json:"devices"`
	Ephemeral    bool                 `json:"ephemeral"`
	Name         string               `json:"name"`
	Profiles     []string             `json:"profiles"`
	Source       containerImageSource `json:"source"`
}

type containerImageSource struct {
	Type string `json:"type"`

	/* for "image" type */
	Alias       string `json:"alias"`
	Fingerprint string `json:"fingerprint"`
	Server      string `json:"server"`
	Secret      string `json:"secret"`

	/*
	 * for "migration" and "copy" types, as an optimization users can
	 * provide an image hash to extract before the filesystem is rsync'd,
	 * potentially cutting down filesystem transfer time. LXD will not go
	 * and fetch this image, it will simply use it if it exists in the
	 * image store.
	 */
	BaseImage string `json:"base-image"`

	/* for "migration" type */
	Mode       string            `json:"mode"`
	Operation  string            `json:"operation"`
	Websockets map[string]string `json:"secrets"`

	/* for "copy" type */
	Source string `json:"source"`
}

var containersCmd = Command{
	name: "containers",
	get:  containersGet,
	post: containersPost,
}

var containerCmd = Command{
	name:   "containers/{name}",
	get:    containerGet,
	put:    containerPut,
	delete: containerDelete,
	post:   containerPost,
}

var containerStateCmd = Command{
	name: "containers/{name}/state",
	get:  containerState,
	put:  containerStatePut,
}

var containerFileCmd = Command{
	name: "containers/{name}/files",
	get:  containerFileHandler,
	post: containerFileHandler,
}

var containerSnapshotsCmd = Command{
	name: "containers/{name}/snapshots",
	get:  containerSnapshotsGet,
	post: containerSnapshotsPost,
}

var containerSnapshotCmd = Command{
	name:   "containers/{name}/snapshots/{snapshotName}",
	get:    snapshotHandler,
	post:   snapshotHandler,
	delete: snapshotHandler,
}

var containerExecCmd = Command{
	name: "containers/{name}/exec",
	post: containerExecPost,
}

func containersRestart(d *Daemon) error {
	containers, err := doContainersGet(d, true)

	if err != nil {
		return err
	}

	containerInfo := containers.(shared.ContainerInfoList)
	sort.Sort(containerInfo)

	for _, container := range containerInfo {
		lastState := container.State.Config["volatile.last_state.power"]

		autoStart := container.State.ExpandedConfig["boot.autostart"]
		autoStartDelay := container.State.ExpandedConfig["boot.autostart.delay"]

		if lastState == "RUNNING" || autoStart == "true" {
			c, err := containerLXDLoad(d, container.State.Name)
			if err != nil {
				return err
			}

			if c.IsRunning() {
				continue
			}

			c.Start()

			autoStartDelayInt, err := strconv.Atoi(autoStartDelay)
			if err == nil {
				time.Sleep(time.Duration(autoStartDelayInt) * time.Second)
			}
		}
	}

	_, err = dbExec(d.db, "DELETE FROM containers_config WHERE key='volatile.last_state.power'")
	if err != nil {
		return err
	}

	return nil
}

func containersShutdown(d *Daemon) error {
	results, err := dbContainersList(d.db, cTypeRegular)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup

	for _, r := range results {
		c, err := containerLXDLoad(d, r)
		if err != nil {
			return err
		}

		err = c.ConfigKeySet("volatile.last_state.power", c.State())

		if err != nil {
			return err
		}

		if c.IsRunning() {
			wg.Add(1)
			go func() {
				c.Shutdown(time.Second * 30)
				c.Stop()
				wg.Done()
			}()
		}
		wg.Wait()
	}

	return nil
}

func containerDeleteSnapshots(d *Daemon, cname string) error {
	shared.Log.Debug("containerDeleteSnapshots",
		log.Ctx{"container": cname})

	results, err := dbContainerGetSnapshots(d.db, cname)
	if err != nil {
		return err
	}

	for _, sname := range results {
		sc, err := containerLXDLoad(d, sname)
		if err != nil {
			shared.Log.Error(
				"containerDeleteSnapshots: Failed to load the snapshotcontainer",
				log.Ctx{"container": cname, "snapshot": sname})

			continue
		}

		if err := sc.Delete(); err != nil {
			shared.Log.Error(
				"containerDeleteSnapshots: Failed to delete a snapshotcontainer",
				log.Ctx{"container": cname, "snapshot": sname, "err": err})
		}
	}

	return nil
}

/*
 * This is called by lxd when called as "lxd forkstart <container>"
 * 'forkstart' is used instead of just 'start' in the hopes that people
 * do not accidentally type 'lxd start' instead of 'lxc start'
 *
 * We expect to read the lxcconfig over fd 3.
 */
func startContainer(args []string) error {
	if len(args) != 4 {
		return fmt.Errorf("Bad arguments: %q", args)
	}
	name := args[1]
	lxcpath := args[2]
	configPath := args[3]

	c, err := lxc.NewContainer(name, lxcpath)
	if err != nil {
		return fmt.Errorf("Error initializing container for start: %q", err)
	}
	err = c.LoadConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("Error opening startup config file: %q", err)
	}

	/* due to https://github.com/golang/go/issues/13155 and the
	 * CollectOutput call we make for the forkstart process, we need to
	 * close our stdin/stdout/stderr here. Collecting some of the logs is
	 * better than collecting no logs, though.
	 */
	os.Stdin.Close()
	os.Stderr.Close()
	os.Stdout.Close()
	err = c.Start()
	if err != nil {
		os.Remove(configPath)
	} else {
		shared.FileMove(configPath, shared.LogPath(name, "lxc.conf"))
	}

	return err
}
