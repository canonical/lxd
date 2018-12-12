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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"gopkg.in/yaml.v2"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
	"github.com/lxc/lxd/shared/osarch"
	"github.com/lxc/lxd/shared/version"

	log "github.com/lxc/lxd/shared/log15"
)

var imagesCmd = Command{
	name:         "images",
	post:         imagesPost,
	untrustedGet: true,
	get:          imagesGet,
}

var imageCmd = Command{
	name:         "images/{fingerprint}",
	untrustedGet: true,
	get:          imageGet,
	put:          imagePut,
	delete:       imageDelete,
	patch:        imagePatch,
}

var imageExportCmd = Command{
	name:         "images/{fingerprint}/export",
	untrustedGet: true,
	get:          imageExport,
}

var imageSecretCmd = Command{
	name: "images/{fingerprint}/secret",
	post: imageSecret,
}

var imageRefreshCmd = Command{
	name: "images/{fingerprint}/refresh",
	post: imageRefresh,
}

var aliasesCmd = Command{
	name: "images/aliases",
	post: aliasesPost,
	get:  aliasesGet,
}

var aliasCmd = Command{
	name:         "images/aliases/{name:.*}",
	untrustedGet: true,
	get:          aliasGet,
	delete:       aliasDelete,
	put:          aliasPut,
	post:         aliasPost,
	patch:        aliasPatch,
}

/* We only want a single publish running at any one time.
   The CPU and I/O load of publish is such that running multiple ones in
   parallel takes longer than running them serially.

   Additionally, publishing the same container or container snapshot
   twice would lead to storage problem, not to mention a conflict at the
   end for whichever finishes last. */
var imagePublishLock sync.Mutex

func unpackImage(imagefname string, destpath string, sType storageType, runningInUserns bool) error {
	blockBackend := false

	if sType == storageTypeLvm || sType == storageTypeCeph {
		blockBackend = true
	}

	err := shared.Unpack(imagefname, destpath, blockBackend, runningInUserns)
	if err != nil {
		return err
	}

	rootfsPath := fmt.Sprintf("%s/rootfs", destpath)
	if shared.PathExists(imagefname + ".rootfs") {
		err = os.MkdirAll(rootfsPath, 0755)
		if err != nil {
			return fmt.Errorf("Error creating rootfs directory")
		}

		err = shared.Unpack(imagefname+".rootfs", rootfsPath, blockBackend, runningInUserns)
		if err != nil {
			return err
		}
	}

	if !shared.PathExists(rootfsPath) {
		return fmt.Errorf("Image is missing a rootfs: %s", imagefname)
	}

	return nil
}

func compressFile(path string, compress string) (string, error) {
	reproducible := []string{"gzip"}

	args := []string{"-c"}
	if shared.StringInSlice(compress, reproducible) {
		args = append(args, "-n")
	}

	args = append(args, path)
	cmd := exec.Command(compress, args...)

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

/*
 * This function takes a container or snapshot from the local image server and
 * exports it as an image.
 */
func imgPostContInfo(d *Daemon, r *http.Request, req api.ImagesPost, builddir string) (*api.Image, error) {
	info := api.Image{}
	info.Properties = map[string]string{}
	project := projectParam(r)
	name := req.Source.Name
	ctype := req.Source.Type
	if ctype == "" || name == "" {
		return nil, fmt.Errorf("No source provided")
	}

	switch ctype {
	case "snapshot":
		if !shared.IsSnapshot(name) {
			return nil, fmt.Errorf("Not a snapshot")
		}
	case "container":
		if shared.IsSnapshot(name) {
			return nil, fmt.Errorf("This is a snapshot")
		}
	default:
		return nil, fmt.Errorf("Bad type")
	}

	info.Filename = req.Filename
	switch req.Public {
	case true:
		info.Public = true
	case false:
		info.Public = false
	}

	c, err := containerLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return nil, err
	}

	// Build the actual image file
	tarfile, err := ioutil.TempFile(builddir, "lxd_build_tar_")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tarfile.Name())

	if err := c.Export(tarfile, req.Properties); err != nil {
		tarfile.Close()
		return nil, err
	}
	tarfile.Close()

	var compressedPath string
	var compress string

	if req.CompressionAlgorithm != "" {
		compress = req.CompressionAlgorithm
	} else {
		compress, err = cluster.ConfigGetString(d.cluster, "images.compression_algorithm")
		if err != nil {
			return nil, err
		}
	}

	if compress != "none" {
		compressedPath, err = compressFile(tarfile.Name(), compress)
		if err != nil {
			return nil, err
		}
	} else {
		compressedPath = tarfile.Name()
	}
	defer os.Remove(compressedPath)

	sha256 := sha256.New()
	tarf, err := os.Open(compressedPath)
	if err != nil {
		return nil, err
	}

	info.Size, err = io.Copy(sha256, tarf)
	tarf.Close()
	if err != nil {
		return nil, err
	}

	info.Fingerprint = fmt.Sprintf("%x", sha256.Sum(nil))

	_, _, err = d.cluster.ImageGet(project, info.Fingerprint, false, true)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return nil, err
		}

		return nil, fmt.Errorf("The image already exists: %s", info.Fingerprint)
	}

	/* rename the the file to the expected name so our caller can use it */
	finalName := shared.VarPath("images", info.Fingerprint)
	err = shared.FileMove(compressedPath, finalName)
	if err != nil {
		return nil, err
	}

	info.Architecture, _ = osarch.ArchitectureName(c.Architecture())
	info.Properties = req.Properties

	// Create the database entry
	err = d.cluster.ImageInsert(c.Project(), info.Fingerprint, info.Filename, info.Size, info.Public, info.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties)
	if err != nil {
		return nil, err
	}

	return &info, nil
}

