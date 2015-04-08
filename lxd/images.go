package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
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
	COMPRESSION_XZ
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

func detectCompression(fname string) (int, string, error) {

	f, err := os.Open(fname)
	if err != nil {
		return -1, "", err
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
		return COMPRESSION_BZ2, ".tar.bz2", nil
	case bytes.Equal(header[0:2], []byte{0x1f, 0x8b}):
		return COMPRESSION_GZIP, ".tar.gz", nil
	case (bytes.Equal(header[1:5], []byte{'7', 'z', 'X', 'Z'}) && header[0] == 0xFD):
		return COMPRESSION_XZ, ".tar.xz", nil
	case (bytes.Equal(header[1:5], []byte{'7', 'z', 'X', 'Z'}) && header[0] != 0xFD):
		return COMPRESSION_LZMA, ".tar.lzma", nil
	case bytes.Equal(header[257:262], []byte{'u', 's', 't', 'a', 'r'}):
		return COMPRESSION_TAR, ".tar", nil
	default:
		return -1, "", fmt.Errorf("Unsupported compression.")
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
		return SmartError(err)
	}

	public, err := strconv.Atoi(r.Header.Get("X-LXD-public"))
	tarname := r.Header.Get("X-LXD-filename")

	dirname := shared.VarPath("images")
	err = os.MkdirAll(dirname, 0700)
	if err != nil {
		return InternalError(err)
	}

	f, err := ioutil.TempFile(dirname, "lxd_image_")
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
		return cleanup(err, fname)
	}

	hashfname := shared.VarPath("images", hash)
	if shared.PathExists(hashfname) {
		return cleanup(fmt.Errorf("Image already exists."), fname)
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

	// read multiple headers, if required
	propHeaders := r.Header[http.CanonicalHeaderKey("X-LXD-properties")]

	if len(propHeaders) > 0 {

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

		for _, ph := range propHeaders {

			// url parse the header
			p, err := url.ParseQuery(ph)

			if err != nil {
				tx.Rollback()
				return cleanup(err, hashfname)
			}

			// for each key value pair, exec statement
			for pkey, pval := range p {

				// we can assume, that there is just one
				// value per key
				_, err = pstmt.Exec(id, pkey, pval[0])
				if err != nil {
					tx.Rollback()
					return cleanup(err, hashfname)
				}

			}

		}

	}

	if err := shared.TxCommit(tx); err != nil {
		return cleanup(err, hashfname)
	}

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

	compression, _, err := detectCompression(fname)

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
	public := !d.isTrustedClient(r)

	recursion_str := r.FormValue("recursion")
	recursion, err := strconv.Atoi(recursion_str)
	if err != nil {
		recursion = 0
	}

	result, err := doImagesGet(d, recursion, public)
	if err != nil {
		return SmartError(err)
	}
	return SyncResponse(true, result)
}

