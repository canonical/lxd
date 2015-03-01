package main

import (
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
	"bytes"
	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared"
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

	/* TODO - this reads whole file into memory; we should probably
	 * do the sha256sum piecemeal */
	contents, err := ioutil.ReadFile(fname)
	if err != nil {
		// TODO clean up file
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

	imageMeta, err := getImageMetadata(uuidfname)
	if err != nil {
		// TODO: clean up file
		return InternalError(err)
	}

	arch := ARCH_UNKNOWN
	_, exists := architectures[imageMeta.Architecture]
	if exists {
		arch = architectures[imageMeta.Architecture]
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
		return nil, err
	}

	metadata := new(imageMetadata)
	err = json.Unmarshal(output, &metadata)

	if err != nil {
		return nil, err
	}

	return metadata, nil
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

	//	tr := tar.NewReader(f)
	//	for {
	//		hdr, err := tr.Next()
	//		if err != nil {
	//			if err == io.EOF {
	//				break
	//			}
	//			fmt.Printf("got error %s\n", err)
	//			/* TODO - figure out why we get error */
	//			return 0, nil
	//			//return 0, err
	//		}
	//		if hdr.Name != "metadata.yaml" {
	//			continue
	//		}
	//		//tr.Read()
	//		// find architecture line
	//		break
	//	}

	/* todo - read arch from metadata.yaml */
	//	arch := 0
	//	return arch, nil
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

var aliasesCmd = Command{name: "images/aliases", post: aliasesPost, get: aliasesGet}

var aliasCmd = Command{name: "images/aliases/{name:.*}", get: aliasGet, delete: aliasDelete}