func imgPostRemoteInfo(d *Daemon, req api.ImagesPost, op *operation, project string) (*api.Image, error) {
	var err error
	var hash string

	if req.Source.Fingerprint != "" {
		hash = req.Source.Fingerprint
	} else if req.Source.Alias != "" {
		hash = req.Source.Alias
	} else {
		return nil, fmt.Errorf("must specify one of alias or fingerprint for init from image")
	}

	info, err := d.ImageDownload(op, req.Source.Server, req.Source.Protocol, req.Source.Certificate, req.Source.Secret, hash, false, req.AutoUpdate, "", false, project)
	if err != nil {
		return nil, err
	}

	id, info, err := d.cluster.ImageGet("default", info.Fingerprint, false, true)
	if err != nil {
		return nil, err
	}

	// Allow overriding or adding properties
	for k, v := range req.Properties {
		info.Properties[k] = v
	}

	// Update the DB record if needed
	if req.Public || req.AutoUpdate || req.Filename != "" || len(req.Properties) > 0 {
		err = d.cluster.ImageUpdate(id, req.Filename, info.Size, req.Public, req.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties)
		if err != nil {
			return nil, err
		}
	}

	return info, nil
}

func imgPostURLInfo(d *Daemon, req api.ImagesPost, op *operation, project string) (*api.Image, error) {
	var err error

	if req.Source.URL == "" {
		return nil, fmt.Errorf("Missing URL")
	}

	myhttp, err := util.HTTPClient("", d.proxy)
	if err != nil {
		return nil, err
	}

	// Resolve the image URL
	head, err := http.NewRequest("HEAD", req.Source.URL, nil)
	if err != nil {
		return nil, err
	}

	architecturesStr := []string{}
	for _, arch := range d.os.Architectures {
		architecturesStr = append(architecturesStr, fmt.Sprintf("%d", arch))
	}

	head.Header.Set("User-Agent", version.UserAgent)
	head.Header.Set("LXD-Server-Architectures", strings.Join(architecturesStr, ", "))
	head.Header.Set("LXD-Server-Version", version.Version)

	raw, err := myhttp.Do(head)
	if err != nil {
		return nil, err
	}

	hash := raw.Header.Get("LXD-Image-Hash")
	if hash == "" {
		return nil, fmt.Errorf("Missing LXD-Image-Hash header")
	}

	url := raw.Header.Get("LXD-Image-URL")
	if url == "" {
		return nil, fmt.Errorf("Missing LXD-Image-URL header")
	}

	// Import the image
	info, err := d.ImageDownload(op, url, "direct", "", "", hash, false, req.AutoUpdate, "", false, project)
	if err != nil {
		return nil, err
	}

	id, info, err := d.cluster.ImageGet(project, info.Fingerprint, false, false)
	if err != nil {
		return nil, err
	}

	// Allow overriding or adding properties
	for k, v := range req.Properties {
		info.Properties[k] = v
	}

	if req.Public || req.AutoUpdate || req.Filename != "" || len(req.Properties) > 0 {
		err = d.cluster.ImageUpdate(id, req.Filename, info.Size, req.Public, req.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties)
		if err != nil {
			return nil, err
		}
	}

	return info, nil
}

func getImgPostInfo(d *Daemon, r *http.Request, builddir string, project string, post *os.File) (*api.Image, error) {
	info := api.Image{}
	var imageMeta *api.ImageMetadata
	logger := logging.AddContext(logger.Log, log.Ctx{"function": "getImgPostInfo"})

	info.Public = shared.IsTrue(r.Header.Get("X-LXD-public"))
	propHeaders := r.Header[http.CanonicalHeaderKey("X-LXD-properties")]
	ctype, ctypeParams, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		ctype = "application/octet-stream"
	}

	sha256 := sha256.New()
	var size int64

	if ctype == "multipart/form-data" {
		// Create a temporary file for the image tarball
		imageTarf, err := ioutil.TempFile(builddir, "lxd_tar_")
		if err != nil {
			return nil, err
		}
		defer os.Remove(imageTarf.Name())

		// Parse the POST data
		post.Seek(0, 0)
		mr := multipart.NewReader(post, ctypeParams["boundary"])

		// Get the metadata tarball
		part, err := mr.NextPart()
		if err != nil {
			return nil, err
		}

		if part.FormName() != "metadata" {
			return nil, fmt.Errorf("Invalid multipart image")
		}

		size, err = io.Copy(io.MultiWriter(imageTarf, sha256), part)
		info.Size += size

		imageTarf.Close()
		if err != nil {
			logger.Error(
				"Failed to copy the image tarfile",
				log.Ctx{"err": err})
			return nil, err
		}

		// Get the rootfs tarball
		part, err = mr.NextPart()
		if err != nil {
			logger.Error(
				"Failed to get the next part",
				log.Ctx{"err": err})
			return nil, err
		}

		if part.FormName() != "rootfs" {
			logger.Error(
				"Invalid multipart image")

			return nil, fmt.Errorf("Invalid multipart image")
		}

		// Create a temporary file for the rootfs tarball
		rootfsTarf, err := ioutil.TempFile(builddir, "lxd_tar_")
		if err != nil {
			return nil, err
		}
		defer os.Remove(rootfsTarf.Name())

		size, err = io.Copy(io.MultiWriter(rootfsTarf, sha256), part)
		info.Size += size

		rootfsTarf.Close()
		if err != nil {
			logger.Error(
				"Failed to copy the rootfs tarfile",
				log.Ctx{"err": err})
			return nil, err
		}

		info.Filename = part.FileName()
		info.Fingerprint = fmt.Sprintf("%x", sha256.Sum(nil))

		expectedFingerprint := r.Header.Get("X-LXD-fingerprint")
		if expectedFingerprint != "" && info.Fingerprint != expectedFingerprint {
			err = fmt.Errorf("fingerprints don't match, got %s expected %s", info.Fingerprint, expectedFingerprint)
			return nil, err
		}

		imageMeta, err = getImageMetadata(imageTarf.Name())
		if err != nil {
			logger.Error(
				"Failed to get image metadata",
				log.Ctx{"err": err})
			return nil, err
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
			return nil, err
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
			return nil, err
		}
	} else {
		post.Seek(0, 0)
		size, err = io.Copy(sha256, post)
		info.Size = size
		logger.Debug("Tar size", log.Ctx{"size": size})
		if err != nil {
			logger.Error(
				"Failed to copy the tarfile",
				log.Ctx{"err": err})
			return nil, err
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
			return nil, err
		}

		imageMeta, err = getImageMetadata(post.Name())
		if err != nil {
			logger.Error(
				"Failed to get image metadata",
				log.Ctx{"err": err})
			return nil, err
		}

		imgfname := shared.VarPath("images", info.Fingerprint)
		err = shared.FileMove(post.Name(), imgfname)
		if err != nil {
			logger.Error(
				"Failed to move the tarfile",
				log.Ctx{
					"err":    err,
					"source": post.Name(),
					"dest":   imgfname})
			return nil, err
		}
	}

	info.Architecture = imageMeta.Architecture
	info.CreatedAt = time.Unix(imageMeta.CreationDate, 0)
	info.ExpiresAt = time.Unix(imageMeta.ExpiryDate, 0)

	info.Properties = imageMeta.Properties
	if len(propHeaders) > 0 {
		for _, ph := range propHeaders {
			p, _ := url.ParseQuery(ph)
			for pkey, pval := range p {
				info.Properties[pkey] = pval[0]
			}
		}
	}

	// Check if the image already exists
	exists, err := d.cluster.ImageExists(project, info.Fingerprint)
	if err != nil {
		return nil, err
	}
	if exists {
		// Do not create a database entry if the request is coming from the internal
		// cluster communications for image synchronization
		if isClusterNotification(r) {
			err := d.cluster.ImageAssociateNode(project, info.Fingerprint)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("Image with same fingerprint already exists")
		}
	} else {
		// Create the database entry
		err = d.cluster.ImageInsert(project, info.Fingerprint, info.Filename, info.Size, info.Public, info.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties)
		if err != nil {
			return nil, err
		}
	}

	return &info, nil
}

