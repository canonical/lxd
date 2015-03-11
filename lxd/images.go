package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared"
	"gopkg.in/yaml.v2"
)

const (
	COMPRESSION_TAR = iota
	COMPRESSION_GZIP
	COMPRESSION_BZ2
	COMPRESSION_LZMA
	COMPRESSION_XY
)

const (
	ARCH_UNKNOWN                     = 0
	ARCH_32BIT_INTEL_X86             = 1
	ARCH_64BIT_INTEL_X86             = 2
	ARCH_ARMV7_LITTLE_ENDIAN         = 3
	ARCH_64BIT_ARMV8_LITTLE_ENDIAN   = 4
	ARCH_32BIT_POWERPC_BIG_ENDIAN    = 5
	ARCH_64BIT_POWERPC_BIG_ENDIAN    = 6
	ARCH_64BIT_POWERPC_LITTLE_ENDIAN = 7
)

var architectures = map[string]int{
	"i686":    ARCH_32BIT_INTEL_X86,
	"x86_64":  ARCH_64BIT_INTEL_X86,
	"armv7l":  ARCH_ARMV7_LITTLE_ENDIAN,
	"aarch64": ARCH_64BIT_ARMV8_LITTLE_ENDIAN,
	"ppc":     ARCH_32BIT_POWERPC_BIG_ENDIAN,
	"ppc64":   ARCH_64BIT_POWERPC_BIG_ENDIAN,
	"ppc64le": ARCH_64BIT_POWERPC_LITTLE_ENDIAN,
}

