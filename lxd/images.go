package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

func getSize(f *os.File) (int64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func detectCompression(fname string) ([]string, string, error) {
	f, err := os.Open(fname)
	if err != nil {
		return []string{""}, "", err
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
	if err != nil {
		return []string{""}, "", err
	}

	switch {
	case bytes.Equal(header[0:2], []byte{'B', 'Z'}):
		return []string{"--jxf"}, ".tar.bz2", nil
	case bytes.Equal(header[0:2], []byte{0x1f, 0x8b}):
		return []string{"-zxf"}, ".tar.gz", nil
	case (bytes.Equal(header[1:5], []byte{'7', 'z', 'X', 'Z'}) && header[0] == 0xFD):
		return []string{"-Jxf"}, ".tar.xz", nil
	case (bytes.Equal(header[1:5], []byte{'7', 'z', 'X', 'Z'}) && header[0] != 0xFD):
		return []string{"--lzma", "-xf"}, ".tar.lzma", nil
	case bytes.Equal(header[257:262], []byte{'u', 's', 't', 'a', 'r'}):
		return []string{"-xf"}, ".tar", nil
	default:
		return []string{""}, "", fmt.Errorf("Unsupported compression.")
	}

}

func untar(tarball string, path string) error {
	extractArgs, _, err := detectCompression(tarball)
	if err != nil {
		return err
	}

	args := []string{"-C", path, "--numeric-owner"}
	args = append(args, extractArgs...)
	args = append(args, tarball)

	output, err := exec.Command("tar", args...).CombinedOutput()
	if err != nil {
		shared.Debugf("unpacking failed\n")
		shared.Debugf(string(output))
		return err
	}

	return nil
}

func untarImage(imagefname string, destpath string) error {
	err := untar(imagefname, destpath)
	if err != nil {
		return err
	}

	if shared.PathExists(imagefname + ".rootfs") {
		rootfsPath := fmt.Sprintf("%s/rootfs", destpath)
		err = os.MkdirAll(rootfsPath, 0700)
		if err != nil {
			return fmt.Errorf("Error creating rootfs directory")
		}

		err = untar(imagefname+".rootfs", rootfsPath)
		if err != nil {
			return err
		}
	}

	return nil
}

type imageFromContainerPostReq struct {
	Filename   string            `json:"filename"`
	Public     bool              `json:"public"`
	Source     map[string]string `json:"source"`
	Properties map[string]string `json:"properties"`
}

type imageMetadata struct {
	Architecture string                    `yaml:"architecture"`
	CreationDate int64                     `yaml:"creation_date"`
	ExpiryDate   int64                     `yaml:"expiry_date"`
	Properties   map[string]string         `yaml:"properties"`
	Templates    map[string]*TemplateEntry `yaml:"templates"`
}

/*
 * This function takes a container or snapshot from the local image server and
 * exports it as an image.
 */
func imgPostContInfo(d *Daemon, r *http.Request, req imageFromContainerPostReq,
	builddir string) (info shared.ImageInfo, err error) {

	info.Properties = map[string]string{}
	name := req.Source["name"]
	ctype := req.Source["type"]
	if ctype == "" || name == "" {
		return info, fmt.Errorf("No source provided")
	}

	switch ctype {
	case "snapshot":
		if !shared.IsSnapshot(name) {
			return info, fmt.Errorf("Not a snapshot")
		}
	case "container":
		if shared.IsSnapshot(name) {
			return info, fmt.Errorf("This is a snapshot")
		}
	default:
		return info, fmt.Errorf("Bad type")
	}

	info.Filename = req.Filename
	switch req.Public {
	case true:
		info.Public = 1
	case false:
		info.Public = 0
	}

	snap := ""
	if ctype == "snapshot" {
		fields := strings.SplitN(name, "/", 2)
		if len(fields) != 2 {
			return info, fmt.Errorf("Not a snapshot")
		}
		name = fields[0]
		snap = fields[1]
	}

	c, err := newLxdContainer(name, d)
	if err != nil {
		return info, err
	}

	// Build the actual image file
	tarfile, err := ioutil.TempFile(builddir, "lxd_build_tar_")
	if err != nil {
		return info, err
	}

	if err := c.exportToTar(snap, tarfile); err != nil {
		tarfile.Close()
		return info, fmt.Errorf("imgPostContInfo: exportToTar failed: %s\n", err)
	}
	tarfile.Close()

	_, err = exec.Command("gzip", tarfile.Name()).CombinedOutput()
	if err != nil {
		shared.Debugf("image compression\n")
		return info, err
	}
	gztarpath := fmt.Sprintf("%s.gz", tarfile.Name())

	sha256 := sha256.New()
	tarf, err := os.Open(gztarpath)
	if err != nil {
		return info, err
	}
	info.Size, err = io.Copy(sha256, tarf)
	tarf.Close()
	if err != nil {
		return info, err
	}
	info.Fingerprint = fmt.Sprintf("%x", sha256.Sum(nil))

	/* rename the the file to the expected name so our caller can use it */
	finalName := shared.VarPath("images", info.Fingerprint)
	err = shared.FileMove(gztarpath, finalName)
	if err != nil {
		return info, err
	}

	info.Architecture = c.architecture
	info.Properties = req.Properties

	return info, nil
}

func imgPostRemoteInfo(d *Daemon, req imageFromContainerPostReq) Response {
	var err error
	var hash string

	if req.Source["alias"] != "" {
		if req.Source["mode"] == "pull" && req.Source["server"] != "" {
			hash, err = remoteGetImageFingerprint(d, req.Source["server"], req.Source["alias"])
			if err != nil {
				return InternalError(err)
			}
		} else {

			hash, err = dbAliasGet(d.db, req.Source["alias"])
			if err != nil {
				return InternalError(err)
			}
		}
	} else if req.Source["fingerprint"] != "" {
		hash = req.Source["fingerprint"]
	} else {
		return BadRequest(fmt.Errorf("must specify one of alias or fingerprint for init from image"))
	}

	err = ensureLocalImage(d, req.Source["server"], hash, req.Source["secret"])
	if err != nil {
		return InternalError(err)
	}

	info, err := dbImageGet(d.db, hash, false)
	if err != nil {
		return InternalError(err)
	}

	metadata := make(map[string]string)
	metadata["fingerprint"] = info.Fingerprint
	metadata["size"] = strconv.FormatInt(info.Size, 10)

	return SyncResponse(true, metadata)
}

func getImgPostInfo(d *Daemon, r *http.Request,
	builddir string, post *os.File) (info shared.ImageInfo, err error) {

	var imageMeta *imageMetadata
	logger := shared.Log.New(log.Ctx{"function": "getImgPostInfo"})

	info.Public, _ = strconv.Atoi(r.Header.Get("X-LXD-public"))
	propHeaders := r.Header[http.CanonicalHeaderKey("X-LXD-properties")]
	ctype, ctypeParams, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		ctype = "application/octet-stream"
	}

	sha256 := sha256.New()
	var size int64

	// Create a temporary file for the image tarball
	imageTarf, err := ioutil.TempFile(builddir, "lxd_tar_")
	if err != nil {
		return info, err
	}

	if ctype == "multipart/form-data" {
		// Parse the POST data
		post.Seek(0, 0)
		mr := multipart.NewReader(post, ctypeParams["boundary"])

		// Get the metadata tarball
		part, err := mr.NextPart()
		if err != nil {
			return info, err
		}

		if part.FormName() != "metadata" {
			return info, fmt.Errorf("Invalid multipart image")
		}

		size, err = io.Copy(io.MultiWriter(imageTarf, sha256), part)
		info.Size += size

		imageTarf.Close()
		if err != nil {
			logger.Error(
				"Failed to copy the image tarfile",
				log.Ctx{"err": err})
			return info, err
		}

		// Get the rootfs tarball
		part, err = mr.NextPart()
		if err != nil {
			logger.Error(
				"Failed to get the next part",
				log.Ctx{"err": err})
			return info, err
		}

		if part.FormName() != "rootfs" {
			logger.Error(
				"Invalid multipart image")

			return info, fmt.Errorf("Invalid multipart image")
		}

		// Create a temporary file for the rootfs tarball
		rootfsTarf, err := ioutil.TempFile(builddir, "lxd_tar_")
		if err != nil {
			return info, err
		}

		size, err = io.Copy(io.MultiWriter(rootfsTarf, sha256), part)
		info.Size += size

		rootfsTarf.Close()
		if err != nil {
			logger.Error(
				"Failed to copy the rootfs tarfile",
				log.Ctx{"err": err})
			return info, err
		}

		info.Filename = part.FileName()
		info.Fingerprint = fmt.Sprintf("%x", sha256.Sum(nil))

		expectedFingerprint := r.Header.Get("X-LXD-fingerprint")
		if expectedFingerprint != "" && info.Fingerprint != expectedFingerprint {
			err = fmt.Errorf("fingerprints don't match, got %s expected %s", info.Fingerprint, expectedFingerprint)
			return info, err
		}

		imgfname := shared.VarPath("images", info.Fingerprint)
		err = shared.FileMove(imageTarf.Name(), imgfname)
		if err != nil {
			logger.Error(
				"Failed to move the image tarfile",
				log.Ctx{
					"err":    err,
					"source": imageTarf.Name(),
					"dest":   imgfname})
			return info, err
		}

		rootfsfname := shared.VarPath("images", info.Fingerprint+".rootfs")
		err = shared.FileMove(rootfsTarf.Name(), rootfsfname)
		if err != nil {
			logger.Error(
				"Failed to move the rootfs tarfile",
				log.Ctx{
					"err":    err,
					"source": rootfsTarf.Name(),
					"dest":   imgfname})
			return info, err
		}

		imageMeta, err = getImageMetadata(imgfname)
		if err != nil {
			logger.Error(
				"Failed to get image metadata",
				log.Ctx{"err": err})
			return info, err
		}
	} else {
		post.Seek(0, 0)
		size, err = io.Copy(io.MultiWriter(imageTarf, sha256), post)
		info.Size = size
		imageTarf.Close()
		logger.Debug("Tar size", log.Ctx{"size": size})
		if err != nil {
			logger.Error(
				"Failed to copy the tarfile",
				log.Ctx{"err": err})
			return info, err
		}

		info.Filename = r.Header.Get("X-LXD-filename")
		info.Fingerprint = fmt.Sprintf("%x", sha256.Sum(nil))

		expectedFingerprint := r.Header.Get("X-LXD-fingerprint")
		if expectedFingerprint != "" && info.Fingerprint != expectedFingerprint {
			logger.Error(
				"Fingerprints don't match",
				log.Ctx{
					"got":      info.Fingerprint,
					"expected": expectedFingerprint})
			err = fmt.Errorf(
				"fingerprints don't match, got %s expected %s",
				info.Fingerprint,
				expectedFingerprint)
			return info, err
		}

		imgfname := shared.VarPath("images", info.Fingerprint)
		err = shared.FileMove(imageTarf.Name(), imgfname)
		if err != nil {
			logger.Error(
				"Failed to move the tarfile",
				log.Ctx{
					"err":    err,
					"source": imageTarf.Name(),
					"dest":   imgfname})
			return info, err
		}

		imageMeta, err = getImageMetadata(imgfname)
		if err != nil {
			logger.Error(
				"Failed to get image metadata",
				log.Ctx{"err": err})
			return info, err
		}
	}

	info.Architecture, _ = shared.ArchitectureId(imageMeta.Architecture)
	info.CreationDate = imageMeta.CreationDate
	info.ExpiryDate = imageMeta.ExpiryDate

	info.Properties = imageMeta.Properties
	if len(propHeaders) > 0 {
		for _, ph := range propHeaders {
			p, _ := url.ParseQuery(ph)
			for pkey, pval := range p {
				info.Properties[pkey] = pval[0]
			}
		}
	}

	return info, nil
}

func dbInsertImage(d *Daemon, fp string, fname string, sz int64, public int,
	arch int, creationDate int64, expiryDate int64, properties map[string]string) error {
	tx, err := dbBegin(d.db)
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`INSERT INTO images (fingerprint, filename, size, public, architecture, creation_date, expiry_date, upload_date) VALUES (?, ?, ?, ?, ?, ?, ?, strftime("%s"))`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	result, err := stmt.Exec(fp, fname, sz, public, arch, creationDate, expiryDate)
	if err != nil {
		tx.Rollback()
		return err
	}

	if len(properties) > 0 {

		id64, err := result.LastInsertId()
		if err != nil {
			tx.Rollback()
			return err
		}
		id := int(id64)

		pstmt, err := tx.Prepare(`INSERT INTO images_properties (image_id, type, key, value) VALUES (?, 0, ?, ?)`)
		if err != nil {
			tx.Rollback()
			return err
		}
		defer pstmt.Close()

		for k, v := range properties {

			// we can assume, that there is just one
			// value per key
			_, err = pstmt.Exec(id, k, v)
			if err != nil {
				tx.Rollback()
				return err
			}
		}

	}

	if err := txCommit(tx); err != nil {
		return err
	}

	return nil
}

func imageBuildFromInfo(d *Daemon, info shared.ImageInfo) (metadata map[string]string, err error) {
	err = d.Storage.ImageCreate(info.Fingerprint)
	if err != nil {
		return metadata, err
	}

	err = dbInsertImage(
		d,
		info.Fingerprint,
		info.Filename,
		info.Size,
		info.Public,
		info.Architecture,
		info.CreationDate,
		info.ExpiryDate,
		info.Properties)
	if err != nil {
		return metadata, err
	}

	metadata = make(map[string]string)
	metadata["fingerprint"] = info.Fingerprint
	metadata["size"] = strconv.FormatInt(info.Size, 10)

	return metadata, nil
}

func imagesPost(d *Daemon, r *http.Request) Response {
	var err error
	var info shared.ImageInfo

	dirname := shared.VarPath("images")
	if err := os.MkdirAll(dirname, 0700); err != nil {
		return InternalError(err)
	}

	// create a directory under which we keep everything while building
	builddir, err := ioutil.TempDir(dirname, "lxd_build_")
	if err != nil {
		return InternalError(err)
	}

	/* remove the builddir when done */
	defer func() {
		if err := os.RemoveAll(builddir); err != nil {
			shared.Debugf("Error deleting temporary directory: %s", err)
		}
	}()

	// Store the post data to disk
	post, err := ioutil.TempFile(builddir, "lxd_post_")
	if err != nil {
		return InternalError(err)
	}

	os.Remove(post.Name())
	defer post.Close()

	_, err = io.Copy(post, r.Body)
	if err != nil {
		return InternalError(err)
	}

	// Is this a container request?
	post.Seek(0, 0)
	decoder := json.NewDecoder(post)
	req := imageFromContainerPostReq{}
	err = decoder.Decode(&req)

	if err == nil {
		/* Processing image request */
		if req.Source["type"] == "container" || req.Source["type"] == "snapshot" {
			info, err = imgPostContInfo(d, r, req, builddir)
			if err != nil {
				return SmartError(err)
			}
		} else if req.Source["type"] == "image" {
			return imgPostRemoteInfo(d, req)
		} else {
			return InternalError(fmt.Errorf("Invalid images JSON"))
		}
	} else {
		/* Processing image upload */
		info, err = getImgPostInfo(d, r, builddir, post)
		if err != nil {
			return SmartError(err)
		}
	}

	defer func() {
		if err := os.RemoveAll(builddir); err != nil {
			shared.Log.Error(
				"Deleting temporary directory",
				log.Ctx{"builddir": builddir, "err": err})
		}
	}()

	metadata, err := imageBuildFromInfo(d, info)
	if err != nil {
		return SmartError(err)
	}

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
	metadataName := "metadata.yaml"

	compressionArgs, _, err := detectCompression(fname)

	if err != nil {
		return nil, fmt.Errorf(
			"detectCompression failed, err='%v', tarfile='%s'",
			err,
			fname)
	}

	args := []string{"-O"}
	args = append(args, compressionArgs...)
	args = append(args, fname, metadataName)

	shared.Debugf("Extracting metadata.yaml using command: tar %s", strings.Join(args, " "))

	// read the metadata.yaml
	output, err := exec.Command("tar", args...).CombinedOutput()

	if err != nil {
		outputLines := strings.Split(string(output), "\n")
		return nil, fmt.Errorf("Could not extract image metadata %s from tar: %v (%s)", metadataName, err, outputLines[0])
	}

	metadata := new(imageMetadata)
	err = yaml.Unmarshal(output, &metadata)

	if err != nil {
		return nil, fmt.Errorf("Could not parse %s: %v", metadataName, err)
	}

	return metadata, nil
}

func imagesGet(d *Daemon, r *http.Request) Response {
	public := !d.isTrustedClient(r)

	result, err := doImagesGet(d, d.isRecursionRequest(r), public)
	if err != nil {
		return SmartError(err)
	}
	return SyncResponse(true, result)
}

func doImagesGet(d *Daemon, recursion bool, public bool) (interface{}, error) {
	resultString := []string{}
	resultMap := []shared.ImageInfo{}

	q := "SELECT fingerprint FROM images"
	var name string
	if public == true {
		q = "SELECT fingerprint FROM images WHERE public=1"
	}
	inargs := []interface{}{}
	outfmt := []interface{}{name}
	results, err := dbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	for _, r := range results {
		name = r[0].(string)
		if !recursion {
			url := fmt.Sprintf("/%s/images/%s", shared.APIVersion, name)
			resultString = append(resultString, url)
		} else {
			image, response := doImageGet(d, name, public)
			if response != nil {
				continue
			}
			resultMap = append(resultMap, image)
		}
	}

	if !recursion {
		return resultString, nil
	}

	return resultMap, nil
}

var imagesCmd = Command{name: "images", post: imagesPost, untrustedGet: true, get: imagesGet}

func imageDelete(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	imgInfo, err := dbImageGet(d.db, fingerprint, false)
	if err != nil {
		return SmartError(err)
	}

	if err = dbImageDelete(d.db, imgInfo.Id); err != nil {
		return SmartError(err)
	}

	fname := shared.VarPath("images", imgInfo.Fingerprint)
	err = os.Remove(fname)
	if err != nil {
		shared.Debugf("Error deleting image file %s: %s\n", fname, err)
	}

	if err = d.Storage.ImageDelete(imgInfo.Fingerprint); err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

func doImageGet(d *Daemon, fingerprint string, public bool) (shared.ImageInfo, Response) {
	imgInfo, err := dbImageGet(d.db, fingerprint, public)
	if err != nil {
		return shared.ImageInfo{}, SmartError(err)
	}

	q := "SELECT key, value FROM images_properties where image_id=?"
	var key, value, name, desc string
	inargs := []interface{}{imgInfo.Id}
	outfmt := []interface{}{key, value}
	results, err := dbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return shared.ImageInfo{}, SmartError(err)
	}
	properties := map[string]string{}
	for _, r := range results {
		key = r[0].(string)
		value = r[1].(string)
		properties[key] = value
	}

	q = "SELECT name, description FROM images_aliases WHERE image_id=?"
	inargs = []interface{}{imgInfo.Id}
	outfmt = []interface{}{name, desc}
	results, err = dbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return shared.ImageInfo{}, InternalError(err)
	}
	aliases := shared.ImageAliases{}
	for _, r := range results {
		name = r[0].(string)
		desc = r[0].(string)
		a := shared.ImageAlias{Name: name, Description: desc}
		aliases = append(aliases, a)
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
	Properties map[string]string `json:"properties"`
}

func imagePut(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	imageRaw := imagePutReq{}
	if err := json.NewDecoder(r.Body).Decode(&imageRaw); err != nil {
		return BadRequest(err)
	}

	imgInfo, err := dbImageGet(d.db, fingerprint, false)
	if err != nil {
		return SmartError(err)
	}

	tx, err := dbBegin(d.db)
	if err != nil {
		return InternalError(err)
	}

	_, err = tx.Exec(`DELETE FROM images_properties WHERE image_id=?`, imgInfo.Id)

	stmt, err := tx.Prepare(`INSERT INTO images_properties (image_id, type, key, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return InternalError(err)
	}

	for key, value := range imageRaw.Properties {
		_, err = stmt.Exec(imgInfo.Id, 0, key, value)
		if err != nil {
			tx.Rollback()
			return InternalError(err)
		}
	}

	if err := txCommit(tx); err != nil {
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

	// This is just to see if the alias name already exists.
	_, err := dbAliasGet(d.db, req.Name)
	if err == nil {
		return Conflict
	}

	imgInfo, err := dbImageGet(d.db, req.Target, false)
	if err != nil {
		return SmartError(err)
	}

	err = dbAddAlias(d.db, req.Name, imgInfo.Id, req.Description)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

func aliasesGet(d *Daemon, r *http.Request) Response {
	recursion := d.isRecursionRequest(r)

	q := "SELECT name FROM images_aliases"
	var name string
	inargs := []interface{}{}
	outfmt := []interface{}{name}
	results, err := dbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return BadRequest(err)
	}
	responseStr := []string{}
	responseMap := []shared.ImageAlias{}
	for _, res := range results {
		name = res[0].(string)
		if !recursion {
			url := fmt.Sprintf("/%s/images/aliases/%s", shared.APIVersion, name)
			responseStr = append(responseStr, url)

		} else {
			alias, err := doAliasGet(d, name, d.isTrustedClient(r))
			if err != nil {
				continue
			}
			responseMap = append(responseMap, alias)
		}
	}

	if !recursion {
		return SyncResponse(true, responseStr)
	}

	return SyncResponse(true, responseMap)
}

func aliasGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	alias, err := doAliasGet(d, name, d.isTrustedClient(r))
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, alias)
}

func doAliasGet(d *Daemon, name string, isTrustedClient bool) (shared.ImageAlias, error) {
	q := `SELECT images.fingerprint, images_aliases.description
			 FROM images_aliases
			 INNER JOIN images
			 ON images_aliases.image_id=images.id
			 WHERE images_aliases.name=?`
	if !isTrustedClient {
		q = q + ` AND images.public=1`
	}

	var fingerprint, description string
	arg1 := []interface{}{name}
	arg2 := []interface{}{&fingerprint, &description}
	err := dbQueryRowScan(d.db, q, arg1, arg2)
	if err != nil {
		return shared.ImageAlias{}, err
	}

	return shared.ImageAlias{Name: fingerprint, Description: description}, nil
}

func aliasDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	_, _ = dbExec(d.db, "DELETE FROM images_aliases WHERE name=?", name)

	return EmptySyncResponse
}

func imageExport(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	public := !d.isTrustedClient(r)
	secret := r.FormValue("secret")

	if public == true && imageValidSecret(fingerprint, secret) == true {
		public = false
	}

	imgInfo, err := dbImageGet(d.db, fingerprint, public)
	if err != nil {
		return SmartError(err)
	}

	filename := imgInfo.Filename
	imagePath := shared.VarPath("images", imgInfo.Fingerprint)
	rootfsPath := imagePath + ".rootfs"
	if filename == "" {
		_, ext, err := detectCompression(imagePath)
		if err != nil {
			ext = ""
		}
		filename = fmt.Sprintf("%s%s", fingerprint, ext)
	}

	if shared.PathExists(rootfsPath) {
		files := make([]fileResponseEntry, 2)

		files[0].identifier = "metadata"
		files[0].path = imagePath
		files[0].filename = "meta-" + filename

		files[1].identifier = "rootfs"
		files[1].path = rootfsPath
		files[1].filename = filename

		return FileResponse(r, files, nil, false)
	}

	files := make([]fileResponseEntry, 1)
	files[0].identifier = filename
	files[0].path = imagePath
	files[0].filename = filename

	return FileResponse(r, files, nil, false)
}

func imageSecret(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]
	_, err := dbImageGet(d.db, fingerprint, false)
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

var imagesExportCmd = Command{name: "images/{fingerprint}/export", untrustedGet: true, get: imageExport}
var imagesSecretCmd = Command{name: "images/{fingerprint}/secret", post: imageSecret}

var aliasesCmd = Command{name: "images/aliases", post: aliasesPost, get: aliasesGet}

var aliasCmd = Command{name: "images/aliases/{name:.*}", untrustedGet: true, get: aliasGet, delete: aliasDelete}
