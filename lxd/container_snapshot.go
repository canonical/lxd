package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

func containerSnapshotsGet(d *Daemon, r *http.Request) Response {
	recursionStr := r.FormValue("recursion")
	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		recursion = 0
	}

	cname := mux.Vars(r)["name"]
	// Makes sure the requested container exists.
	_, err = containerLXDLoad(d, cname)
	if err != nil {
		return SmartError(err)
	}

	regexp := cname + shared.SnapshotDelimiter
	length := len(regexp)
	q := "SELECT name FROM containers WHERE type=? AND SUBSTR(name,1,?)=?"
	var name string
	inargs := []interface{}{cTypeSnapshot, length, regexp}
	outfmt := []interface{}{name}
	results, err := dbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return SmartError(err)
	}

	resultString := []string{}
	resultMap := []shared.Jmap{}

	for _, r := range results {
		name = r[0].(string)
		sc, err := containerLXDLoad(d, name)
		if err != nil {
			shared.Log.Error("Failed to load snapshot", log.Ctx{"snapshot": name})
			continue
		}

		if recursion == 0 {
			url := fmt.Sprintf("/%s/containers/%s/snapshots/%s", shared.APIVersion, cname, name)
			resultString = append(resultString, url)
		} else {
			body := shared.Jmap{"name": name, "stateful": shared.PathExists(sc.StateDirGet())}
			resultMap = append(resultMap, body)
		}

	}

	if recursion == 0 {
		return SyncResponse(true, resultString)
	}

	return SyncResponse(true, resultMap)
}

/*
 * Note, the code below doesn't deal with snapshots of snapshots.
 * To do that, we'll need to weed out based on # slashes in names
 */
func nextSnapshot(d *Daemon, name string) int {
	base := name + shared.SnapshotDelimiter + "snap"
	length := len(base)
	q := fmt.Sprintf("SELECT MAX(name) FROM containers WHERE type=? AND SUBSTR(name,1,?)=?")
	var numstr string
	inargs := []interface{}{cTypeSnapshot, length, base}
	outfmt := []interface{}{numstr}
	results, err := dbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return 0
	}
	max := 0

	for _, r := range results {
		numstr = r[0].(string)
		if len(numstr) <= length {
			continue
		}
		substr := numstr[length:]
		var num int
		count, err := fmt.Sscanf(substr, "%d", &num)
		if err != nil || count != 1 {
			continue
		}
		if num >= max {
			max = num + 1
		}
	}

	return max
}

func containerSnapshotsPost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	/*
	 * snapshot is a three step operation:
	 * 1. choose a new name
	 * 2. copy the database info over
	 * 3. copy over the rootfs
	 */
	c, err := containerLXDLoad(d, name)
	if err != nil {
		return SmartError(err)
	}

	raw := shared.Jmap{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return BadRequest(err)
	}

	snapshotName, err := raw.GetString("name")
	if err != nil || snapshotName == "" {
		// come up with a name
		i := nextSnapshot(d, name)
		snapshotName = fmt.Sprintf("snap%d", i)
	}

	stateful, err := raw.GetBool("stateful")
	if err != nil {
		return BadRequest(err)
	}

	fullName := name +
		shared.SnapshotDelimiter +
		snapshotName

	snapshot := func() error {
		config := c.ConfigGet()
		args := containerLXDArgs{
			Ctype:        cTypeSnapshot,
			Config:       config,
			Profiles:     c.ProfilesGet(),
			Ephemeral:    c.IsEphemeral(),
			BaseImage:    config["volatile.base_image"],
			Architecture: c.ArchitectureGet(),
			Devices:      c.DevicesGet(),
		}

		_, err := containerLXDCreateAsSnapshot(d, fullName, args, c, stateful)
		if err != nil {
			return err
		}

		return nil
	}

	return AsyncResponse(shared.OperationWrap(snapshot), nil)
}

func snapshotHandler(d *Daemon, r *http.Request) Response {
	containerName := mux.Vars(r)["name"]
	snapshotName := mux.Vars(r)["snapshotName"]

	sc, err := containerLXDLoad(
		d,
		containerName+
			shared.SnapshotDelimiter+
			snapshotName)
	if err != nil {
		return SmartError(err)
	}

	switch r.Method {
	case "GET":
		return snapshotGet(sc, snapshotName)
	case "POST":
		return snapshotPost(r, sc, containerName)
	case "DELETE":
		return snapshotDelete(sc, snapshotName)
	default:
		return NotFound
	}
}

func snapshotGet(sc container, name string) Response {
	body := shared.Jmap{"name": name, "stateful": shared.PathExists(sc.StateDirGet())}
	return SyncResponse(true, body)
}

func snapshotPost(r *http.Request, sc container, containerName string) Response {
	raw := shared.Jmap{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return BadRequest(err)
	}

	newName, err := raw.GetString("name")
	if err != nil {
		return BadRequest(err)
	}

	rename := func() error {
		return sc.Rename(containerName + shared.SnapshotDelimiter + newName)
	}
	return AsyncResponse(shared.OperationWrap(rename), nil)
}

func snapshotDelete(sc container, name string) Response {
	remove := func() error {
		return sc.Delete()
	}
	return AsyncResponse(shared.OperationWrap(remove), nil)
}