// imageCreateInPool() creates a new storage volume in a given storage pool for
// the image. No entry in the images database will be created. This implies that
// imageCreateinPool() should only be called when an image already exists in the
// database and hence has already a storage volume in at least one storage pool.
func imageCreateInPool(d *Daemon, info *api.Image, storagePool string) error {
	if storagePool == "" {
		return fmt.Errorf("No storage pool specified")
	}

	// Initialize a new storage interface.
	s, err := storagePoolInit(d.State(), storagePool)
	if err != nil {
		return err
	}

	// Create the storage volume for the image on the requested storage
	// pool.
	err = s.ImageCreate(info.Fingerprint)
	if err != nil {
		return err
	}

	return nil
}

func imagesPost(d *Daemon, r *http.Request) Response {
	project := projectParam(r)

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

		err := os.RemoveAll(path)
		if err != nil {
			logger.Debugf("Error deleting temporary directory \"%s\": %s", path, err)
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
	imageUpload := false

	req := api.ImagesPost{}
	err = decoder.Decode(&req)
	if err != nil {
		if r.Header.Get("Content-Type") == "application/json" {
			cleanup(builddir, post)
			return BadRequest(err)
		}

		imageUpload = true
	}

	if !imageUpload && !shared.StringInSlice(req.Source.Type, []string{"container", "snapshot", "image", "url"}) {
		cleanup(builddir, post)
		return InternalError(fmt.Errorf("Invalid images JSON"))
	}

	/* Forward requests for containers on other nodes */
	if !imageUpload && shared.StringInSlice(req.Source.Type, []string{"container", "snapshot"}) {
		name := req.Source.Name
		if name != "" {
			post.Seek(0, 0)
			r.Body = post
			response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name)
			if err != nil {
				cleanup(builddir, post)
				return SmartError(err)
			}

			if response != nil {
				cleanup(builddir, nil)
				return response
			}
		}
	}

	// Begin background operation
	run := func(op *operation) error {
		var err error
		var info *api.Image

		// Setup the cleanup function
		defer cleanup(builddir, post)

		if imageUpload {
			/* Processing image upload */
			info, err = getImgPostInfo(d, r, builddir, project, post)
		} else {
			if req.Source.Type == "image" {
				/* Processing image copy from remote */
				info, err = imgPostRemoteInfo(d, req, op, project)
			} else if req.Source.Type == "url" {
				/* Processing image copy from URL */
				info, err = imgPostURLInfo(d, req, op, project)
			} else {
				/* Processing image creation from container */
				imagePublishLock.Lock()
				info, err = imgPostContInfo(d, r, req, builddir)
				imagePublishLock.Unlock()
			}
		}
		if err != nil {
			return err
		}

		// Apply any provided alias
		for _, alias := range req.Aliases {
			_, _, err := d.cluster.ImageAliasGet(project, alias.Name, true)
			if err != db.ErrNoSuchObject {
				if err != nil {
					return errors.Wrapf(err, "Fetch image alias %q", alias.Name)
				}

				return fmt.Errorf("Alias already exists: %s", alias.Name)
			}

			id, _, err := d.cluster.ImageGet(project, info.Fingerprint, false, false)
			if err != nil {
				return errors.Wrapf(err, "Fetch image %q", info.Fingerprint)
			}

			err = d.cluster.ImageAliasAdd(project, alias.Name, id, alias.Description)
			if err != nil {
				return errors.Wrapf(err, "Add new image alias to the database")
			}
		}

		// Sync the images between each node in the cluster on demand
		err = imageSyncBetweenNodes(d, req, info.Fingerprint)
		if err != nil {
			return errors.Wrapf(err, "Image sync between nodes")
		}

		// Set the metadata
		metadata := make(map[string]string)
		metadata["fingerprint"] = info.Fingerprint
		metadata["size"] = strconv.FormatInt(info.Size, 10)
		op.UpdateMetadata(metadata)
		return nil
	}

	op, err := operationCreate(d.cluster, project, operationClassTask, db.OperationImageDownload, nil, nil, run, nil, nil)
	if err != nil {
		cleanup(builddir, post)
		return InternalError(err)
	}

	return OperationResponse(op)
}

func getImageMetadata(fname string) (*api.ImageMetadata, error) {
	metadataName := "metadata.yaml"

	compressionArgs, _, _, err := shared.DetectCompression(fname)

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
	output, err := shared.RunCommand("tar", args...)

	if err != nil {
		outputLines := strings.Split(output, "\n")
		return nil, fmt.Errorf("Could not extract image %s from tar: %v (%s)", metadataName, err, outputLines[0])
	}

	metadata := api.ImageMetadata{}
	err = yaml.Unmarshal([]byte(output), &metadata)

	if err != nil {
		return nil, fmt.Errorf("Could not parse %s: %v", metadataName, err)
	}

	_, err = osarch.ArchitectureId(metadata.Architecture)
	if err != nil {
		return nil, err
	}

	if metadata.CreationDate == 0 {
		return nil, fmt.Errorf("Missing creation date")
	}

	return &metadata, nil
}