func getSize(f *os.File) (int64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func detectCompression(fname string) (int, error) {

	f, err := os.Open(fname)
	if err != nil {
		return -1, err
	}
	defer f.Close()

	// read header parts to detect compression method
	// bz2 - 2 bytes, 'BZ' signature/magic number
	// gz - 2 bytes, 0x1f 0x8b
	// lzma - 6 bytes, { [0x000, 0xE0], '7', 'z', 'X', 'Z', 0x00 } -
	// xy - 6 bytes,  header format { 0xFD, '7', 'z', 'X', 'Z', 0x00 }
	// tar - 263 bytes, trying to get ustar from 257 - 262
	header := make([]byte, 263)
	_, err = f.Read(header)

	switch {
	case bytes.Equal(header[0:2], []byte{'B', 'Z'}):
		return COMPRESSION_BZ2, nil
	case bytes.Equal(header[0:2], []byte{0x1f, 0x8b}):
		return COMPRESSION_GZIP, nil
	case (bytes.Equal(header[1:5], []byte{'7', 'z', 'X', 'Z'}) && header[0] == 0xFD):
		return COMPRESSION_XY, nil
	case (bytes.Equal(header[1:5], []byte{'7', 'z', 'X', 'Z'}) && header[0] != 0xFD):
		return COMPRESSION_LZMA, nil
	case bytes.Equal(header[257:262], []byte{'u', 's', 't', 'a', 'r'}):
		return COMPRESSION_TAR, nil
	default:
		return -1, fmt.Errorf("Unsupported compression.")
	}

}

type imageMetadata struct {
	Architecture  string
	Creation_date float64
	Properties    map[string]interface{}
}

func imagesPost(d *Daemon, r *http.Request) Response {

	cleanup := func(err error, fname string) Response {
		// show both errors, if remove fails
		if remErr := os.Remove(fname); remErr != nil {
			return InternalError(fmt.Errorf("Could not process image: %s; Error deleting temporary file: %s", err, remErr))
		}
		return InternalError(err)
	}

	shared.Debugf("responding to images:post")

	public, err := strconv.Atoi(r.Header.Get("X-LXD-public"))
	tarname := r.Header.Get("X-LXD-filename")

	dirname := shared.VarPath("images")
	err = os.MkdirAll(dirname, 0700)
	if err != nil {
		return InternalError(err)
	}

	f, err := ioutil.TempFile(dirname, "lxd_image")
	if err != nil {
		return InternalError(err)
	}
	defer f.Close()

	fname := f.Name()

	sha256 := sha256.New()
	size, err := io.Copy(io.MultiWriter(f, sha256), r.Body)
	if err != nil {
		return cleanup(err, fname)
	}

	hash := fmt.Sprintf("%x", sha256.Sum(nil))
	expectedHash := r.Header.Get("X-LXD-fingerprint")
	if expectedHash != "" && hash != expectedHash {
		err = fmt.Errorf("hashes don't match, got %s expected %s", hash, expectedHash)
		fmt.Println("TYCHO: ", err)
		return cleanup(err, fname)
	}

	hashfname := shared.VarPath("images", hash)
	if shared.PathExists(hashfname) {
		return cleanup(fmt.Errorf("Image exists already."), fname)
	}

	err = os.Rename(fname, hashfname)
	if err != nil {
		return cleanup(err, fname)
	}

	imageMeta, err := getImageMetadata(hashfname)
	if err != nil {
		return cleanup(err, hashfname)
	}

	arch := ARCH_UNKNOWN
	_, exists := architectures[imageMeta.Architecture]
	if exists {
		arch = architectures[imageMeta.Architecture]
	}

	tx, err := d.db.Begin()
	if err != nil {
		return cleanup(err, hashfname)
	}

	stmt, err := tx.Prepare(`INSERT INTO images (fingerprint, filename, size, public, architecture, upload_date) VALUES (?, ?, ?, ?, ?, strftime("%s"))`)
	if err != nil {
		tx.Rollback()
		return cleanup(err, hashfname)
	}
	defer stmt.Close()

	result, err := stmt.Exec(hash, tarname, size, public, arch)
	if err != nil {
		tx.Rollback()
		return cleanup(err, hashfname)
	}

	properties := r.Header.Get("X-LXD-properties")
	if properties != "" {
		id64, err := result.LastInsertId()
		if err != nil {
			tx.Rollback()
			return cleanup(err, hashfname)
		}
		id := int(id64)

		pstmt, err := tx.Prepare(`INSERT INTO images_properties (image_id, type, key, value) VALUES (?, 0, ?, ?)`)
		if err != nil {
			tx.Rollback()
			return cleanup(err, hashfname)
		}
		defer pstmt.Close()

		list := strings.Split(properties, "; ")
		for _, l := range list {
			fields := strings.SplitN(l, "=", 2)
			if len(fields) != 2 {
				shared.Debugf("Bad image property: %s\n", l)
				continue
			}
			_, err = pstmt.Exec(id, fields[0], fields[1])
			if err != nil {
				tx.Rollback()
				return cleanup(err, hashfname)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return cleanup(err, hashfname)
	}

	/*
	 * TODO - take X-LXD-properties from headers and add those to
	 * containers_properties table
	 */

	metadata := make(map[string]string)
	metadata["fingerprint"] = hash
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

func getImageMetadata(fname string) (*imageMetadata, error) {

	compression, err := detectCompression(fname)

	if err != nil {
		return nil, err
	}

	args := []string{"-O"}
	switch compression {
	case COMPRESSION_TAR:
		args = append(args, "-xf")
	case COMPRESSION_GZIP:
		args = append(args, "-zxf")
	case COMPRESSION_BZ2:
		args = append(args, "--jxf")
	case COMPRESSION_LZMA:
		args = append(args, "--lzma", "-xf")
	default:
		args = append(args, "-Jxf")
	}
	args = append(args, fname, "metadata.yaml")

	// read the metadata.yaml
	output, err := exec.Command("tar", args...).Output()

	if err != nil {
		return nil, fmt.Errorf("Could not get image metadata: %v", err)
	}

	metadata := new(imageMetadata)
	err = yaml.Unmarshal(output, &metadata)

	if err != nil {
		return nil, fmt.Errorf("Could not get image metadata: %v", err)
	}

	return metadata, nil

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
		url := fmt.Sprintf("/%s/images/%s", shared.APIVersion, name)
		result = append(result, url)
	}

	return SyncResponse(true, result)
}

var imagesCmd = Command{name: "images", post: imagesPost, get: imagesGet}

func imageDelete(d *Daemon, r *http.Request) Response {
	shared.Debugf("responding to image:delete")

	fingerprint := mux.Vars(r)["fingerprint"]
	fname := shared.VarPath("images", fingerprint)
	err := os.Remove(fname)
	if err != nil {
		shared.Debugf("Error deleting image file %s: %s\n", fname, err)
	}

	tx, err := d.db.Begin()
	if err != nil {
		return BadRequest(err)
	}

	rows, err := tx.Query("SELECT id FROM images WHERE fingerprint=?", fingerprint)
	if err != nil {
		tx.Rollback()
		return BadRequest(err)
	}
	defer rows.Close()

	id := -1
	for rows.Next() {
		var xId int
		rows.Scan(&xId)
		id = xId
	}
	if id == -1 {
		shared.Debugf("Error deleting image from db %s: %s\n", fingerprint, err)
		tx.Rollback()
		return NotFound
	}

	_, _ = tx.Exec("DELETE FROM images_aliases WHERE image_id=?", id)
	_, _ = tx.Exec("DELETE FROM images_properties WHERE ?", id)
	_, _ = tx.Exec("DELETE FROM images WHERE id=?", id)

	if err := tx.Commit(); err != nil {
		return BadRequest(err)
	}

	return EmptySyncResponse
}

func imageGet(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	rows, err := d.db.Query("SELECT id, public FROM images WHERE fingerprint=?", fingerprint)
	if err != nil {
		return InternalError(err)
	}
	defer rows.Close()
	id := -1
	public := 0
	for rows.Next() {
		var xId int
		var p int
		rows.Scan(&xId, &p)
		id = xId
		public = p
	}
	if id == -1 {
		return NotFound
	}

	rows2, err := d.db.Query("SELECT type, key, value FROM images_properties where image_id=?", id)
	if err != nil {
		return InternalError(err)
	}
	defer rows2.Close()
	properties := shared.ImageProperties{}
	for rows2.Next() {
		var key, value string
		var imagetype int
		rows2.Scan(&imagetype, &key, &value)
		i := shared.ImageProperty{Imagetype: imagetype, Key: key, Value: value}
		properties = append(properties, i)
	}

	rows3, err := d.db.Query("SELECT name, description FROM images_aliases WHERE image_id=?", id)
	if err != nil {
		return InternalError(err)
	}
	defer rows3.Close()
	aliases := shared.ImageAliases{}
	for rows3.Next() {
		var name, desc string
		rows3.Scan(&name, &desc)
		a := shared.ImageAlias{Name: name, Description: desc}
		aliases = append(aliases, a)
	}

	info := shared.ImageInfo{Fingerprint: fingerprint, Properties: properties, Aliases: aliases, Public: public}
	return SyncResponse(true, info)
}

type imagePutReq struct {
	Properties shared.ImageProperties `json:"properties"`
}

func imagePut(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	imageRaw := imagePutReq{}
	if err := json.NewDecoder(r.Body).Decode(&imageRaw); err != nil {
		return BadRequest(err)
	}

	tx, err := d.db.Begin()
	if err != nil {
		return InternalError(err)
	}

	id := -1
	rows, err := d.db.Query("SELECT id FROM images WHERE fingerprint=?", fingerprint)
	if err != nil {
		tx.Rollback()
		return InternalError(err)
	}
	defer rows.Close()

	for rows.Next() {
		var idx int
		rows.Scan(&idx)
		id = idx
	}

	_, err = tx.Exec(`DELETE FROM images_properties WHERE image_id=?`, id)

	stmt, err := tx.Prepare(`INSERT INTO images_properties (image_id, type, key, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return InternalError(err)
	}
	for _, i := range imageRaw.Properties {
		_, err = stmt.Exec(id, i.Imagetype, i.Key, i.Value)
		if err != nil {
			tx.Rollback()
			return InternalError(err)
		}
	}

	if err := tx.Commit(); err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

var imageCmd = Command{name: "images/{fingerprint}", get: imageGet, put: imagePut, delete: imageDelete}

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
		url := fmt.Sprintf("/%s/images/aliases/%s", shared.APIVersion, name)
		result = append(result, url)
	}

	return SyncResponse(true, result)
}

func aliasGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	rows, err := d.db.Query(`SELECT images.fingerprint, images_aliases.description
	                         FROM images_aliases
	                         INNER JOIN images
				 ON images_aliases.image_id=images.id
				 WHERE images_aliases.name=?`, name)
	if err != nil {
		return InternalError(err)
	}
	defer rows.Close()

	for rows.Next() {
		var fingerprint, description string
		if err := rows.Scan(&fingerprint, &description); err != nil {
			return InternalError(err)
		}

		return SyncResponse(true, shared.Jmap{"target": fingerprint, "description": description})
	}

	return NotFound
}

func aliasDelete(d *Daemon, r *http.Request) Response {
	shared.Debugf("responding to images/aliases:delete")

	name := mux.Vars(r)["name"]
	_, _ = d.db.Exec("DELETE FROM images_aliases WHERE name=?", name)

	return EmptySyncResponse
}

func imageExport(d *Daemon, r *http.Request) Response {
	shared.Debugf("responding to images/export")

	name := mux.Vars(r)["name"]

	rows, err := d.db.Query(`SELECT images.filename, images.size FROM images WHERE images.fingerprint=?`, name)
	if err != nil {
		return InternalError(err)
	}
	defer rows.Close()

	for rows.Next() {
		var filename string
		var size int64
		if err := rows.Scan(&filename, &size); err != nil {
			return InternalError(err)
		}

		// test compression, for content type header
		// if unknown compression we send standard header
		_, err := detectCompression(filename)

		ctype := "application/x-gtar"
		if err != nil {
			ctype = "application/octet-stream"
		}

		return FileResponse(filename, size, ctype)

	}

	return NotFound

}

var imagesExportCmd = Command{name: "images/{name}/export", get: imageExport}

var aliasesCmd = Command{name: "images/aliases", post: aliasesPost, get: aliasesGet}

var aliasCmd = Command{name: "images/aliases/{name:.*}", get: aliasGet, delete: aliasDelete}
