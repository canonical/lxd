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
	"sync"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logging"

	log "gopkg.in/inconshreveable/log15.v2"
)

/* We only want a single publish running at any one time.
   The CPU and I/O load of publish is such that running multiple ones in
   parallel takes longer than running them serially.

   Additionaly, publishing the same container or container snapshot
   twice would lead to storage problem, not to mention a conflict at the
   end for whichever finishes last. */
var imagePublishLock sync.Mutex

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
		return []string{"-jxf"}, ".tar.bz2", nil
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

	command := "tar"
	args := []string{}
	if runningInUserns {
		args = append(args, "--wildcards")
		args = append(args, "--exclude=dev/*")
		args = append(args, "--exclude=./dev/*")
		args = append(args, "--exclude=rootfs/dev/*")
		args = append(args, "--exclude=rootfs/./dev/*")
	}
	args = append(args, "-C", path, "--numeric-owner")
	args = append(args, extractArgs...)
	args = append(args, tarball)

	output, err := exec.Command(command, args...).CombinedOutput()
	if err != nil {
		shared.Debugf("Unpacking failed")
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
		err = os.MkdirAll(rootfsPath, 0755)
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

func compressFile(path string, compress string) (string, error) {
	cmd := exec.Command(compress, path, "-c", "-n")

	outfile, err := os.Create(path + ".compressed")
	if err != nil {
		return "", err
	}

	defer outfile.Close()
	cmd.Stdout = outfile

	err = cmd.Run()
	if err != nil {
		os.Remove(outfile.Name())
		return "", err
	}

	return outfile.Name(), nil
}

type templateEntry struct {
	When       []string
	Template   string
	Properties map[string]string
}

type imagePostReq struct {
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
	Templates    map[string]*templateEntry `yaml:"templates"`
}

/*
 * This function takes a container or snapshot from the local image server and
 * exports it as an image.
 */
func imgPostContInfo(d *Daemon, r *http.Request, req imagePostReq,
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
		info.Public = true
	case false:
		info.Public = false
	}

	c, err := containerLoadByName(d, name)
	if err != nil {
		return info, err
	}

	// Build the actual image file
	tarfile, err := ioutil.TempFile(builddir, "lxd_build_tar_")
	if err != nil {
		return info, err
	}
	defer os.Remove(tarfile.Name())

	if err := c.Export(tarfile); err != nil {
		tarfile.Close()
		return info, fmt.Errorf("imgPostContInfo: export failed: %s", err)
	}
	tarfile.Close()

	compress, err := d.ConfigValueGet("images.compression_algorithm")
	if err != nil {
		return info, err
	}

	// Default to gzip for this
	if compress == "" {
		compress = "gzip"
	}

	var compressedPath string
	if compress != "none" {
		compressedPath, err = compressFile(tarfile.Name(), compress)
		if err != nil {
			return info, err
		}
	} else {
		compressedPath = tarfile.Name()
	}
	defer os.Remove(compressedPath)

	sha256 := sha256.New()
	tarf, err := os.Open(compressedPath)
	if err != nil {
		return info, err
	}
	info.Size, err = io.Copy(sha256, tarf)
	tarf.Close()
	if err != nil {
		return info, err
	}
	info.Fingerprint = fmt.Sprintf("%x", sha256.Sum(nil))

	_, err = dbImageGet(d.db, info.Fingerprint, false, true)
	if err == nil {
		return info, fmt.Errorf("The image already exists: %s", info.Fingerprint)
	}

	/* rename the the file to the expected name so our caller can use it */
	finalName := shared.VarPath("images", info.Fingerprint)
	err = shared.FileMove(compressedPath, finalName)
	if err != nil {
		return info, err
	}

	info.Architecture = c.Architecture()
	info.Properties = req.Properties

	return info, nil
}

func imgPostRemoteInfo(d *Daemon, req imagePostReq, op *operation) error {
	var err error
	var hash string

	if req.Source["alias"] != "" {
		if req.Source["mode"] == "pull" && req.Source["server"] != "" {
			hash, err = remoteGetImageFingerprint(d, req.Source["server"], req.Source["alias"])
			if err != nil {
				return err
			}
		} else {
			hash, err = dbImageAliasGet(d.db, req.Source["alias"])
			if err != nil {
				return err
			}
		}
	} else if req.Source["fingerprint"] != "" {
		hash = req.Source["fingerprint"]
	} else {
		return fmt.Errorf("must specify one of alias or fingerprint for init from image")
	}

	err = d.ImageDownload(op,
		req.Source["server"], hash, req.Source["secret"], false, false)

	if err != nil {
		return err
	}

	info, err := dbImageGet(d.db, hash, false, false)
	if err != nil {
		return err
	}

	if req.Public {
		err = dbImageSetPublic(d.db, info.Id, req.Public)
		if err != nil {
			return err
		}
	}

	metadata := make(map[string]string)
	metadata["fingerprint"] = info.Fingerprint
	metadata["size"] = strconv.FormatInt(info.Size, 10)
	op.UpdateMetadata(metadata)

	return nil
}

