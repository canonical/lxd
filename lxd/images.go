package main

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"strconv"

	//"github.com/uli-go/xz/lzma"
	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared"
)

func getSize(f *os.File) (int64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func imagesPost(d *Daemon, r *http.Request) Response {
	shared.Debugf("responding to images:post")

	public, err := strconv.Atoi(r.Header.Get("X-LXD-public"))
	tarname := r.Header.Get("X-LXD-filename")

	dirname := shared.VarPath("images")
	err = os.MkdirAll(dirname, 0700)
	if err != nil {
		return InternalError(err)
	}

	f, err := ioutil.TempFile(dirname, "image_")
	if err != nil {
		return InternalError(err)
	}

	fname := f.Name()

	_, err = io.Copy(f, r.Body)

	size, err := getSize(f)
	f.Close()
	if err != nil {
		return InternalError(err)
	}

	if err != nil {
		return InternalError(err)
	}

	/* TODO - this reads whole file into memory; we should probably
	 * do the sha256sum piecemeal */
	contents, err := ioutil.ReadFile(fname)
	if err != nil {
		return InternalError(err)
	}

	fingerprint := sha256.Sum256(contents)
	uuid := fmt.Sprintf("%x", fingerprint)
	uuidfname := shared.VarPath("images", uuid)

	if shared.PathExists(uuidfname) {
		return InternalError(fmt.Errorf("Image exists"))
	}
	err = os.Rename(fname, uuidfname)
	if err != nil {
		return InternalError(err)
	}

	arch, err := extractTar(uuidfname)
	if err != nil {
		return InternalError(err)
	}

	tx, err := d.db.Begin()
	if err != nil {
		return InternalError(err)
	}

	stmt, err := tx.Prepare(`INSERT INTO images (fingerprint, filename, size, public, architecture, upload_date) VALUES (?, ?, ?, ?, ?, strftime("%s"))`)
	if err != nil {
		tx.Rollback()
		return InternalError(err)
	}
	defer stmt.Close()
	_, err = stmt.Exec(uuid, tarname, size, public, arch)
	if err != nil {
		tx.Rollback()
		return InternalError(err)
	}

	if err := tx.Commit(); err != nil {
		return InternalError(err)
	}

	/*
	 * TODO - take X-LXD-properties from headers and add those to
	 * containers_properties table
	 */

	metadata := make(map[string]string)
	metadata["fingerprint"] = uuid
	metadata["size"] = strconv.FormatInt(size, 10)

	return SyncResponse(true, metadata)
}

func xzReader(r io.Reader) io.ReadCloser {
	rpipe, wpipe := io.Pipe()

	cmd := exec.Command("xz", "--decompress", "--stdout")
	cmd.Stdin = r
	cmd.Stdout = wpipe

	go func() {
		err := cmd.Run()
		wpipe.CloseWithError(err)
	}()

	return rpipe
}

func extractTar(fname string) (int, error) {
	f, err := os.Open(fname)
	if err != nil {
		fmt.Printf("error opening %s: %s\n", fname, err)
		return 0, err
	}
	defer f.Close()
	fmt.Printf("opened %s\n", fname)

	/* todo - uncompress */
	/*
		var fileReader io.ReadCloser = f
		if fileReader, err = lzma.NewReader(f); err != nil {
			// ok it's not xz - ignore (or try others)
			filereader = f
		} else {
			defer filereader.Close()
		}
	*/

	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Printf("got error %s\n", err)
			/* TODO - figure out why we get error */
			return 0, nil
			//return 0, err
		}
		if hdr.Name != "metadata.yaml" {
			continue
		}
		//tr.Read()
		// find architecture line
		break
	}

	/* todo - read arch from metadata.yaml */
	arch := 0
	return arch, nil
}

func imagesGet(d *Daemon, r *http.Request) Response {
	shared.Debugf("responding to images:get")

	rows, err := d.db.Query("SELECT fingerprint FROM images")
	if err != nil {
		return BadRequest(err)
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var name string
		rows.Scan(&name)
		url := fmt.Sprintf("/1.0/images/%s", name)
		result = append(result, url)
	}

	return SyncResponse(true, result)
}

var imagesCmd = Command{name: "images", post: imagesPost, get: imagesGet}

func imageDelete(d *Daemon, r *http.Request) Response {
	shared.Debugf("responding to image:delete")

	uuid := mux.Vars(r)["name"]
	uuidfname := shared.VarPath("images", uuid)
	err := os.Remove(uuidfname)
	if err != nil {
		shared.Debugf("Error deleting image file %s: %s\n", uuidfname, err)
	}

	_, _ = d.db.Exec("DELETE FROM images_aliases WHERE image_id=(SELECT id FROM images WHERE fingerprint=?);", uuid)
	_, _ = d.db.Exec("DELETE FROM images WHERE fingerprint=?", uuid)

	return EmptySyncResponse
}

var imageCmd = Command{name: "images/{name}", delete: imageDelete}

type aliasPostReq struct {
	Name        string `json:"name"`
	Description string `json:"descriptoin"`
	Target      string `json:"target"`
}

func aliasesPost(d *Daemon, r *http.Request) Response {
	shared.Debugf("responding to images/aliases:put")

	req := aliasPostReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	if req.Name == "" || req.Target == "" {
		return BadRequest(fmt.Errorf("name and target are required"))
	}
	if req.Description == "" {
		req.Description = req.Name
	}

	_, _, err := dbAliasGet(d, req.Name)
	if err == nil {
		return BadRequest(fmt.Errorf("alias exists"))
	}

	iId, err := dbImageGet(d, req.Target)
	if err != nil {
		return BadRequest(err)
	}

	err = dbAddAlias(d, req.Name, iId, req.Description)
	if err != nil {
		return BadRequest(err)
	}

	return EmptySyncResponse
}

func aliasesGet(d *Daemon, r *http.Request) Response {
	shared.Debugf("responding to images/aliases:get")

	rows, err := d.db.Query("SELECT name FROM images_aliases")
	if err != nil {
		return BadRequest(err)
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var name string
		rows.Scan(&name)
		url := fmt.Sprintf("/1.0/images/aliases/%s", name)
		result = append(result, url)
	}

	return SyncResponse(true, result)
}

func aliasDelete(d *Daemon, r *http.Request) Response {
	shared.Debugf("responding to images/aliases:delete")

	name := mux.Vars(r)["name"]
	_, _ = d.db.Exec("DELETE FROM images_aliases WHERE name=?", name)

	return EmptySyncResponse
}

var aliasesCmd = Command{name: "images/aliases", post: aliasesPost, get: aliasesGet}

var aliasCmd = Command{name: "images/aliases/{name:.*}", delete: aliasDelete}