func doImagesGet(d *Daemon, recursion bool, project string, public bool) (interface{}, error) {
	results, err := d.cluster.ImagesGet(project, public)
	if err != nil {
		return []string{}, err
	}

	resultString := make([]string, len(results))
	resultMap := make([]*api.Image, len(results))
	i := 0
	for _, name := range results {
		if !recursion {
			url := fmt.Sprintf("/%s/images/%s", version.APIVersion, name)
			resultString[i] = url
		} else {
			image, response := doImageGet(d.cluster, project, name, public)
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
	project := projectParam(r)
	public := d.checkTrustedClient(r) != nil

	result, err := doImagesGet(d, util.IsRecursionRequest(r), project, public)
	if err != nil {
		return SmartError(err)
	}
	return SyncResponse(true, result)
}

func autoUpdateImagesTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		opRun := func(op *operation) error {
			return autoUpdateImages(ctx, d)
		}

		op, err := operationCreate(d.cluster, "", operationClassTask, db.OperationImagesUpdate, nil, nil, opRun, nil, nil)
		if err != nil {
			logger.Error("Failed to start image update operation", log.Ctx{"err": err})
		}

		logger.Infof("Updating images")
		_, err = op.Run()
		if err != nil {
			logger.Error("Failed to update images", log.Ctx{"err": err})
		}
		logger.Infof("Done updating images")
	}

	schedule := func() (time.Duration, error) {
		var interval time.Duration
		err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
			config, err := cluster.ConfigLoad(tx)
			if err != nil {
				return errors.Wrap(err, "failed to load cluster configuration")
			}
			interval = config.AutoUpdateInterval()
			return nil
		})
		if err != nil {
			return 0, err
		}
		return interval, nil
	}
	return f, schedule
}

func autoUpdateImages(ctx context.Context, d *Daemon) error {
	projectNames := []string{}
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		projects, err := tx.ProjectList(db.ProjectFilter{})
		if err != nil {
			return err
		}

		for _, project := range projects {
			projectNames = append(projectNames, project.Name)
		}

		return nil
	})
	if err != nil {
		return errors.Wrap(err, "Unable to retrieve project names")
	}

	for _, project := range projectNames {
		err := autoUpdateImagesInProject(ctx, d, project)
		if err != nil {
			return errors.Wrapf(err, "Unable to update images for project %s", project)
		}
	}

	return nil
}

func autoUpdateImagesInProject(ctx context.Context, d *Daemon, project string) error {
	images, err := d.cluster.ImagesGet(project, false)
	if err != nil {
		return errors.Wrap(err, "Unable to retrieve the list of images")
	}

	for _, fingerprint := range images {
		id, info, err := d.cluster.ImageGet(project, fingerprint, false, true)
		if err != nil {
			logger.Error("Error loading image", log.Ctx{"err": err, "fp": fingerprint, "project": project})
			continue
		}

		if !info.AutoUpdate {
			continue
		}

		// FIXME: since our APIs around image downloading don't support
		//        cancelling, we run the function in a different
		//        goroutine and simply abort when the context expires.
		ch := make(chan struct{})
		go func() {
			autoUpdateImage(d, nil, id, info, project)
			ch <- struct{}{}
		}()
		select {
		case <-ctx.Done():
			return nil
		case <-ch:
		}
	}

	return nil
}

// Update a single image.  The operation can be nil, if no progress tracking is needed.
// Returns whether the image has been updated.
func autoUpdateImage(d *Daemon, op *operation, id int, info *api.Image, project string) error {
	fingerprint := info.Fingerprint
	_, source, err := d.cluster.ImageSourceGet(id)
	if err != nil {
		logger.Error("Error getting source image", log.Ctx{"err": err, "fp": fingerprint})
		return err
	}

	// Get the IDs of all storage pools on which a storage volume
	// for the requested image currently exists.
	poolIDs, err := d.cluster.ImageGetPools(fingerprint)
	if err != nil {
		logger.Error("Error getting image pools", log.Ctx{"err": err, "fp": fingerprint})
		return err
	}

	// Translate the IDs to poolNames.
	poolNames, err := d.cluster.ImageGetPoolNamesFromIDs(poolIDs)
	if err != nil {
		logger.Error("Error getting image pools", log.Ctx{"err": err, "fp": fingerprint})
		return err
	}

	// If no optimized pools at least update the base store
	if len(poolNames) == 0 {
		poolNames = append(poolNames, "")
	}

	logger.Debug("Processing image", log.Ctx{"fp": fingerprint, "server": source.Server, "protocol": source.Protocol, "alias": source.Alias})

	// Set operation metadata to indicate whether a refresh happened
	setRefreshResult := func(result bool) {
		if op == nil {
			return
		}

		metadata := map[string]interface{}{"refreshed": result}
		op.UpdateMetadata(metadata)
	}

	// Update the image on each pool where it currently exists.
	hash := fingerprint
	for _, poolName := range poolNames {
		newInfo, err := d.ImageDownload(op, source.Server, source.Protocol, source.Certificate, "", source.Alias, false, true, poolName, false, project)

		if err != nil {
			logger.Error("Failed to update the image", log.Ctx{"err": err, "fp": fingerprint})
			continue
		}

		hash = newInfo.Fingerprint
		if hash == fingerprint {
			logger.Debug("Already up to date", log.Ctx{"fp": fingerprint})
			continue
		}

		newId, _, err := d.cluster.ImageGet("default", hash, false, true)
		if err != nil {
			logger.Error("Error loading image", log.Ctx{"err": err, "fp": hash})
			continue
		}

		if info.Cached {
			err = d.cluster.ImageLastAccessInit(hash)
			if err != nil {
				logger.Error("Error setting cached flag", log.Ctx{"err": err, "fp": hash})
				continue
			}
		}

		err = d.cluster.ImageLastAccessUpdate(hash, info.LastUsedAt)
		if err != nil {
			logger.Error("Error setting last use date", log.Ctx{"err": err, "fp": hash})
			continue
		}

		err = d.cluster.ImageAliasesMove(id, newId)
		if err != nil {
			logger.Error("Error moving aliases", log.Ctx{"err": err, "fp": hash})
			continue
		}

		// If we do have optimized pools, make sure we remove
		// the volumes associated with the image.
		if poolName != "" {
			err = doDeleteImageFromPool(d.State(), fingerprint, poolName)
			if err != nil {
				logger.Error("Error deleting image from pool", log.Ctx{"err": err, "fp": fingerprint})
			}
		}
	}

	// Image didn't change, nothing to do.
	if hash == fingerprint {
		setRefreshResult(false)
		return nil
	}

	// Remove main image file.
	fname := filepath.Join(d.os.VarDir, "images", fingerprint)
	if shared.PathExists(fname) {
		err = os.Remove(fname)
		if err != nil {
			logger.Debugf("Error deleting image file %s: %s", fname, err)
		}
	}

	// Remove the rootfs file for the image.
	fname = filepath.Join(d.os.VarDir, "images", fingerprint) + ".rootfs"
	if shared.PathExists(fname) {
		err = os.Remove(fname)
		if err != nil {
			logger.Debugf("Error deleting image file %s: %s", fname, err)
		}
	}

	// Remove the database entry for the image.
	if err = d.cluster.ImageDelete(id); err != nil {
		logger.Debugf("Error deleting image from database %s: %s", fname, err)
	}

	setRefreshResult(true)
	return nil
}

func pruneExpiredImagesTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		opRun := func(op *operation) error {
			return pruneExpiredImages(ctx, d)
		}

		op, err := operationCreate(d.cluster, "", operationClassTask, db.OperationImagesExpire, nil, nil, opRun, nil, nil)
		if err != nil {
			logger.Error("Failed to start expired image operation", log.Ctx{"err": err})
		}

		logger.Infof("Pruning expired images")
		_, err = op.Run()
		if err != nil {
			logger.Error("Failed to expire images", log.Ctx{"err": err})
		}
		logger.Infof("Done pruning expired images")
	}

	// Skip the first run, and instead run an initial pruning synchronously
	// before we start updating images later on in the start up process.
	expiry, err := cluster.ConfigGetInt64(d.cluster, "images.remote_cache_expiry")
	if err != nil {
		logger.Error("Unable to fetch cluster configuration", log.Ctx{"err": err})
	} else if expiry > 0 {
		f(context.Background())
	}

	first := true
	schedule := func() (time.Duration, error) {
		interval := 24 * time.Hour
		if first {
			first = false
			return interval, task.ErrSkip
		}

		expiry, err := cluster.ConfigGetInt64(d.cluster, "images.remote_cache_expiry")
		if err != nil {
			logger.Error("Unable to fetch cluster configuration", log.Ctx{"err": err})
			return interval, nil
		}

		// Check if we're supposed to prune at all
		if expiry <= 0 {
			interval = 0
		}

		return interval, nil
	}

	return f, schedule
}

func pruneLeftoverImages(d *Daemon) {
	opRun := func(op *operation) error {
		// Get all images
		images, err := d.cluster.ImagesGet("default", false)
		if err != nil {
			return errors.Wrap(err, "Unable to retrieve the list of images")
		}

		// Look at what's in the images directory
		entries, err := ioutil.ReadDir(shared.VarPath("images"))
		if err != nil {
			return errors.Wrap(err, "Unable to list the images directory")
		}

		// Check and delete leftovers
		for _, entry := range entries {
			fp := strings.Split(entry.Name(), ".")[0]
			if !shared.StringInSlice(fp, images) {
				err = os.RemoveAll(shared.VarPath("images", entry.Name()))
				if err != nil {
					return errors.Wrapf(err, "Unable to remove leftover image: %v", entry.Name())
				}

				logger.Debugf("Removed leftover image file: %s", entry.Name())
			}
		}

		return nil
	}

	op, err := operationCreate(d.cluster, "", operationClassTask, db.OperationImagesPruneLeftover, nil, nil, opRun, nil, nil)
	if err != nil {
		logger.Error("Failed to start image leftover cleanup operation", log.Ctx{"err": err})
	}

	logger.Infof("Pruning leftover image files")
	_, err = op.Run()
	if err != nil {
		logger.Error("Failed to prune leftover image files", log.Ctx{"err": err})
	}
	logger.Infof("Done pruning leftover image files")
}

func pruneExpiredImages(ctx context.Context, d *Daemon) error {
	expiry, err := cluster.ConfigGetInt64(d.cluster, "images.remote_cache_expiry")
	if err != nil {
		return errors.Wrap(err, "Unable to fetch cluster configuration")
	}

	// Get the list of expired images.
	images, err := d.cluster.ImagesGetExpired(expiry)
	if err != nil {
		return errors.Wrap(err, "Unable to retrieve the list of expired images")
	}

	// Delete them
	for _, fp := range images {
		// At each iteration we check if we got cancelled in the
		// meantime. It is safe to abort here since anything not
		// expired now will be expired at the next run.
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// Get the IDs of all storage pools on which a storage volume
		// for the requested image currently exists.
		poolIDs, err := d.cluster.ImageGetPools(fp)
		if err != nil {
			continue
		}

		// Translate the IDs to poolNames.
		poolNames, err := d.cluster.ImageGetPoolNamesFromIDs(poolIDs)
		if err != nil {
			continue
		}

		for _, pool := range poolNames {
			err := doDeleteImageFromPool(d.State(), fp, pool)
			if err != nil {
				return errors.Wrapf(err, "Error deleting image %s from storage pool %s", fp, pool)
			}
		}

		// Remove main image file.
		fname := filepath.Join(d.os.VarDir, "images", fp)
		if shared.PathExists(fname) {
			err = os.Remove(fname)
			if err != nil && !os.IsNotExist(err) {
				return errors.Wrapf(err, "Error deleting image file %s", fname)
			}
		}

		// Remove the rootfs file for the image.
		fname = filepath.Join(d.os.VarDir, "images", fp) + ".rootfs"
		if shared.PathExists(fname) {
			err = os.Remove(fname)
			if err != nil && !os.IsNotExist(err) {
				return errors.Wrapf(err, "Error deleting image file %s", fname)
			}
		}

		imgID, _, err := d.cluster.ImageGet("default", fp, false, false)
		if err != nil {
			return errors.Wrapf(err, "Error retrieving image info %s", fp)
		}

		// Remove the database entry for the image.
		if err = d.cluster.ImageDelete(imgID); err != nil {
			return errors.Wrapf(err, "Error deleting image %s from database", fp)
		}
	}

	return nil
}