func imgPostURLInfo(d *Daemon, req imagePostReq, op *operation) error {
	var err error

	if req.Source["url"] == "" {
		return fmt.Errorf("Missing URL")
	}

	// Resolve the image URL
	if d.tlsconfig == nil {
		d.tlsconfig, err = shared.GetTLSConfig(d.certf, d.keyf)
		if err != nil {
			return err
		}
	}

	tr := &http.Transport{
		TLSClientConfig: d.tlsconfig,
		Dial:            shared.RFC3493Dialer,
		Proxy:           http.ProxyFromEnvironment,
	}

	myhttp := http.Client{
		Transport: tr,
	}

	head, err := http.NewRequest("HEAD", req.Source["url"], nil)
	if err != nil {
		return err
	}

	architecturesStr := []string{}
	for _, arch := range d.architectures {
		architecturesStr = append(architecturesStr, fmt.Sprintf("%d", arch))
	}

	head.Header.Set("User-Agent", shared.UserAgent)
	head.Header.Set("LXD-Server-Architectures", strings.Join(architecturesStr, ", "))
	head.Header.Set("LXD-Server-Version", shared.Version)

	raw, err := myhttp.Do(head)
	if err != nil {
		return err
	}

	hash := raw.Header.Get("LXD-Image-Hash")
	if hash == "" {
		return fmt.Errorf("Missing LXD-Image-Hash header")
	}

	url := raw.Header.Get("LXD-Image-URL")
	if url == "" {
		return fmt.Errorf("Missing LXD-Image-URL header")
	}

	// Import the image
	err = d.ImageDownload(op,
		url, hash, "", false, true)

	if err != nil {
		return err
	}

	info, err := dbImageGet(d.db, hash, false, false)
	if err != nil {
		return err
	}

	if req.Public {
		err = dbImageSetPublic(d.db, info.Id, req.Public)
		if err != nil {
			return err
		}
	}

	metadata := make(map[string]string)
	metadata["fingerprint"] = info.Fingerprint
	metadata["size"] = strconv.FormatInt(info.Size, 10)
	op.UpdateMetadata(metadata)

	return nil
}

