package main

import (
	"archive/tar"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"strconv"

	//"github.com/uli-go/xz/lzma"
	"github.com/lxc/lxd/shared"
	_ "github.com/mattn/go-sqlite3"
)

func getSize(f *os.File) (int64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func imagesPut(d *Daemon, r *http.Request) Response {
	shared.Debugf("responding to images:put")

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

	certdbname := shared.VarPath("lxd.db")
	db, err := sql.Open("sqlite3", certdbname)
	if err != nil {
		return InternalError(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return InternalError(err)
	}

	stmt, err := tx.Prepare("INSERT INTO images (fingerprint, filename, size, public, architecture, upload_date) VALUES (?, ?, ?, ?, ?, ?)")
	if err != nil {
		return InternalError(err)
	}
	defer stmt.Close()
	_, err = stmt.Exec(uuid, tarname, size, public, arch, "now")
	if err != nil {
		return InternalError(err)
	}
	tx.Commit()

	/*
	 * TODO - take X-LXD-properties from headers and add those to
	 * containers_properties table
	 */

	return EmptySyncResponse
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
	certdbname := shared.VarPath("lxd.db")
	db, err := sql.Open("sqlite3", certdbname)
	if err != nil {
		return BadRequest(err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT fingerprint FROM images")
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

var imagesCmd = Command{name: "images", put: imagesPut, get: imagesGet}