func doDeleteImageFromPool(state *state.State, fingerprint string, storagePool string) error {
	// Initialize a new storage interface.
	s, err := storagePoolVolumeImageInit(state, storagePool, fingerprint)
	if err != nil {
		return err
	}

	// Delete the storage volume for the image from the storage pool.
	err = s.ImageDelete(fingerprint)
	if err != nil {
		return err
	}

	return nil
}

func imageDelete(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	fingerprint := mux.Vars(r)["fingerprint"]

	deleteFromAllPools := func() error {
		// Use the fingerprint we received in a LIKE query and use the full
		// fingerprint we receive from the database in all further queries.
		imgID, imgInfo, err := d.cluster.ImageGet(project, fingerprint, false, false)
		if err != nil {
			return err
		}

		// Check if the image being deleted is actually still
		// referenced by other projects. In that case we don't want to
		// physically delete it just yet, but just to remove the
		// relevant database entry.
		referenced, err := d.cluster.ImageIsReferencedByOtherProjects(project, imgInfo.Fingerprint)
		if err != nil {
			return err
		}
		if referenced {
			err := d.cluster.ImageDelete(imgID)
			if err != nil {
				return errors.Wrap(err, "Error deleting image info from the database")
			}
			return nil
		}

		poolIDs, err := d.cluster.ImageGetPools(imgInfo.Fingerprint)
		if err != nil {
			return err
		}

		pools, err := d.cluster.ImageGetPoolNamesFromIDs(poolIDs)
		if err != nil {
			return err
		}

		for _, pool := range pools {
			err := doDeleteImageFromPool(d.State(), imgInfo.Fingerprint, pool)
			if err != nil {
				return err
			}
		}

		// Remove main image file.
		fname := shared.VarPath("images", imgInfo.Fingerprint)
		if shared.PathExists(fname) {
			err = os.Remove(fname)
			if err != nil && !os.IsNotExist(err) {
				return errors.Wrapf(err, "Error deleting image file %s", fname)
			}
		}

		imageDeleteFromDisk(imgInfo.Fingerprint)

		err = d.cluster.ImageDelete(imgID)
		if err != nil {
			return errors.Wrap(err, "Error deleting image info from the database")
		}

		// Notify the other nodes about the removed image.
		notifier, err := cluster.NewNotifier(d.State(), d.endpoints.NetworkCert(), cluster.NotifyAlive)
		if err != nil {
			// This isn't fatal.
			logger.Warnf("Error notifying other nodes about image removal: %v", err)
			return nil
		}

		err = notifier(func(client lxd.ContainerServer) error {
			op, err := client.DeleteImage(imgInfo.Fingerprint)
			if err != nil {
				return errors.Wrap(err, "Failed to request to delete image from peer node")
			}

			err = op.Wait()
			if err != nil {
				return errors.Wrap(err, "Failed to delete image from peer node")
			}

			return nil
		})
		if err != nil {
			// This isn't fatal.
			logger.Warnf("Failed to notify other nodes about removed image: %v", err)
			return nil
		}

		return nil
	}

	deleteFromDisk := func() error {
		imageDeleteFromDisk(fingerprint)
		return nil
	}

	rmimg := func(op *operation) error {
		if isClusterNotification(r) {
			return deleteFromDisk()
		}

		return deleteFromAllPools()
	}

	resources := map[string][]string{}
	resources["images"] = []string{fingerprint}

	op, err := operationCreate(d.cluster, project, operationClassTask, db.OperationImageDelete, resources, nil, rmimg, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

// Helper to delete an image file from the local images directory.
func imageDeleteFromDisk(fingerprint string) {
	// Remove main image file.
	fname := shared.VarPath("images", fingerprint)
	if shared.PathExists(fname) {
		err := os.Remove(fname)
		if err != nil && !os.IsNotExist(err) {
			logger.Errorf("Error deleting image file %s: %s", fname, err)
		}
	}

	// Remove the rootfs file for the image.
	fname = shared.VarPath("images", fingerprint) + ".rootfs"
	if shared.PathExists(fname) {
		err := os.Remove(fname)
		if err != nil && !os.IsNotExist(err) {
			logger.Errorf("Error deleting image file %s: %s", fname, err)
		}
	}
}

func doImageGet(db *db.Cluster, project, fingerprint string, public bool) (*api.Image, Response) {
	_, imgInfo, err := db.ImageGet(project, fingerprint, public, false)
	if err != nil {
		return nil, SmartError(err)
	}

	return imgInfo, nil
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
	project := projectParam(r)
	fingerprint := mux.Vars(r)["fingerprint"]
	public := d.checkTrustedClient(r) != nil
	secret := r.FormValue("secret")

	info, response := doImageGet(d.cluster, project, fingerprint, false)
	if response != nil {
		return response
	}

	if !info.Public && public && !imageValidSecret(info.Fingerprint, secret) {
		return NotFound(fmt.Errorf("Image '%s' not found", info.Fingerprint))
	}

	etag := []interface{}{info.Public, info.AutoUpdate, info.Properties}
	return SyncResponseETag(true, info, etag)
}

func imagePut(d *Daemon, r *http.Request) Response {
	// Get current value
	project := projectParam(r)
	fingerprint := mux.Vars(r)["fingerprint"]
	id, info, err := d.cluster.ImageGet(project, fingerprint, false, false)
	if err != nil {
		return SmartError(err)
	}

	// Validate ETag
	etag := []interface{}{info.Public, info.AutoUpdate, info.Properties}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.ImagePut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	err = d.cluster.ImageUpdate(id, info.Filename, info.Size, req.Public, req.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, req.Properties)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

func imagePatch(d *Daemon, r *http.Request) Response {
	// Get current value
	project := projectParam(r)
	fingerprint := mux.Vars(r)["fingerprint"]
	id, info, err := d.cluster.ImageGet(project, fingerprint, false, false)
	if err != nil {
		return SmartError(err)
	}

	// Validate ETag
	etag := []interface{}{info.Public, info.AutoUpdate, info.Properties}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return InternalError(err)
	}

	rdr1 := ioutil.NopCloser(bytes.NewBuffer(body))
	rdr2 := ioutil.NopCloser(bytes.NewBuffer(body))

	reqRaw := shared.Jmap{}
	if err := json.NewDecoder(rdr1).Decode(&reqRaw); err != nil {
		return BadRequest(err)
	}

	req := api.ImagePut{}
	if err := json.NewDecoder(rdr2).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Get AutoUpdate
	autoUpdate, err := reqRaw.GetBool("auto_update")
	if err == nil {
		info.AutoUpdate = autoUpdate
	}

	// Get Public
	public, err := reqRaw.GetBool("public")
	if err == nil {
		info.Public = public
	}

	// Get Properties
	_, ok := reqRaw["properties"]
	if ok {
		properties := req.Properties
		for k, v := range info.Properties {
			_, ok := req.Properties[k]
			if !ok {
				properties[k] = v
			}
		}
		info.Properties = properties
	}

	err = d.cluster.ImageUpdate(id, info.Filename, info.Size, info.Public, info.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

func aliasesPost(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	req := api.ImageAliasesPost{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	if req.Name == "" || req.Target == "" {
		return BadRequest(fmt.Errorf("name and target are required"))
	}

	// This is just to see if the alias name already exists.
	_, _, err := d.cluster.ImageAliasGet(project, req.Name, true)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return InternalError(err)
		}

		return Conflict(fmt.Errorf("Alias '%s' already exists", req.Name))
	}

	id, _, err := d.cluster.ImageGet(project, req.Target, false, false)
	if err != nil {
		return SmartError(err)
	}

	err = d.cluster.ImageAliasAdd(project, req.Name, id, req.Description)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/images/aliases/%s", version.APIVersion, req.Name))
}

func aliasesGet(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	recursion := util.IsRecursionRequest(r)

	names, err := d.cluster.ImageAliasesGet(project)
	if err != nil {
		return BadRequest(err)
	}
	responseStr := []string{}
	responseMap := []api.ImageAliasesEntry{}
	for _, name := range names {
		if !recursion {
			url := fmt.Sprintf("/%s/images/aliases/%s", version.APIVersion, name)
			responseStr = append(responseStr, url)

		} else {
			_, alias, err := d.cluster.ImageAliasGet(project, name, d.checkTrustedClient(r) == nil)
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
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	_, alias, err := d.cluster.ImageAliasGet(project, name, d.checkTrustedClient(r) == nil)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponseETag(true, alias, alias)
}

func aliasDelete(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	name := mux.Vars(r)["name"]
	_, _, err := d.cluster.ImageAliasGet(project, name, true)
	if err != nil {
		return SmartError(err)
	}

	err = d.cluster.ImageAliasDelete(project, name)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

func aliasPut(d *Daemon, r *http.Request) Response {
	// Get current value
	project := projectParam(r)
	name := mux.Vars(r)["name"]
	id, alias, err := d.cluster.ImageAliasGet(project, name, true)
	if err != nil {
		return SmartError(err)
	}

	// Validate ETag
	err = util.EtagCheck(r, alias)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.ImageAliasesEntryPut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	if req.Target == "" {
		return BadRequest(fmt.Errorf("The target field is required"))
	}

	imageId, _, err := d.cluster.ImageGet(project, req.Target, false, false)
	if err != nil {
		return SmartError(err)
	}

	err = d.cluster.ImageAliasUpdate(id, imageId, req.Description)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

func aliasPatch(d *Daemon, r *http.Request) Response {
	// Get current value
	project := projectParam(r)
	name := mux.Vars(r)["name"]
	id, alias, err := d.cluster.ImageAliasGet(project, name, true)
	if err != nil {
		return SmartError(err)
	}

	// Validate ETag
	err = util.EtagCheck(r, alias)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := shared.Jmap{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	_, ok := req["target"]
	if ok {
		target, err := req.GetString("target")
		if err != nil {
			return BadRequest(err)
		}

		alias.Target = target
	}

	_, ok = req["description"]
	if ok {
		description, err := req.GetString("description")
		if err != nil {
			return BadRequest(err)
		}

		alias.Description = description
	}

	imageId, _, err := d.cluster.ImageGet(project, alias.Target, false, false)
	if err != nil {
		return SmartError(err)
	}

	err = d.cluster.ImageAliasUpdate(id, imageId, alias.Description)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

func aliasPost(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	req := api.ImageAliasesEntryPost{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Check that the name isn't already in use
	id, _, _ := d.cluster.ImageAliasGet(project, req.Name, true)
	if id > 0 {
		return Conflict(fmt.Errorf("Alias '%s' already in use", req.Name))
	}

	id, _, err := d.cluster.ImageAliasGet(project, name, true)
	if err != nil {
		return SmartError(err)
	}

	err = d.cluster.ImageAliasRename(id, req.Name)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/images/aliases/%s", version.APIVersion, req.Name))
}

func imageExport(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	fingerprint := mux.Vars(r)["fingerprint"]

	public := d.checkTrustedClient(r) != nil
	secret := r.FormValue("secret")

	var imgInfo *api.Image
	var err error
	if r.RemoteAddr == "@devlxd" {
		// /dev/lxd API requires exact match
		_, imgInfo, err = d.cluster.ImageGet(project, fingerprint, false, true)
		if err != nil {
			return SmartError(err)
		}

		if !imgInfo.Public && !imgInfo.Cached {
			return NotFound(fmt.Errorf("Image '%s' not found", fingerprint))
		}
	} else {
		_, imgInfo, err = d.cluster.ImageGet(project, fingerprint, false, false)
		if err != nil {
			return SmartError(err)
		}

		if !imgInfo.Public && public && !imageValidSecret(imgInfo.Fingerprint, secret) {
			return NotFound(fmt.Errorf("Image '%s' not found", imgInfo.Fingerprint))
		}
	}

	// Check if the image is only available on another node.
	address, err := d.cluster.ImageLocate(imgInfo.Fingerprint)
	if err != nil {
		return SmartError(err)
	}
	if address != "" {
		// Forward the request to the other node
		cert := d.endpoints.NetworkCert()
		client, err := cluster.Connect(address, cert, false)
		if err != nil {
			return SmartError(err)
		}
		return ForwardedResponse(client, r)
	}

	imagePath := shared.VarPath("images", imgInfo.Fingerprint)
	rootfsPath := imagePath + ".rootfs"

	_, ext, _, err := shared.DetectCompression(imagePath)
	if err != nil {
		ext = ""
	}
	filename := fmt.Sprintf("%s%s", imgInfo.Fingerprint, ext)

	if shared.PathExists(rootfsPath) {
		files := make([]fileResponseEntry, 2)

		files[0].identifier = "metadata"
		files[0].path = imagePath
		files[0].filename = "meta-" + filename

		// Recompute the extension for the root filesystem, it may use a different
		// compression algorithm than the metadata.
		_, ext, _, err = shared.DetectCompression(rootfsPath)
		if err != nil {
			ext = ""
		}
		filename = fmt.Sprintf("%s%s", imgInfo.Fingerprint, ext)

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
	project := projectParam(r)
	fingerprint := mux.Vars(r)["fingerprint"]
	_, imgInfo, err := d.cluster.ImageGet(project, fingerprint, false, false)
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
	resources["images"] = []string{imgInfo.Fingerprint}

	op, err := operationCreate(d.cluster, project, operationClassToken, db.OperationImageToken, resources, meta, nil, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func imageImportFromNode(imagesDir string, client lxd.ContainerServer, fingerprint string) error {
	// Prepare the temp files
	buildDir, err := ioutil.TempDir(imagesDir, "lxd_build_")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary directory for download")
	}
	defer os.RemoveAll(buildDir)

	metaFile, err := ioutil.TempFile(buildDir, "lxd_tar_")
	if err != nil {
		return err
	}
	defer metaFile.Close()

	rootfsFile, err := ioutil.TempFile(buildDir, "lxd_tar_")
	if err != nil {
		return err
	}
	defer rootfsFile.Close()

	getReq := lxd.ImageFileRequest{
		MetaFile:   io.WriteSeeker(metaFile),
		RootfsFile: io.WriteSeeker(rootfsFile),
	}
	getResp, err := client.GetImageFile(fingerprint, getReq)
	if err != nil {
		return err
	}
	metaFile.Close()
	rootfsFile.Close()

	if getResp.RootfsSize == 0 {
		// This is a unified image.
		rootfsPath := filepath.Join(imagesDir, fingerprint)
		err := shared.FileMove(metaFile.Name(), rootfsPath)
		if err != nil {
			return err
		}
	} else {
		// This is a split image.
		metaPath := filepath.Join(imagesDir, fingerprint)
		rootfsPath := filepath.Join(imagesDir, fingerprint+".rootfs")

		err := shared.FileMove(metaFile.Name(), metaPath)
		if err != nil {
			return nil
		}
		err = shared.FileMove(rootfsFile.Name(), rootfsPath)
		if err != nil {
			return nil
		}
	}

	return nil
}

func imageRefresh(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	fingerprint := mux.Vars(r)["fingerprint"]
	imageId, imageInfo, err := d.cluster.ImageGet(project, fingerprint, false, false)
	if err != nil {
		return SmartError(err)
	}

	// Begin background operation
	run := func(op *operation) error {
		return autoUpdateImage(d, op, imageId, imageInfo, project)
	}

	op, err := operationCreate(d.cluster, project, operationClassTask, db.OperationImageRefresh, nil, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func imageSyncBetweenNodes(d *Daemon, req api.ImagesPost, fingerprint string) error {
	var desiredSyncNodeCount int64

	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		config, err := cluster.ConfigLoad(tx)
		if err != nil {
			return errors.Wrap(err, "Failed to load cluster configuration")
		}
		desiredSyncNodeCount = config.ImagesMinimalReplica()

		// -1 means that we want to replicate the image on all nodes
		if desiredSyncNodeCount == -1 {
			nodesCount, err := tx.NodesCount()
			if err != nil {
				return errors.Wrap(err, "Failed to get the number of nodes")
			}

			desiredSyncNodeCount = int64(nodesCount)
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Check how many nodes already have this image
	syncNodeAddresses, err := d.cluster.ImageGetNodesWithImage(fingerprint)
	if err != nil {
		return errors.Wrap(err, "Failed to get nodes for the image synchronization")
	}

	nodeCount := desiredSyncNodeCount - int64(len(syncNodeAddresses))
	if nodeCount <= 0 {
		return nil
	}

	addresses, err := d.cluster.ImageGetNodesHasNoImage(fingerprint)
	if err != nil {
		return errors.Wrap(err, "Failed to get nodes for the image synchronization")
	}

	min := func(x, y int64) int64 {
		if x > y {
			return y
		}

		return x
	}

	for idx := int64(0); idx < min(int64(len(addresses)), nodeCount); idx++ {
		// We spread the image for the nodes inside of cluster and we need to double
		// check if the image already exists via DB since when one certain node is
		// going to create an image it will invoke the same routine.
		syncNodeAddresses, err = d.cluster.ImageGetNodesWithImage(fingerprint)
		if err != nil {
			return errors.Wrap(err, "Failed to get nodes for the image synchronization")
		}

		if shared.StringInSlice(addresses[idx], syncNodeAddresses) {
			continue
		}

		client, err := cluster.Connect(addresses[idx], d.endpoints.NetworkCert(), true)
		if err != nil {
			return errors.Wrap(err, "Failed to connect node for image synchronization")
		}

		createArgs := &lxd.ImageCreateArgs{}
		imageMetaPath := shared.VarPath("images", fingerprint)
		imageRootfsPath := shared.VarPath("images", fingerprint+".rootfs")

		metaFile, err := os.Open(imageMetaPath)
		if err != nil {
			return err
		}
		defer metaFile.Close()

		createArgs.MetaFile = metaFile
		createArgs.MetaName = filepath.Base(imageMetaPath)

		if shared.PathExists(imageRootfsPath) {
			rootfsFile, err := os.Open(imageRootfsPath)
			if err != nil {
				return err
			}
			defer rootfsFile.Close()

			createArgs.RootfsFile = rootfsFile
			createArgs.RootfsName = filepath.Base(imageRootfsPath)
		}

		image := api.ImagesPost{}
		image.Filename = createArgs.MetaName

		op, err := client.CreateImage(image, createArgs)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	return nil
}