func getImgPostInfo(d *Daemon, r *http.Request,
	builddir string, post *os.File) (info shared.ImageInfo, err error) {

	var imageMeta *imageMetadata
	logger := logging.AddContext(shared.Log, log.Ctx{"function": "getImgPostInfo"})

	public, _ := strconv.Atoi(r.Header.Get("X-LXD-public"))
	info.Public = public == 1
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

func dbInsertImage(d *Daemon, fp string, fname string, sz int64, public bool,
	arch int, creationDate int64, expiryDate int64, properties map[string]string) error {
	tx, err := dbBegin(d.db)
	if err != nil {
		return err
	}

	sqlPublic := 0
	if public {
		sqlPublic = 1
	}

	stmt, err := tx.Prepare(`INSERT INTO images (fingerprint, filename, size, public, architecture, creation_date, expiry_date, upload_date) VALUES (?, ?, ?, ?, ?, ?, ?, strftime("%s"))`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	result, err := stmt.Exec(fp, fname, sz, sqlPublic, arch, creationDate, expiryDate)
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

		// FIXME: InterfaceToBool is there for backward compatibility
		shared.InterfaceToBool(info.Public),

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

	// create a directory under which we keep everything while building
	builddir, err := ioutil.TempDir(shared.VarPath("images"), "lxd_build_")
	if err != nil {
		return InternalError(err)
	}

	cleanup := func(path string, fd *os.File) {
		if fd != nil {
			fd.Close()
		}

		if err := os.RemoveAll(path); err != nil {
			shared.Debugf("Error deleting temporary directory \"%s\": %s", path, err)
		}
	}

	// Store the post data to disk
	post, err := ioutil.TempFile(builddir, "lxd_post_")
	if err != nil {
		cleanup(builddir, nil)
		return InternalError(err)
	}

	_, err = io.Copy(post, r.Body)
	if err != nil {
		cleanup(builddir, post)
		return InternalError(err)
	}

	// Is this a container request?
	post.Seek(0, 0)
	decoder := json.NewDecoder(post)
	req := imagePostReq{}
	err = decoder.Decode(&req)
	imageUpload := err != nil

	if !imageUpload && !shared.StringInSlice(req.Source["type"], []string{"container", "snapshot", "image", "url"}) {
		cleanup(builddir, post)
		return InternalError(fmt.Errorf("Invalid images JSON"))
	}

	// Begin background operation
	run := func(op *operation) error {
		var info shared.ImageInfo

		// Setup the cleanup function
		defer cleanup(builddir, post)

		/* Processing image copy from remote */
		if !imageUpload && req.Source["type"] == "image" {
			err := imgPostRemoteInfo(d, req, op)
			if err != nil {
				return err
			}
			return nil
		}

		/* Processing image copy from URL */
		if !imageUpload && req.Source["type"] == "url" {
			err := imgPostURLInfo(d, req, op)
			if err != nil {
				return err
			}
			return nil
		}

		if imageUpload {
			/* Processing image upload */
			info, err = getImgPostInfo(d, r, builddir, post)
			if err != nil {
				return err
			}
		} else {
			/* Processing image creation from container */
			imagePublishLock.Lock()
			info, err = imgPostContInfo(d, r, req, builddir)
			if err != nil {
				imagePublishLock.Unlock()
				return err
			}
			imagePublishLock.Unlock()
		}

		metadata, err := imageBuildFromInfo(d, info)
		if err != nil {
			return err
		}

		op.UpdateMetadata(metadata)
		return nil
	}

	op, err := operationCreate(operationClassTask, nil, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
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

func doImagesGet(d *Daemon, recursion bool, public bool) (interface{}, error) {
	results, err := dbImagesGet(d.db, public)
	if err != nil {
		return []string{}, err
	}

	resultString := make([]string, len(results))
	resultMap := make([]shared.ImageInfo, len(results))
	i := 0
	for _, name := range results {
		if !recursion {
			url := fmt.Sprintf("/%s/images/%s", shared.APIVersion, name)
			resultString[i] = url
		} else {
			image, response := doImageGet(d, name, public)
			if response != nil {
				continue
			}
			resultMap[i] = image
		}

		i++
	}

	if !recursion {
		return resultString, nil
	}

	return resultMap, nil
}

func imagesGet(d *Daemon, r *http.Request) Response {
	public := !d.isTrustedClient(r)

	result, err := doImagesGet(d, d.isRecursionRequest(r), public)
	if err != nil {
		return SmartError(err)
	}
	return SyncResponse(true, result)
}

var imagesCmd = Command{name: "images", post: imagesPost, untrustedGet: true, get: imagesGet}

func doDeleteImage(d *Daemon, fingerprint string) error {
	imgInfo, err := dbImageGet(d.db, fingerprint, false, false)
	if err != nil {
		return err
	}

	// get storage before deleting images/$fp because we need to
	// look at the path
	s, err := storageForImage(d, imgInfo)
	if err != nil {
		return err
	}

	// Remove the image from storage backend
	if err = s.ImageDelete(imgInfo.Fingerprint); err != nil {
		return err
	}

	// Remove main image file
	fname := shared.VarPath("images", imgInfo.Fingerprint)
	if shared.PathExists(fname) {
		err = os.Remove(fname)
		if err != nil {
			shared.Debugf("Error deleting image file %s: %s", fname, err)
		}
	}

	// Remote the rootfs file
	fname = shared.VarPath("images", imgInfo.Fingerprint) + ".rootfs"
	if shared.PathExists(fname) {
		err = os.Remove(fname)
		if err != nil {
			shared.Debugf("Error deleting image file %s: %s", fname, err)
		}
	}

	// Remove the DB entry
	if err = dbImageDelete(d.db, imgInfo.Id); err != nil {
		return err
	}

	return nil
}

func imageDelete(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	if err := doDeleteImage(d, fingerprint); err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

func doImageGet(d *Daemon, fingerprint string, public bool) (shared.ImageInfo, Response) {
	imgInfo, err := dbImageGet(d.db, fingerprint, public, false)
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
	for _, op := range operations {
		if op.resources == nil {
			continue
		}

		opImages, ok := op.resources["images"]
		if !ok {
			continue
		}

		if !shared.StringInSlice(fingerprint, opImages) {
			continue
		}

		opSecret, ok := op.metadata["secret"]
		if !ok {
			continue
		}

		if opSecret == secret {
			// Token is single-use, so cancel it now
			op.Cancel()
			return true
		}
	}

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
	Public     bool              `json:"public"`
}

func imagePut(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	imageRaw := imagePutReq{}
	if err := json.NewDecoder(r.Body).Decode(&imageRaw); err != nil {
		return BadRequest(err)
	}

	imgInfo, err := dbImageGet(d.db, fingerprint, false, false)
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

	err = dbImageSetPublic(d.db, imgInfo.Id, imageRaw.Public)
	if err != nil {
		tx.Rollback()
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
	_, err := dbImageAliasGet(d.db, req.Name)
	if err == nil {
		return Conflict
	}

	imgInfo, err := dbImageGet(d.db, req.Target, false, false)
	if err != nil {
		return SmartError(err)
	}

	err = dbImageAliasAdd(d.db, req.Name, imgInfo.Id, req.Description)
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

	imgInfo, err := dbImageGet(d.db, fingerprint, public, false)
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
	_, err := dbImageGet(d.db, fingerprint, false, false)
	if err != nil {
		return SmartError(err)
	}

	secret, err := shared.RandomCryptoString()

	if err != nil {
		return InternalError(err)
	}

	meta := shared.Jmap{}
	meta["secret"] = secret

	resources := map[string][]string{}
	resources["images"] = []string{fingerprint}

	op, err := operationCreate(operationClassToken, resources, meta, nil, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

var imagesExportCmd = Command{name: "images/{fingerprint}/export", untrustedGet: true, get: imageExport}
var imagesSecretCmd = Command{name: "images/{fingerprint}/secret", post: imageSecret}

var aliasesCmd = Command{name: "images/aliases", post: aliasesPost, get: aliasesGet}

var aliasCmd = Command{name: "images/aliases/{name:.*}", untrustedGet: true, get: aliasGet, delete: aliasDelete}