func doImagesGet(d *Daemon, recursion int, public bool) (interface{}, error) {
	var err error
	var rows *sql.Rows
	result_string := make([]string, 0)
	result_map := make([]shared.ImageInfo, 0)

	if public == true {
		rows, err = shared.DbQuery(d.db, "SELECT fingerprint FROM images WHERE public=1")
	} else {
		rows, err = shared.DbQuery(d.db, "SELECT fingerprint FROM images")
	}
	if err != nil {
		return []string{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		err = rows.Scan(&name)
		if err != nil {
			return []string{}, err
		}
		if recursion == 0 {
			url := fmt.Sprintf("/%s/images/%s", shared.APIVersion, name)
			result_string = append(result_string, url)
		} else {
			image, response := doImageGet(d, name, public)
			if response != nil {
				continue
			}
			result_map = append(result_map, image)
		}
	}
	err = rows.Err()
	if err != nil {
		return []string{}, err
	}

	if recursion == 0 {
		return result_string, nil
	} else {
		return result_map, nil
	}
}

var imagesCmd = Command{name: "images", post: imagesPost, get: imagesGet}

func imageDelete(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	imgInfo, err := dbImageGet(d, fingerprint, false)
	if err != nil {
		return SmartError(err)
	}

	fname := shared.VarPath("images", imgInfo.Fingerprint)
	err = os.Remove(fname)
	if err != nil {
		shared.Debugf("Error deleting image file %s: %s\n", fname, err)
	}

	tx, err := d.db.Begin()
	if err != nil {
		return InternalError(err)
	}

	_, _ = tx.Exec("DELETE FROM images_aliases WHERE image_id=?", imgInfo.Id)
	_, _ = tx.Exec("DELETE FROM images_properties WHERE image_id?", imgInfo.Id)
	_, _ = tx.Exec("DELETE FROM images WHERE id=?", imgInfo.Id)

	if err := shared.TxCommit(tx); err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

func doImageGet(d *Daemon, fingerprint string, public bool) (shared.ImageInfo, Response) {
	imgInfo, err := dbImageGet(d, fingerprint, public)
	if err != nil {
		return shared.ImageInfo{}, SmartError(err)
	}

	rows2, err := shared.DbQuery(d.db, "SELECT type, key, value FROM images_properties where image_id=?", imgInfo.Id)
	if err != nil {
		return shared.ImageInfo{}, SmartError(err)
	}
	defer rows2.Close()
	properties := map[string]string{}
	for rows2.Next() {
		var key, value string
		var imagetype int
		err = rows2.Scan(&imagetype, &key, &value)
		if err != nil {
			fmt.Printf("DBERR: imageGet: scan returned error %q\n", err)
			return shared.ImageInfo{}, InternalError(err)
		}
		properties[key] = value
	}
	err = rows2.Err()
	if err != nil {
		fmt.Printf("DBERR: imageGet: Err returned an error %q\n", err)
		return shared.ImageInfo{}, InternalError(err)
	}

	rows3, err := shared.DbQuery(d.db, "SELECT name, description FROM images_aliases WHERE image_id=?", imgInfo.Id)
	if err != nil {
		return shared.ImageInfo{}, InternalError(err)
	}
	defer rows3.Close()
	aliases := shared.ImageAliases{}
	for rows3.Next() {
		var name, desc string
		err = rows3.Scan(&name, &desc)
		if err != nil {
			fmt.Printf("DBERR: imageGet (2): scan returned error %q\n", err)
			return shared.ImageInfo{}, InternalError(err)
		}
		a := shared.ImageAlias{Name: name, Description: desc}
		aliases = append(aliases, a)
	}
	err = rows3.Err()
	if err != nil {
		fmt.Printf("DBERR: imageGet (2): Err returned an error %q\n", err)
		return shared.ImageInfo{}, InternalError(err)
	}

	info := shared.ImageInfo{Fingerprint: imgInfo.Fingerprint,
		Filename:     imgInfo.Filename,
		Properties:   properties,
		Aliases:      aliases,
		Public:       imgInfo.Public,
		Size:         imgInfo.Size,
		Architecture: imgInfo.Architecture,
		CreationDate: imgInfo.CreationDate,
		ExpiryDate:   imgInfo.ExpiryDate,
		UploadDate:   imgInfo.UploadDate}

	return info, nil
}

func imageValidSecret(fingerprint string, secret string) bool {
	lock.Lock()
	for _, op := range operations {
		if op.Resources == nil {
			continue
		}

		opImages, ok := op.Resources["images"]
		if ok == false {
			continue
		}

		found := false
		for img := range opImages {
			toScan := strings.Replace(opImages[img], "/", " ", -1)
			imgVersion := ""
			imgFingerprint := ""
			count, err := fmt.Sscanf(toScan, " %s images %s", &imgVersion, &imgFingerprint)
			if err != nil || count != 2 {
				continue
			}

			if imgFingerprint == fingerprint {
				found = true
				break
			}
		}

		if found == false {
			continue
		}

		opMetadata, err := op.MetadataAsMap()
		if err != nil {
			continue
		}

		opSecret, err := opMetadata.GetString("secret")
		if err != nil {
			continue
		}

		if opSecret == secret {
			lock.Unlock()
			return true
		}
	}
	lock.Unlock()

	return false
}

func imageGet(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]
	public := !d.isTrustedClient(r)
	secret := r.FormValue("secret")

	if public == true && imageValidSecret(fingerprint, secret) == true {
		public = false
	}

	info, response := doImageGet(d, fingerprint, public)
	if response != nil {
		return response
	}

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

	imgInfo, err := dbImageGet(d, fingerprint, false)
	if err != nil {
		return SmartError(err)
	}

	_, err = tx.Exec(`DELETE FROM images_properties WHERE image_id=?`, imgInfo.Id)

	stmt, err := tx.Prepare(`INSERT INTO images_properties (image_id, type, key, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return InternalError(err)
	}
	for _, i := range imageRaw.Properties {
		_, err = stmt.Exec(imgInfo.Id, i.Imagetype, i.Key, i.Value)
		if err != nil {
			tx.Rollback()
			return InternalError(err)
		}
	}

	if err := shared.TxCommit(tx); err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

var imageCmd = Command{name: "images/{fingerprint}", untrustedGet: true, get: imageGet, put: imagePut, delete: imageDelete}

type aliasPostReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Target      string `json:"target"`
}

func aliasesPost(d *Daemon, r *http.Request) Response {
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
		return Conflict
	}

	imgInfo, err := dbImageGet(d, req.Target, false)
	if err != nil {
		return SmartError(err)
	}

	err = dbAddAlias(d, req.Name, imgInfo.Id, req.Description)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

func aliasesGet(d *Daemon, r *http.Request) Response {
	rows, err := shared.DbQuery(d.db, "SELECT name FROM images_aliases")
	if err != nil {
		return BadRequest(err)
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var name string
		err = rows.Scan(&name)
		if err != nil {
			fmt.Printf("DBERR: aliasesGet: scan returned error %q\n", err)
			return InternalError(err)
		}
		url := fmt.Sprintf("/%s/images/aliases/%s", shared.APIVersion, name)
		result = append(result, url)
	}
	err = rows.Err()
	if err != nil {
		fmt.Printf("DBERR: aliasesGet: Err returned an error %q\n", err)
		return InternalError(err)
	}

	return SyncResponse(true, result)
}

func aliasGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	q := `SELECT images.fingerprint, images_aliases.description
			 FROM images_aliases
			 INNER JOIN images
			 ON images_aliases.image_id=images.id
			 WHERE images_aliases.name=?`
	if !d.isTrustedClient(r) {
		q = q + ` AND images.public=1`
	}

	var fingerprint, description string
	arg1 := []interface{}{name}
	arg2 := []interface{}{&fingerprint, &description}
	err := shared.DbQueryRowScan(d.db, q, arg1, arg2)
	if err == sql.ErrNoRows {
		return NotFound
	}
	if err != nil {
		return InternalError(err)
	}

	return SyncResponse(true, shared.Jmap{"target": fingerprint, "description": description})
}

func aliasDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	_, _ = shared.DbExec(d.db, "DELETE FROM images_aliases WHERE name=?", name)

	return EmptySyncResponse
}

func imageExport(d *Daemon, r *http.Request) Response {
	hash := mux.Vars(r)["hash"]

	public := !d.isTrustedClient(r)
	secret := r.FormValue("secret")

	if public == true && imageValidSecret(hash, secret) == true {
		public = false
	}

	imgInfo, err := dbImageGet(d, hash, public)
	if err != nil {
		return SmartError(err)
	}

	path := shared.VarPath("images", imgInfo.Fingerprint)
	filename := imgInfo.Filename

	if filename == "" {
		_, ext, err := detectCompression(path)
		if err != nil {
			ext = ""
		}
		filename = fmt.Sprintf("%s%s", hash, ext)
	}

	headers := map[string]string{
		"Content-Disposition": fmt.Sprintf("inline;filename=%s", filename),
	}

	return FileResponse(r, path, filename, headers)
}

func imageSecret(d *Daemon, r *http.Request) Response {
	hash := mux.Vars(r)["hash"]
	_, err := dbImageGet(d, hash, false)
	if err != nil {
		return SmartError(err)
	}

	secret, err := shared.RandomCryptoString()

	if err != nil {
		return InternalError(err)
	}

	meta := shared.Jmap{}
	meta["secret"] = secret

	resources := make(map[string][]string)
	resources["images"] = []string{fingerprint}
	return &asyncResponse{resources: resources, metadata: meta}
}

var imagesExportCmd = Command{name: "images/{hash}/export", untrustedGet: true, get: imageExport}
var imagesSecretCmd = Command{name: "images/{hash}/secret", post: imageSecret}

var aliasesCmd = Command{name: "images/aliases", post: aliasesPost, get: aliasesGet}

var aliasCmd = Command{name: "images/aliases/{name:.*}", untrustedGet: true, get: aliasGet, delete: aliasDelete}
