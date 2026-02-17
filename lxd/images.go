package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"math/rand"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/kballard/go-shellquote"
	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/config"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/lxd/operations"
	projectutils "github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/project/limits"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/lxd/task"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/filter"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/osarch"
	"github.com/canonical/lxd/shared/validate"
	"github.com/canonical/lxd/shared/version"
)

var imagesCmd = APIEndpoint{
	Path:        "images",
	MetricsType: entity.TypeImage,

	Get:  APIEndpointAction{Handler: imagesGet, AllowUntrusted: true, AccessHandler: imagesGetAccessHandler},
	Post: APIEndpointAction{Handler: imagesPost, AllowUntrusted: true, ContentTypes: []string{"application/json", "application/octet-stream", "multipart/form-data"}},
}

var imageCmd = APIEndpoint{
	Path:        "images/{fingerprint}",
	MetricsType: entity.TypeImage,

	Delete: APIEndpointAction{Handler: imageDelete, AccessHandler: imageAccessHandler(auth.EntitlementCanDelete)},
	Get:    APIEndpointAction{Handler: imageGet, AllowUntrusted: true},
	Patch:  APIEndpointAction{Handler: imagePatch, AccessHandler: imageAccessHandler(auth.EntitlementCanEdit)},
	Put:    APIEndpointAction{Handler: imagePut, AccessHandler: imageAccessHandler(auth.EntitlementCanEdit)},
}

var imageExportCmd = APIEndpoint{
	Path:        "images/{fingerprint}/export",
	MetricsType: entity.TypeImage,

	Get:  APIEndpointAction{Handler: imageExport, AllowUntrusted: true},
	Post: APIEndpointAction{Handler: imageExportPost, AccessHandler: imageAccessHandler(auth.EntitlementCanEdit)},
}

var imageSecretCmd = APIEndpoint{
	Path:        "images/{fingerprint}/secret",
	MetricsType: entity.TypeImage,

	Post: APIEndpointAction{Handler: imageSecret, AccessHandler: imageAccessHandler(auth.EntitlementCanEdit)},
}

var imageRefreshCmd = APIEndpoint{
	Path:        "images/{fingerprint}/refresh",
	MetricsType: entity.TypeImage,

	Post: APIEndpointAction{Handler: imageRefresh, AccessHandler: imageAccessHandler(auth.EntitlementCanEdit)},
}

var imageAliasesCmd = APIEndpoint{
	Path:        "images/aliases",
	MetricsType: entity.TypeImage,

	Get:  APIEndpointAction{Handler: imageAliasesGet, AccessHandler: allowProjectResourceList(false)},
	Post: APIEndpointAction{Handler: imageAliasesPost, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanCreateImageAliases)},
}

var imageAliasCmd = APIEndpoint{
	Path:        "images/aliases/{name:.*}",
	MetricsType: entity.TypeImage,

	Delete: APIEndpointAction{Handler: imageAliasDelete, AccessHandler: imageAliasAccessHandler(auth.EntitlementCanDelete)},
	Get:    APIEndpointAction{Handler: imageAliasGet, AllowUntrusted: true},
	Patch:  APIEndpointAction{Handler: imageAliasPatch, AccessHandler: imageAliasAccessHandler(auth.EntitlementCanEdit)},
	Post:   APIEndpointAction{Handler: imageAliasPost, AccessHandler: imageAliasAccessHandler(auth.EntitlementCanEdit)},
	Put:    APIEndpointAction{Handler: imageAliasPut, AccessHandler: imageAliasAccessHandler(auth.EntitlementCanEdit)},
}

// validateImageFingerprintPrefix validates that the given string is at least 12 characters long contains only lowercase
// hex characters.
func validateImageFingerprintPrefix(prefix string) error {
	// 12 characters is chosen because this is the length of the fingerprint as displayed in the CLI when listing images.
	if len(prefix) < 12 {
		return api.NewStatusError(http.StatusBadRequest, "Image fingerprint prefix must contain 12 characters or more")
	}

	if len(prefix) > 64 {
		return api.NewStatusError(http.StatusBadRequest, "Image fingerprint cannot be longer than 64 characters")
	}

	// Prefixes containing non-hex or uppercase characters can never match an image.
	for _, b := range []byte(prefix) {
		if (b < '0' || b > '9') && (b < 'a' || b > 'f') {
			return api.NewStatusError(http.StatusBadRequest, "Image fingerprint prefix must contain only lowercase hexadecimal characters")
		}
	}

	return nil
}

const ctxImageDetails request.CtxKey = "image-details"

// imageDetails contains fields that are determined prior to the access check. This is set in the request context when
// addImageDetailsToRequestContext is called.
type imageDetails struct {
	imageFingerprintPrefix string
	imageID                int
	image                  api.Image
}

// addImageDetailsToRequestContext sets the effective project in the request.Info and sets ctxImageDetails (imageDetails)
// in the request context.
func addImageDetailsToRequestContext(s *state.State, r *http.Request) error {
	imageFingerprintPrefix, err := url.PathUnescape(mux.Vars(r)["fingerprint"])
	if err != nil {
		return err
	}

	err = validateImageFingerprintPrefix(imageFingerprintPrefix)
	if err != nil {
		return err
	}

	requestProjectName := request.ProjectParam(r)
	effectiveProjectName := requestProjectName
	var imageID int
	var image *api.Image
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		effectiveProjectName, err = projectutils.ImageProject(ctx, tx.Tx(), requestProjectName)
		if err != nil {
			return err
		}

		imageID, image, err = tx.GetImageByFingerprintPrefix(ctx, imageFingerprintPrefix, dbCluster.ImageFilter{Project: &requestProjectName})
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to check project %q image feature: %w", requestProjectName, err)
	}

	request.SetContextValue(r, request.CtxEffectiveProjectName, effectiveProjectName)
	request.SetContextValue(r, ctxImageDetails, imageDetails{
		imageFingerprintPrefix: imageFingerprintPrefix,
		imageID:                imageID,
		image:                  *image,
	})

	return nil
}

func imagesGetAccessHandler(d *Daemon, r *http.Request) response.Response {
	projectName, allProjects, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	// Regardless of trust status, if the request is for the default project then we allow it. This is to return public images.
	if !allProjects && projectName == api.ProjectDefaultName {
		return response.EmptySyncResponse
	}

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	// An untrusted caller has attempted to list images in a non-default project, or to use the all-projects parameter.
	// Reject immediately.
	if !requestor.IsTrusted() {
		if allProjects {
			return response.Forbidden(errors.New("Untrusted callers may only access public images in the default project"))
		}

		return response.NotFound(nil)
	}

	// The caller is trusted and is listing resources in a non-default project (or using all-projects).
	// Use the same access handler as is used for listing any project specific entity type.
	return allowProjectResourceList(false)(d, r)
}

func imageAccessHandler(entitlement auth.Entitlement) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		s := d.State()
		err := addImageDetailsToRequestContext(s, r)
		if err != nil {
			return response.SmartError(err)
		}

		details, err := request.GetContextValue[imageDetails](r.Context(), ctxImageDetails)
		if err != nil {
			return response.SmartError(err)
		}

		err = s.Authorizer.CheckPermission(r.Context(), entity.ImageURL(request.ProjectParam(r), details.image.Fingerprint), entitlement)
		if err != nil {
			return response.SmartError(err)
		}

		return response.EmptySyncResponse
	}
}

func imageAliasAccessHandler(entitlement auth.Entitlement) func(d *Daemon, r *http.Request) response.Response {
	return func(d *Daemon, r *http.Request) response.Response {
		imageAliasName, err := url.PathUnescape(mux.Vars(r)["name"])
		if err != nil {
			return response.SmartError(err)
		}

		requestProjectName := request.ProjectParam(r)
		var effectiveProjectName string
		s := d.State()
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			effectiveProjectName, err = projectutils.ImageProject(ctx, tx.Tx(), requestProjectName)
			return err
		})
		if err != nil && api.StatusErrorCheck(err, http.StatusNotFound) {
			return response.NotFound(nil)
		} else if err != nil {
			return response.SmartError(err)
		}

		request.SetContextValue(r, request.CtxEffectiveProjectName, effectiveProjectName)
		err = s.Authorizer.CheckPermission(r.Context(), entity.ImageAliasURL(requestProjectName, imageAliasName), entitlement)
		if err != nil {
			return response.SmartError(err)
		}

		return response.EmptySyncResponse
	}
}

/*
We only want a single publish running at any one time.

	The CPU and I/O load of publish is such that running multiple ones in
	parallel takes longer than running them serially.

	Additionally, publishing the same container or container snapshot
	twice would lead to storage problem, not to mention a conflict at the
	end for whichever finishes last.
*/
var imagePublishLock sync.Mutex

// imageTaskMu prevents image related tasks from being scheduled at the same time as each other to prevent them
// stepping on each other's toes.
var imageTaskMu sync.Mutex

func compressFile(compress string, infile io.Reader, outfile io.Writer) error {
	var cmd *exec.Cmd

	// Parse the command.
	fields, err := shellquote.Split(compress)
	if err != nil {
		return err
	}

	err = validate.IsCompressionAlgorithm(fields[0])
	if err != nil {
		return err
	}

	if fields[0] == "squashfs" {
		// 'tar2sqfs' do not support writing to stdout. So write to a temporary
		//  file first and then replay the compressed content to outfile.
		tempfile, err := os.CreateTemp("", "lxd_compress_")
		if err != nil {
			return err
		}

		defer func() { _ = tempfile.Close() }()
		defer func() { _ = os.Remove(tempfile.Name()) }()

		// Prepare 'tar2sqfs' arguments
		args := make([]string, 0, len(fields))
		if len(fields) > 1 {
			args = append(args, fields[1:]...)
		}

		args = append(args, "--quiet", "--no-skip", "--force", "--compressor", "xz", tempfile.Name())
		cmd = exec.Command("tar2sqfs", args...)
		cmd.Stdin = infile

		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("tar2sqfs: %w (%v)", err, strings.TrimSpace(string(output)))
		}
		// Replay the result to outfile
		_, err = tempfile.Seek(0, io.SeekStart)
		if err != nil {
			return err
		}

		_, err = io.Copy(outfile, tempfile)
		if err != nil {
			return err
		}
	} else {
		args := []string{"-c"}
		if len(fields) > 1 {
			args = append(args, fields[1:]...)
		}

		if fields[0] == "gzip" {
			args = append(args, "-n")
		}

		cmd := exec.Command(fields[0], args...)
		cmd.Stdin = infile
		cmd.Stdout = outfile
		err := cmd.Run()
		if err != nil {
			return err
		}
	}

	return nil
}

/*
 * This function takes a container or snapshot from the local image server and
 * exports it as an image.
 */
func imgPostInstanceInfo(s *state.State, req api.ImagesPost, op *operations.Operation, instanceProject string, imageProject string, builddir string, budget int64) (*api.Image, error) {
	info := api.Image{}
	info.Properties = map[string]string{}

	name := req.Source.Name
	ctype := req.Source.Type
	if ctype == "" || name == "" {
		return nil, errors.New("No source provided")
	}

	switch ctype {
	case "snapshot":
		if !shared.IsSnapshot(name) {
			return nil, errors.New("Not a snapshot")
		}

	case "container", "virtual-machine", "instance":
		if shared.IsSnapshot(name) {
			return nil, errors.New("This is a snapshot")
		}

	default:
		return nil, errors.New("Bad type")
	}

	info.Filename = req.Filename
	switch req.Public {
	case true:
		info.Public = true
	case false:
		info.Public = false
	}

	c, err := instance.LoadByProjectAndName(s, instanceProject, name)
	if err != nil {
		return nil, err
	}

	info.Type = c.Type().String()

	// Build the actual image file
	imageFile, err := os.CreateTemp(builddir, "lxd_build_image_")
	if err != nil {
		return nil, err
	}

	defer func() { _ = os.Remove(imageFile.Name()) }()

	// Calculate (close estimate of) total size of input to image
	totalSize := int64(0)
	sumSize := func(path string, fi os.FileInfo, err error) error {
		if err == nil {
			totalSize += fi.Size()
		}

		return nil
	}

	err = filepath.Walk(c.RootfsPath(), sumSize)
	if err != nil {
		return nil, err
	}

	// Track progress creating image.
	imageProgressWriter := &ioprogress.ProgressWriter{
		Tracker: &ioprogress.ProgressTracker{
			Handler: func(value, speed int64) {
				percent := int64(0)
				var processed int64

				if totalSize > 0 {
					percent = value
					processed = totalSize * (percent / 100.0)
				} else {
					processed = value
				}

				_ = op.UpdateProgress("create_image_from_instance_pack", "Image pack", percent, processed, speed)
			},
			Length: totalSize,
		},
	}

	sha256 := sha256.New()
	var compress string
	var writer io.Writer

	if req.CompressionAlgorithm != "" {
		compress = req.CompressionAlgorithm
	} else {
		var p *api.Project
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			project, err := dbCluster.GetProject(ctx, tx.Tx(), instanceProject)
			if err != nil {
				return err
			}

			p, err = project.ToAPI(ctx, tx.Tx())

			return err
		})
		if err != nil {
			return nil, err
		}

		if p.Config["images.compression_algorithm"] != "" {
			compress = p.Config["images.compression_algorithm"]
		} else {
			compress = s.GlobalConfig.ImagesCompressionAlgorithm()
		}
	}

	// Setup tar, optional compress and sha256 to happen in one pass.
	wg := sync.WaitGroup{}
	var compressErr error
	if compress != "none" {
		wg.Add(1)
		tarReader, tarWriter := io.Pipe()
		imageProgressWriter.WriteCloser = tarWriter
		writer = imageProgressWriter
		compressWriter := io.MultiWriter(imageFile, sha256)
		go func() {
			defer wg.Done()
			compressErr = compressFile(compress, tarReader, compressWriter)

			// If a compression error occurred, close the writer to end the instance export.
			if compressErr != nil {
				_ = imageProgressWriter.Close()
			}
		}()
	} else {
		imageProgressWriter.WriteCloser = imageFile
		writer = io.MultiWriter(imageProgressWriter, sha256)
	}

	// Tracker instance for the export phase.
	tracker := &ioprogress.ProgressTracker{
		Handler: func(value, speed int64) {
			_ = op.UpdateProgress("create_image_from_instance_pack", "Exporting", value, 0, 0)
		},
	}

	// Export instance to writer.
	var meta api.ImageMetadata

	writer = shared.NewQuotaWriter(writer, budget)
	meta, err = c.Export(writer, req.Properties, req.ExpiresAt, tracker)

	// Get ExpiresAt
	if meta.ExpiryDate != 0 {
		info.ExpiresAt = time.Unix(meta.ExpiryDate, 0)
	}

	// Clean up file handles.
	// When compression is used, Close on imageProgressWriter/tarWriter is required for compressFile/gzip to
	// know it is finished. Otherwise it is equivalent to imageFile.Close.
	_ = imageProgressWriter.Close()
	wg.Wait() // Wait until compression helper has finished if used.
	_ = imageFile.Close()

	// Check compression errors.
	if compressErr != nil {
		return nil, compressErr
	}

	// Check instance export errors.
	if err != nil {
		return nil, err
	}

	fi, err := os.Stat(imageFile.Name())
	if err != nil {
		return nil, err
	}

	info.Size = fi.Size()
	info.Fingerprint = hex.EncodeToString(sha256.Sum(nil))
	info.CreatedAt = time.Now().UTC()

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, _, err = tx.GetImage(ctx, info.Fingerprint, dbCluster.ImageFilter{Project: &imageProject})

		return err
	})
	if !response.IsNotFoundError(err) {
		if err != nil {
			return nil, err
		}

		return &info, fmt.Errorf("The image already exists: %s", info.Fingerprint)
	}

	/* rename the file to the expected name so our caller can use it */
	finalName := filepath.Join(s.ImagesStoragePath(imageProject), info.Fingerprint)
	err = shared.FileMove(imageFile.Name(), finalName)
	if err != nil {
		return nil, err
	}

	info.Architecture, _ = osarch.ArchitectureName(c.Architecture())
	info.Properties = meta.Properties

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Create the database entry
		return tx.CreateImage(ctx, imageProject, info.Fingerprint, info.Filename, info.Size, info.Public, info.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties, info.Type, nil)
	})
	if err != nil {
		return nil, err
	}

	return &info, nil
}

func imgPostRemoteInfo(ctx context.Context, s *state.State, req api.ImagesPost, op *operations.Operation, profileProject string, imageProject string, budget int64) (*api.Image, error) {
	var err error
	var hash string

	if req.Source.Fingerprint != "" {
		hash = req.Source.Fingerprint
	} else if req.Source.Alias != "" {
		hash = req.Source.Alias
	} else {
		return nil, errors.New("must specify one of alias or fingerprint for init from image")
	}

	info, err := ImageDownload(ctx, s, op, &ImageDownloadArgs{
		Server:            req.Source.Server,
		Protocol:          req.Source.Protocol,
		Certificate:       req.Source.Certificate,
		Secret:            req.Source.Secret,
		Alias:             hash,
		Type:              req.Source.ImageType,
		AutoUpdate:        req.AutoUpdate,
		Public:            req.Public,
		ProjectName:       imageProject,
		Budget:            budget,
		SourceProjectName: req.Source.Project,
		UserRequested:     true,
	})
	if err != nil {
		return nil, err
	}

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var id int

		id, info, err = tx.GetImage(ctx, info.Fingerprint, dbCluster.ImageFilter{Project: &imageProject})
		if err != nil {
			return err
		}

		// Allow overriding or adding properties
		maps.Copy(info.Properties, req.Properties)

		// Get profile IDs
		if req.Profiles == nil {
			req.Profiles = []string{api.ProjectDefaultName}
		}

		profileIDs := make([]int64, len(req.Profiles))

		for i, profile := range req.Profiles {
			profileID, _, err := tx.GetProfile(ctx, profileProject, profile)
			if response.IsNotFoundError(err) {
				return fmt.Errorf("Profile %q doesn't exist", profile)
			} else if err != nil {
				return err
			}

			profileIDs[i] = profileID
		}

		// Handle the case when an image is being copied into the default project.
		if imageProject == api.ProjectDefaultName {
			// Find IDs of profiles that have matching names in projects with "features.images=false".
			otherProfileIDs, err := resolveProfileIDs(ctx, tx, profileProject, req.Profiles)
			if err != nil {
				return err
			}

			profileIDs = append(profileIDs, otherProfileIDs...)
		}

		// Update the DB record if needed
		if req.Public || req.AutoUpdate || req.Filename != "" || len(req.Properties) > 0 || len(req.Profiles) > 0 {
			err := tx.UpdateImage(ctx, id, req.Filename, info.Size, req.Public, req.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties, profileIDs)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return info, nil
}

func imgPostURLInfo(ctx context.Context, s *state.State, req api.ImagesPost, op *operations.Operation, imageProject string, budget int64) (*api.Image, error) {
	var err error

	if req.Source.URL == "" {
		return nil, errors.New("Missing URL")
	}

	myhttp, err := util.HTTPClient("", s.Proxy)
	if err != nil {
		return nil, err
	}

	// Resolve the image URL
	head, err := http.NewRequest("HEAD", req.Source.URL, nil)
	if err != nil {
		return nil, err
	}

	architectures := []string{}
	for _, architecture := range s.OS.Architectures {
		architectureName, err := osarch.ArchitectureName(architecture)
		if err != nil {
			return nil, err
		}

		architectures = append(architectures, architectureName)
	}

	head.Header.Set("User-Agent", version.UserAgent)
	head.Header.Set("LXD-Server-Architectures", strings.Join(architectures, ", "))
	head.Header.Set("LXD-Server-Version", version.Version)

	raw, err := myhttp.Do(head)
	if err != nil {
		return nil, err
	}

	hash := raw.Header.Get("LXD-Image-Hash")
	if hash == "" {
		return nil, errors.New("Missing LXD-Image-Hash header")
	}

	url := raw.Header.Get("LXD-Image-URL")
	if url == "" {
		return nil, errors.New("Missing LXD-Image-URL header")
	}

	// Import the image
	info, err := ImageDownload(ctx, s, op, &ImageDownloadArgs{
		Server:        url,
		Protocol:      "direct",
		Alias:         hash,
		AutoUpdate:    req.AutoUpdate,
		Public:        req.Public,
		ProjectName:   imageProject,
		Budget:        budget,
		UserRequested: true,
	})
	if err != nil {
		return nil, err
	}

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var id int

		id, info, err = tx.GetImage(ctx, info.Fingerprint, dbCluster.ImageFilter{Project: &imageProject})
		if err != nil {
			return err
		}

		// Allow overriding or adding properties
		maps.Copy(info.Properties, req.Properties)

		if req.Public || req.AutoUpdate || req.Filename != "" || len(req.Properties) > 0 {
			err := tx.UpdateImage(ctx, id, req.Filename, info.Size, req.Public, req.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties, nil)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return info, nil
}

func getImgPostInfo(s *state.State, r *http.Request, builddir string, project string, post *os.File, metadata map[string]any) (*api.Image, error) {
	info := api.Image{}
	var imageMeta *api.ImageMetadata
	l := logger.AddContext(logger.Ctx{"function": "getImgPostInfo"})

	info.Public = shared.IsTrue(r.Header.Get("X-LXD-public"))
	propHeaders := r.Header[http.CanonicalHeaderKey("X-LXD-properties")]
	profilesHeaders := r.Header.Get("X-LXD-profiles")
	ctype, ctypeParams, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		ctype = "application/octet-stream"
	}

	sha256 := sha256.New()
	var size int64
	var imageTmpFilename string
	var rootfsTmpFilename string

	if ctype == "multipart/form-data" {
		// Create a temporary file for the image tarball
		imageTarf, err := os.CreateTemp(builddir, "lxd_tar_")
		if err != nil {
			return nil, err
		}

		defer func() { _ = os.Remove(imageTarf.Name()) }()

		imageTmpFilename = imageTarf.Name()

		// Parse the POST data
		_, err = post.Seek(0, io.SeekStart)
		if err != nil {
			return nil, err
		}

		mr := multipart.NewReader(post, ctypeParams["boundary"])

		// Get the metadata tarball
		part, err := mr.NextPart()
		if err != nil {
			return nil, err
		}

		if part.FormName() != "metadata" {
			return nil, errors.New("Invalid multipart image")
		}

		size, err = io.Copy(io.MultiWriter(imageTarf, sha256), part)
		info.Size += size

		_ = imageTarf.Close()
		if err != nil {
			l.Error("Failed to copy the image tarfile", logger.Ctx{"err": err})
			return nil, err
		}

		// Get the rootfs tarball
		part, err = mr.NextPart()
		if err != nil {
			l.Error("Failed to get the next part", logger.Ctx{"err": err})
			return nil, err
		}

		if part.FormName() == "rootfs" {
			info.Type = instancetype.Container.String()
		} else if part.FormName() == "rootfs.img" {
			info.Type = instancetype.VM.String()
		} else {
			l.Error("Invalid multipart image")
			return nil, errors.New("Invalid multipart image")
		}

		// Create a temporary file for the rootfs tarball
		rootfsTarf, err := os.CreateTemp(builddir, "lxd_tar_")
		if err != nil {
			return nil, err
		}

		defer func() { _ = os.Remove(rootfsTarf.Name()) }()

		rootfsTmpFilename = rootfsTarf.Name()

		size, err = io.Copy(io.MultiWriter(rootfsTarf, sha256), part)
		info.Size += size

		_ = rootfsTarf.Close()
		if err != nil {
			l.Error("Failed to copy the rootfs tarfile", logger.Ctx{"err": err})
			return nil, err
		}

		info.Filename = part.FileName()
	} else {
		_, err = post.Seek(0, io.SeekStart)
		if err != nil {
			return nil, err
		}

		size, err = io.Copy(sha256, post)
		if err != nil {
			l.Error("Failed to copy the tarfile", logger.Ctx{"err": err})
			return nil, err
		}

		info.Size = size

		info.Filename = r.Header.Get("X-LXD-filename")

		imageTmpFilename = post.Name()
	}

	info.Fingerprint = hex.EncodeToString(sha256.Sum(nil))

	expectedFingerprint := r.Header.Get("X-LXD-fingerprint")
	if expectedFingerprint != "" && info.Fingerprint != expectedFingerprint {
		l.Error("Fingerprints don't match", logger.Ctx{
			"got":      info.Fingerprint,
			"expected": expectedFingerprint})
		err = fmt.Errorf("Fingerprints don't match, got %s expected %s", info.Fingerprint, expectedFingerprint)
		return nil, err
	}

	unlock, err := imageOperationLock(info.Fingerprint)
	if err != nil {
		return nil, err
	}

	defer unlock()

	imageMeta, imageType, err := getImageMetadata(imageTmpFilename)
	if err != nil {
		l.Error("Failed to get image metadata", logger.Ctx{"err": err})
		return nil, err
	}

	if info.Type == "" {
		info.Type = imageType
	}

	imgfname := filepath.Join(s.ImagesStoragePath(project), info.Fingerprint)
	err = shared.FileMove(imageTmpFilename, imgfname)
	if err != nil {
		l.Error("Failed to move the image tarfile", logger.Ctx{
			"err":    err,
			"source": imageTmpFilename,
			"dest":   imgfname})
		return nil, err
	}

	if rootfsTmpFilename != "" {
		rootfsfname := imgfname + ".rootfs"
		err = shared.FileMove(rootfsTmpFilename, rootfsfname)
		if err != nil {
			l.Error("Failed to move the rootfs tarfile", logger.Ctx{
				"err":    err,
				"source": rootfsTmpFilename,
				"dest":   imgfname})
			return nil, err
		}
	}

	info.Architecture = imageMeta.Architecture
	info.CreatedAt = time.Unix(imageMeta.CreationDate, 0)

	expiresAt, ok := metadata["expires_at"]
	if ok {
		info.ExpiresAt, ok = expiresAt.(time.Time)
		if !ok {
			return nil, errors.New("Invalid type for field \"expires_at\"")
		}
	} else {
		info.ExpiresAt = time.Unix(imageMeta.ExpiryDate, 0)
	}

	properties, ok := metadata["properties"]
	if ok {
		info.Properties, ok = properties.(map[string]string)
		if !ok {
			return nil, errors.New("Invalid type for field \"properties\"")
		}
	} else {
		info.Properties = imageMeta.Properties
	}

	if len(propHeaders) > 0 {
		for _, ph := range propHeaders {
			p, _ := url.ParseQuery(ph)
			for pkey, pval := range p {
				info.Properties[pkey] = pval[0]
			}
		}
	}

	var profileIDs []int64
	if len(profilesHeaders) > 0 {
		p, _ := url.ParseQuery(profilesHeaders)
		profileIDs = make([]int64, len(p["profile"]))

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			for i, val := range p["profile"] {
				profileID, _, err := tx.GetProfile(ctx, project, val)
				if response.IsNotFoundError(err) {
					return fmt.Errorf("Profile %q doesn't exist", val)
				} else if err != nil {
					return err
				}

				profileIDs[i] = profileID
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	var exists bool

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check if the image already exists
		exists, err = tx.ImageExists(ctx, project, info.Fingerprint)

		return err
	})
	if err != nil {
		return nil, err
	}

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return nil, err
	}

	if exists {
		// Do not create a database entry if the request is coming from the internal
		// cluster communications for image synchronization
		if !requestor.IsClusterNotification() {
			return &info, errors.New("Image with same fingerprint already exists")
		}

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.AddImageToLocalNode(ctx, project, info.Fingerprint)
		})
		if err != nil {
			return nil, err
		}
	} else {
		public, ok := metadata["public"]
		if ok {
			info.Public, ok = public.(bool)
			if !ok {
				return nil, errors.New("Invalid type for key \"public\"")
			}
		}

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Create the database entry
			return tx.CreateImage(ctx, project, info.Fingerprint, info.Filename, info.Size, info.Public, info.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties, info.Type, profileIDs)
		})
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
func imageCreateInPool(s *state.State, info *api.Image, storagePool string, projectName string) error {
	if storagePool == "" {
		return errors.New("No storage pool specified")
	}

	pool, err := storagePools.LoadByName(s, storagePool)
	if err != nil {
		return err
	}

	err = pool.EnsureImage(info.Fingerprint, nil, projectName)
	if err != nil {
		return err
	}

	return nil
}

// swagger:operation POST /1.0/images?public images images_post_untrusted
//
//  Add an image
//
//  Pushes the data to the target image server.
//  This is meant for LXD to LXD communication where a new image entry is
//  prepared on the target server and the source server is provided that URL
//  and a secret token to push the image content over.
//
//  ---
//  consumes:
//    - application/json
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: body
//      name: image
//      description: Image
//      required: true
//      schema:
//        $ref: "#/definitions/ImagesPost"
//  responses:
//    "200":
//      $ref: "#/responses/EmptySyncResponse"
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation POST /1.0/images images images_post
//
//	Add an image
//
//	Adds a new image to the image store.
//
//	---
//	consumes:
//	  - application/json
//	  - application/octet-stream
//	  - multipart/form-data
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: image
//	    description: Image
//	    required: false
//	    schema:
//	      $ref: "#/definitions/ImagesPost"
//	  - in: body
//	    name: raw_image
//	    description: Raw image file
//	    required: false
//	  - in: header
//	    name: X-LXD-secret
//	    description: Push secret for server to server communication
//	    schema:
//	      type: string
//	    example: RANDOM-STRING
//	  - in: header
//	    name: X-LXD-fingerprint
//	    description: Expected fingerprint when pushing a raw image
//	    schema:
//	      type: string
//	  - in: header
//	    name: X-LXD-properties
//	    description: Descriptive properties
//	    schema:
//	      type: object
//	      additionalProperties:
//	        type: string
//	  - in: header
//	    name: X-LXD-public
//	    description: Whether the image is available to unauthenticated users
//	    schema:
//	      type: boolean
//	  - in: header
//	    name: X-LXD-filename
//	    description: Original filename of the image
//	    schema:
//	      type: string
//	  - in: header
//	    name: X-LXD-profiles
//	    description: List of profiles to use
//	    schema:
//	      type: array
//	      items:
//	        type: string
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imagesPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	// If the client is not authenticated, CheckPermission will return a http.StatusForbidden api.StatusError.
	var userCanCreateImages bool
	err := s.Authorizer.CheckPermission(r.Context(), entity.ProjectURL(projectName), auth.EntitlementCanCreateImages)
	if err != nil && !auth.IsDeniedError(err) {
		return response.SmartError(err)
	} else if err == nil {
		userCanCreateImages = true
	}

	// Load the project entry so we have a valid project name.
	var dbProject *dbCluster.Project
	var projectConfig map[string]string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err = dbCluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return fmt.Errorf("Failed loading project %q: %w", projectName, err)
		}

		projectConfig, err = dbCluster.GetProjectConfig(ctx, tx.Tx(), projectName)
		if err != nil {
			return fmt.Errorf("Failed loading config for project %q: %w", projectName, err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	secret := r.Header.Get("X-LXD-secret")
	fingerprint := r.Header.Get("X-LXD-fingerprint")

	var imageMetadata map[string]any

	// If user does not have permission to create images. They must provide a secret and a fingerprint.
	if !userCanCreateImages && (secret == "" || fingerprint == "") {
		return response.Forbidden(nil)
	}

	// We need to invalidate the secret whether the source is trusted or not.
	op, err := imageValidSecret(s, r, dbProject.Name, fingerprint, secret, operationtype.ImageUploadToken)
	if err != nil {
		return response.SmartError(err)
	}

	if op != nil {
		imageMetadata = op.Metadata
	} else if !userCanCreateImages {
		return response.Forbidden(nil)
	}

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	// create a directory under which we keep everything while building
	builddir, err := os.MkdirTemp(s.ImagesStoragePath(dbProject.Name), "lxd_build_")
	if err != nil {
		return response.InternalError(err)
	}

	cleanup := func(path string, fd *os.File) {
		if fd != nil {
			_ = fd.Close()
		}

		err := os.RemoveAll(path)
		if err != nil {
			logger.Debugf("Error deleting temporary directory \"%s\": %s", path, err)
		}
	}

	// Store the post data to disk
	post, err := os.CreateTemp(builddir, "lxd_post_")
	if err != nil {
		cleanup(builddir, nil)
		return response.InternalError(err)
	}

	// Possibly set a quota on the amount of disk space this project is
	// allowed to use.
	var budget int64
	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		budget, err = limits.GetImageSpaceBudget(ctx, s.GlobalConfig, tx, dbProject.Name)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	_, err = io.Copy(shared.NewQuotaWriter(post, budget), r.Body)
	if err != nil {
		logger.Errorf("Store image POST data to disk: %v", err)
		cleanup(builddir, post)
		return response.InternalError(err)
	}

	// Is this a container request?
	_, err = post.Seek(0, io.SeekStart)
	if err != nil {
		return response.InternalError(err)
	}

	decoder := json.NewDecoder(post)
	imageUpload := false

	req := api.ImagesPost{}
	err = decoder.Decode(&req)
	if err != nil {
		if r.Header.Get("Content-Type") == "application/json" {
			cleanup(builddir, post)
			return response.BadRequest(err)
		}

		imageUpload = true
	}

	if !imageUpload && req.Source.Mode == "push" {
		cleanup(builddir, post)

		metadata := map[string]any{
			"aliases":    req.Aliases,
			"expires_at": req.ExpiresAt,
			"properties": req.Properties,
			"public":     req.Public,
		}

		return createImageTokenResponse(s, r, dbProject.Name, req.Source.Fingerprint, metadata, operationtype.ImageUploadToken)
	}

	if !imageUpload && !slices.Contains([]api.SourceType{"container", "instance", "virtual-machine", "snapshot", "image", "url"}, req.Source.Type) {
		cleanup(builddir, post)
		return response.InternalError(errors.New("Invalid images JSON"))
	}

	if req.CompressionAlgorithm != "" {
		err = validate.IsCompressionAlgorithm(req.CompressionAlgorithm)
		if err != nil {
			return response.BadRequest(err)
		}
	}

	// Forward requests for containers on other nodes.
	if !imageUpload && slices.Contains([]api.SourceType{"container", "instance", "virtual-machine", "snapshot"}, req.Source.Type) {
		name := req.Source.Name
		if name != "" {
			_, err = post.Seek(0, io.SeekStart)
			if err != nil {
				return response.InternalError(err)
			}

			r.Body = post
			resp, err := forwardedResponseIfInstanceIsRemote(r.Context(), s, dbProject.Name, name, instanceType)
			if err != nil {
				cleanup(builddir, post)
				return response.SmartError(err)
			}

			if resp != nil {
				cleanup(builddir, nil)
				return resp
			}
		}
	}

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	isClusterNotification := requestor.IsClusterNotification()

	// Begin background operation
	run := func(ctx context.Context, op *operations.Operation) error {
		var err error
		var info *api.Image

		// Setup the cleanup function
		defer cleanup(builddir, post)

		if imageUpload {
			/* Processing image upload */
			info, err = getImgPostInfo(s, r, builddir, dbProject.Name, post, imageMetadata)
		} else {
			// Project to associate image with.
			imageProject := dbProject.Name

			// If "features.images" is disabled for the project, associate the image with the "default" project.
			if shared.IsFalseOrEmpty(projectConfig["features.images"]) {
				imageProject = api.ProjectDefaultName
			}

			// Project to associate profiles with.
			profileProject := dbProject.Name

			// If "features.profiles" is disabled for the project, associate the profiles with the "default" project.
			if shared.IsFalseOrEmpty(projectConfig["features.profiles"]) {
				profileProject = api.ProjectDefaultName
			}

			switch req.Source.Type {
			case api.SourceTypeImage:
				/* Processing image copy from remote */
				info, err = imgPostRemoteInfo(ctx, s, req, op, profileProject, imageProject, budget)
			case "url":
				/* Processing image copy from URL */
				info, err = imgPostURLInfo(ctx, s, req, op, imageProject, budget)
			default:
				/* Processing image creation from container */
				imagePublishLock.Lock()
				info, err = imgPostInstanceInfo(s, req, op, dbProject.Name, imageProject, builddir, budget)
				imagePublishLock.Unlock()
			}
		}

		// Set the metadata if possible, even if there is an error
		if info != nil {
			metadata := make(map[string]any)
			metadata["fingerprint"] = info.Fingerprint
			metadata["size"] = strconv.FormatInt(info.Size, 10)

			// Keep secret if available
			secret, ok := op.Metadata()["secret"]
			if ok {
				metadata["secret"], ok = secret.(string)
				if !ok {
					return errors.New("Invalid type for field \"secret\"")
				}
			}

			_ = op.UpdateMetadata(metadata)
		}

		if err != nil {
			return err
		}

		if isClusterNotification {
			// If dealing with in-cluster image copy, don't touch the database.
			return nil
		}

		// Apply any provided alias
		aliases, ok := imageMetadata["aliases"]
		if ok {
			req.Aliases, ok = aliases.([]api.ImageAlias)
			if !ok {
				return errors.New("Invalid type for field \"aliases\"")
			}
		}

		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			imgID, _, err := tx.GetImageByFingerprintPrefix(ctx, info.Fingerprint, dbCluster.ImageFilter{Project: &dbProject.Name})
			if err != nil {
				return fmt.Errorf("Fetch image %q: %w", info.Fingerprint, err)
			}

			for _, alias := range req.Aliases {
				_, _, err := tx.GetImageAlias(ctx, dbProject.Name, alias.Name, true)
				if !response.IsNotFoundError(err) {
					if err != nil {
						return fmt.Errorf("Fetch image alias %q: %w", alias.Name, err)
					}

					return fmt.Errorf("Alias already exists: %s", alias.Name)
				}

				err = tx.CreateImageAlias(ctx, dbProject.Name, alias.Name, imgID, alias.Description)
				if err != nil {
					return fmt.Errorf("Add new image alias to the database: %w", err)
				}
			}

			return nil
		})
		if err != nil {
			return err
		}

		// Sync the images between each node in the cluster on demand
		err = imageSyncBetweenNodes(ctx, s, r, dbProject.Name, info.Fingerprint)
		if err != nil {
			return fmt.Errorf("Failed syncing image between nodes: %w", err)
		}

		s.Events.SendLifecycle(dbProject.Name, lifecycle.ImageCreated.Event(info.Fingerprint, dbProject.Name, op.EventLifecycleRequestor(), logger.Ctx{"type": info.Type}))

		return nil
	}

	var metadata map[string]any

	if imageUpload && imageMetadata != nil {
		secret, _ := shared.RandomCryptoString()
		if secret != "" {
			metadata = map[string]any{
				"secret": secret,
			}
		}
	}

	args := operations.OperationArgs{
		ProjectName: dbProject.Name,
		EntityURL:   api.NewURL().Path(version.APIVersion, "projects", dbProject.Name),
		Type:        operationtype.ImageDownload,
		Class:       operations.OperationClassTask,
		Metadata:    metadata,
		RunHook:     run,
	}

	imageOp, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		cleanup(builddir, post)
		return response.InternalError(err)
	}

	return operations.OperationResponse(imageOp)
}

func getImageMetadata(fname string) (*api.ImageMetadata, string, error) {
	var tr *tar.Reader
	var result api.ImageMetadata

	// Open the file
	r, err := os.Open(fname)
	if err != nil {
		return nil, "unknown", err
	}

	defer func() { _ = r.Close() }()

	// Decompress if needed
	_, algo, unpacker, err := shared.DetectCompressionFile(r)
	if err != nil {
		return nil, "unknown", err
	}

	_, err = r.Seek(0, io.SeekStart)
	if err != nil {
		return nil, "", err
	}

	if unpacker == nil {
		return nil, "unknown", errors.New("Unsupported backup compression")
	}

	// Open the tarball
	if len(unpacker) > 0 {
		if algo == ".squashfs" {
			// sqfs2tar can only read from a file
			unpacker = append(unpacker, fname)
		}

		cmd := exec.Command(unpacker[0], unpacker[1:]...)
		cmd.Stdin = r

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, "unknown", err
		}

		defer func() { _ = stdout.Close() }()

		err = cmd.Start()
		if err != nil {
			return nil, "unknown", err
		}

		defer func() { _ = cmd.Wait() }()

		// Double close stdout, this is to avoid blocks in Wait()
		defer func() { _ = stdout.Close() }()

		tr = tar.NewReader(stdout)
	} else {
		tr = tar.NewReader(r)
	}

	// Parse the content
	hasMeta := false
	hasRoot := false
	imageType := "unknown"
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // End of archive
		}

		if err != nil {
			return nil, "unknown", err
		}

		if hdr.Name == "metadata.yaml" || hdr.Name == "./metadata.yaml" {
			err = yaml.NewDecoder(tr).Decode(&result)
			if err != nil {
				return nil, "unknown", err
			}

			hasMeta = true
		}

		if strings.HasPrefix(hdr.Name, "rootfs/") || strings.HasPrefix(hdr.Name, "./rootfs/") {
			hasRoot = true
			imageType = instancetype.Container.String()
		}

		if hdr.Name == "rootfs.img" || hdr.Name == "./rootfs.img" {
			hasRoot = true
			imageType = instancetype.VM.String()
		}

		if hasMeta && hasRoot {
			// Done with the bits we want, no need to keep reading
			break
		}
	}

	if !hasMeta {
		return nil, "unknown", errors.New("Metadata tarball is missing metadata.yaml")
	}

	_, err = osarch.ArchitectureId(result.Architecture)
	if err != nil {
		return nil, "unknown", err
	}

	if result.CreationDate == 0 {
		return nil, "unknown", errors.New("Missing creation date")
	}

	return &result, imageType, nil
}

func doImagesGet(ctx context.Context, tx *db.ClusterTx, recursion bool, projectName string, public bool, clauses *filter.ClauseSet, hasPermission auth.PermissionChecker, allProjects bool) (any, error) {
	mustLoadObjects := recursion || (clauses != nil && len(clauses.Clauses) > 0)

	imagesProjectsMap := map[string][]string{}
	if allProjects {
		var err error

		imagesProjectsMap, err = tx.GetImages(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		fingerprints, err := tx.GetImagesFingerprints(ctx, projectName, public)
		if err != nil {
			return nil, err
		}

		for _, fingerprint := range fingerprints {
			imagesProjectsMap[fingerprint] = []string{projectName}
		}
	}

	var resultString []string
	var resultMap []*api.Image

	if recursion {
		resultMap = make([]*api.Image, 0, len(imagesProjectsMap))
	} else {
		resultString = make([]string, 0, len(imagesProjectsMap))
	}

	for fingerprint, projects := range imagesProjectsMap {
		hasAccess := false

		image, err := doImageGet(ctx, tx, projects[0], fingerprint, public)
		if err != nil {
			continue
		}

		for _, project := range projects {
			if image.Public || hasPermission(entity.ImageURL(project, fingerprint)) {
				hasAccess = true
				break
			}
		}

		if !hasAccess {
			continue
		}

		if !mustLoadObjects {
			resultString = append(resultString, api.NewURL().Path(version.APIVersion, "images", fingerprint).String())
		} else {
			if clauses != nil && len(clauses.Clauses) > 0 {
				match, err := filter.Match(*image, *clauses)
				if err != nil {
					return nil, err
				}

				if !match {
					continue
				}
			}

			if recursion {
				resultMap = append(resultMap, image)
			} else {
				resultString = append(resultString, api.NewURL().Path(version.APIVersion, "images", image.Fingerprint).String())
			}
		}
	}

	if recursion {
		return resultMap, nil
	}

	return resultString, nil
}

// swagger:operation GET /1.0/images?public images images_get_untrusted
//
//  Get the public images
//
//  Returns a list of publicly available images (URLs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: filter
//      description: Collection filter
//      type: string
//      example: default
//    - in: query
//      name: all-projects
//      description: Retrieve images from all projects
//      type: boolean
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/images/06b86454720d36b20f94e31c6812e05ec51c1b568cf3a8abd273769d213394bb",
//                "/1.0/images/084dd79dd1360fd25a2479eb46674c2a5ef3022a40fe03c91ab3603e3402b8e1"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/images?public&recursion=1 images images_get_recursion1_untrusted
//
//  Get the public images
//
//  Returns a list of publicly available images (structs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: filter
//      description: Collection filter
//      type: string
//      example: default
//    - in: query
//      name: all-projects
//      description: Retrieve images from all projects
//      type: boolean
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of images
//            items:
//              $ref: "#/definitions/Image"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/images images images_get
//
//  Get the images
//
//  Returns a list of images (URLs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: filter
//      description: Collection filter
//      type: string
//      example: default
//    - in: query
//      name: all-projects
//      description: Retrieve images from all projects
//      type: boolean
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/images/06b86454720d36b20f94e31c6812e05ec51c1b568cf3a8abd273769d213394bb",
//                "/1.0/images/084dd79dd1360fd25a2479eb46674c2a5ef3022a40fe03c91ab3603e3402b8e1"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/images?recursion=1 images images_get_recursion1
//
//	Get the images
//
//	Returns a list of images (structs).
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: filter
//	    description: Collection filter
//	    type: string
//	    example: default
//	  - in: query
//	    name: all-projects
//	    description: Retrieve images from all projects
//	    type: boolean
//	    example: default
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of images
//	          items:
//	            $ref: "#/definitions/Image"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imagesGet(d *Daemon, r *http.Request) response.Response {
	filterStr := r.FormValue("filter")

	recursion, _ := util.IsRecursionRequest(r)
	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeImage, true)
	if err != nil {
		return response.SmartError(err)
	}

	projectName, allProjects, err := request.ProjectParams(r)
	if err != nil {
		return response.SmartError(err)
	}

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	trusted := requestor.IsTrusted()

	s := d.State()
	if !allProjects && trusted && projectName != api.ProjectDefaultName {
		var effectiveProjectName string
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			effectiveProjectName, err = projectutils.ImageProject(ctx, tx.Tx(), projectName)
			return err
		})
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				// Return a generic not found so that the caller cannot determine the existence of a project by the
				// contents of the error message.
				return response.NotFound(nil)
			}

			return response.SmartError(err)
		}

		request.SetContextValue(r, request.CtxEffectiveProjectName, effectiveProjectName)
	}

	// If the caller is not trusted, we only want to list public images in the default project.
	publicOnly := !trusted

	// Get a permission checker. If the caller is not authenticated, the permission checker will deny all.
	// However, the permission checker is only called when an image is private. Both trusted and untrusted clients will
	// still see public images.
	canViewImage, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeImage)
	if err != nil {
		return response.SmartError(err)
	}

	clauses, err := filter.Parse(filterStr, filter.QueryOperatorSet())
	if err != nil {
		return response.SmartError(fmt.Errorf("Invalid filter: %w", err))
	}

	var result any
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		result, err = doImagesGet(ctx, tx, recursion > 0, projectName, publicOnly, clauses, canViewImage, allProjects)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if len(withEntitlements) > 0 {
		// We need to get each image project and fingerprint to construct its entity URL,
		// so we need to cast the result to a slice of `api.Image` pointers.
		// This cast should work as we would have never entered this if block if the request was not set with recursion=1,
		// thanks to the `extractEntitlementsFromQuery` function passed with `allowRecursion=true`.
		images, ok := result.([]*api.Image)
		if !ok {
			return response.InternalError(errors.New("Images response is not a slice of Image pointer structs"))
		}

		urlToImage := make(map[*api.URL]auth.EntitlementReporter, len(images))
		for _, image := range images {
			urlToImage[entity.ImageURL(image.Project, image.Fingerprint)] = image
		}

		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeImage, withEntitlements, urlToImage)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponse(true, result)
}

func autoUpdateImagesTask(stateFunc func() *state.State) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := stateFunc()

		opRun := func(ctx context.Context, op *operations.Operation) error {
			return autoUpdateImages(ctx, s)
		}

		args := operations.OperationArgs{
			Type:    operationtype.ImagesUpdate,
			Class:   operations.OperationClassTask,
			RunHook: opRun,
		}

		op, err := operations.ScheduleServerOperation(s, args)
		if err != nil {
			logger.Error("Failed creating image update operation", logger.Ctx{"err": err})
			return
		}

		logger.Debug("Acquiring image task lock")
		imageTaskMu.Lock()
		defer imageTaskMu.Unlock()
		logger.Debug("Acquired image task lock")

		logger.Info("Updating images")
		err = op.Start()
		if err != nil {
			logger.Error("Failed starting image update operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed updating images", logger.Ctx{"err": err})
			return
		}

		logger.Info("Done updating images")
	}

	return f, task.Hourly()
}

func autoUpdateImages(ctx context.Context, s *state.State) error {
	imageMap := make(map[string][]dbCluster.Image)

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		autoUpdate := true
		images, err := dbCluster.GetImages(ctx, tx.Tx(), dbCluster.ImageFilter{AutoUpdate: &autoUpdate})
		if err != nil {
			return err
		}

		for _, image := range images {
			imageMap[image.Fingerprint] = append(imageMap[image.Fingerprint], image)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Unable to retrieve image fingerprints: %w", err)
	}

	for fingerprint, images := range imageMap {
		var nodes []db.NodeInfo
		var nodeIDs []int64

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			nodeAddresses, err := tx.GetNodesWithImageAndAutoUpdate(ctx, fingerprint, true)
			if err != nil {
				return fmt.Errorf("Failed getting cluster members with auto-update images: %w", err)
			}

			for _, nodeAddress := range nodeAddresses {
				nodeInfo, err := tx.GetNodeByAddress(ctx, nodeAddress)
				if err != nil {
					return fmt.Errorf("Failed retrieving cluster member information for %q: %w", nodeAddress, err)
				}

				nodes = append(nodes, nodeInfo)
				nodeIDs = append(nodeIDs, nodeInfo.ID)
			}

			return err
		})
		if err != nil {
			logger.Warn("Failed getting image auto-update info", logger.Ctx{"member": s.ServerName, "fingerprint": fingerprint, "err": err})
			continue
		}

		if len(nodeIDs) > 1 {
			// If multiple nodes have the image, select one to deal with it.
			selectedNode, err := util.GetStableRandomInt64FromList(int64(len(images)), nodeIDs)
			if err != nil {
				logger.Error("Failed to select cluster member for image update", logger.Ctx{"err": err})
				continue
			}

			// Skip image update if we're not the chosen cluster member.
			// That way, an image is only updated by a single cluster member.
			if s.DB.Cluster.GetNodeID() != selectedNode {
				continue
			}
		}

		var deleteIDs []int
		var newImage *api.Image

		for _, image := range images {
			l := logger.AddContext(logger.Ctx{"member": s.ServerName, "project": image.Project, "fingerprint": image.Fingerprint})

			filter := dbCluster.ImageFilter{Project: &image.Project}
			if image.Public {
				filter.Public = &image.Public
			}

			var imageInfo *api.Image

			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				_, imageInfo, err = tx.GetImage(ctx, image.Fingerprint, filter)

				return err
			})
			if err != nil {
				l.Error("Failed to get image", logger.Ctx{"err": err})
				continue
			}

			newInfo, err := autoUpdateImage(ctx, s, nil, image.ID, imageInfo, image.Project, false)
			if err != nil {
				l.Error("Failed to update image", logger.Ctx{"err": err})

				if err == context.Canceled {
					return nil
				}
			} else {
				deleteIDs = append(deleteIDs, image.ID)
			}

			// newInfo will have the same content for each image in the list.
			// Therefore, we just pick the first.
			if newImage == nil {
				newImage = newInfo
			}
		}

		if newImage != nil {
			if len(nodes) > 1 {
				err := distributeImage(ctx, s, nodes, fingerprint, newImage)
				if err != nil {
					logger.Error("Failed to distribute new image", logger.Ctx{"member": s.ServerName, "fingerprint": newImage.Fingerprint, "err": err})

					if err == context.Canceled {
						return nil
					}
				}
			}

			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				for _, ID := range deleteIDs {
					// Remove the database entry for the image after distributing to cluster members.
					err := tx.DeleteImage(ctx, ID)
					if err != nil {
						return fmt.Errorf(`Failed deleting image record with ID "%d": %w`, ID, err)
					}
				}

				return nil
			})
			if err != nil {
				logger.Error("Error deleting old image(s) records", logger.Ctx{"member": s.ServerName, "fingerprint": fingerprint, "err": err})
			}
		}
	}

	return nil
}

func distributeImage(ctx context.Context, s *state.State, nodes []db.NodeInfo, oldFingerprint string, newImage *api.Image) error {
	logger.Info("Distributing image to members", logger.Ctx{"fingerprint": newImage.Fingerprint, "member": s.ServerName, "remotes": len(nodes)})

	// Get config of all nodes (incl. own) and check for storage.images_volume.
	// If the setting is missing, distribute the image to the node.
	// If the option is set, only distribute the image once to nodes with this
	// specific pool/volume.

	// volumesWhichAlreadyHaveImage is a list of all image volumes specified by
	// storage.images_volume which are remote, and thus the image was already distributed
	// there by the main node. Since this option is node specific, the values
	// may be different for each cluster member.
	var volumesWhichAlreadyHaveImage []string

	err := s.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		config, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		vol := config.StorageImagesVolume(newImage.Project)
		if vol != "" {
			fields := strings.Split(vol, "/")

			var pool *api.StoragePool

			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				_, pool, _, err = tx.GetStoragePool(ctx, fields[0])

				return err
			})
			if err != nil {
				return fmt.Errorf("Failed to get storage pool info: %w", err)
			}

			// Add the volume to the list if the pool is backed by remote
			// storage as only then the volumes are shared.
			if slices.Contains(db.StorageRemoteDriverNames(), pool.Driver) {
				volumesWhichAlreadyHaveImage = append(volumesWhichAlreadyHaveImage, vol)
			}
		}

		return nil
	})
	// No need to return with an error as this is only an optimization in the
	// distribution process. Instead, only log the error.
	if err != nil {
		logger.Error("Failed to load config", logger.Ctx{"err": err})
	}

	// Skip own node
	localClusterAddress := s.LocalConfig.ClusterAddress()

	var poolIDs []int64
	var poolNames []string

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the IDs of all storage pools on which a storage volume
		// for the requested image currently exists.
		poolIDs, err = tx.GetPoolsWithImage(ctx, newImage.Fingerprint)
		if err != nil {
			logger.Error("Error getting image storage pools", logger.Ctx{"err": err, "fingerprint": oldFingerprint})
			return err
		}

		// Translate the IDs to poolNames.
		poolNames, err = tx.GetPoolNamesFromIDs(ctx, poolIDs)
		if err != nil {
			logger.Error("Error getting image storage pools", logger.Ctx{"err": err, "fingerprint": oldFingerprint})
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	for _, node := range nodes {
		if node.Address == localClusterAddress {
			continue
		}

		err = func() error {
			client, err := cluster.Connect(context.Background(), node.Address, s.Endpoints.NetworkCert(), s.ServerCert(), true)
			if err != nil {
				return fmt.Errorf("Failed to connect to %q for image synchronization: %w", node.Address, err)
			}

			client = client.UseTarget(node.Name)

			resp, _, err := client.GetServer()
			if err != nil {
				logger.Error("Failed to retrieve information about cluster member", logger.Ctx{"err": err, "remote": node.Address})
			} else {
				vol := ""

				val := resp.Config["storage.project."+newImage.Project+".images_volume"]
				if val == nil {
					val = resp.Config["storage.images_volume"]
				}

				if val != nil {
					var ok bool
					vol, ok = val.(string)
					if !ok {
						return errors.New("Invalid type for field \"storage.images_volume\"")
					}
				}

				// If storage.images_volume is set on the cluster member, check if
				// the image has already been downloaded to this volume. If so,
				// skip distributing the image to this cluster member.
				// If the option is unset, distribute the image.
				if vol != "" {
					if slices.Contains(volumesWhichAlreadyHaveImage, vol) {
						return nil
					}

					fields := strings.Split(vol, "/")

					pool, _, err := client.GetStoragePool(fields[0])
					if err != nil {
						logger.Error("Failed to get storage pool info", logger.Ctx{"err": err, "pool": fields[0]})
					} else {
						if slices.Contains(db.StorageRemoteDriverNames(), pool.Driver) {
							volumesWhichAlreadyHaveImage = append(volumesWhichAlreadyHaveImage, vol)
						}
					}
				}
			}

			imageMetaPath := filepath.Join(s.ImagesStoragePath(newImage.Project), newImage.Fingerprint)
			imageRootfsPath := imageMetaPath + ".rootfs"

			metaFile, err := os.Open(imageMetaPath)
			if err != nil {
				return err
			}

			defer func() { _ = metaFile.Close() }()

			createArgs := &lxd.ImageCreateArgs{
				MetaFile: metaFile,
				MetaName: filepath.Base(metaFile.Name()),
				Type:     newImage.Type,
			}

			if shared.PathExists(imageRootfsPath) {
				rootfsFile, err := os.Open(imageRootfsPath)
				if err != nil {
					return err
				}

				defer func() { _ = rootfsFile.Close() }()

				createArgs.RootfsFile = rootfsFile
				createArgs.RootfsName = filepath.Base(rootfsFile.Name())
			}

			image := api.ImagesPost{
				Filename: createArgs.MetaName,
			}

			logger.Info("Distributing image to member", logger.Ctx{"member": s.ServerName, "remote": node.Name, "fingerprint": newImage.Fingerprint})
			op, err := client.CreateImage(image, createArgs)
			if err != nil {
				return err
			}

			err = ctx.Err()
			if err != nil {
				_ = op.Cancel()
				return err
			}

			err = op.Wait()
			if err != nil {
				return err
			}

			for _, poolName := range poolNames {
				if poolName == "" {
					continue
				}

				req := internalImageOptimizePost{
					Image:   *newImage,
					Pool:    poolName,
					Project: newImage.Project,
				}

				_, _, err = client.RawQuery(http.MethodPost, "/internal/image-optimize", req, "")
				if err != nil {
					logger.Error("Failed creating new image in storage pool", logger.Ctx{"err": err, "remote": node.Address, "pool": poolName, "fingerprint": newImage.Fingerprint})
				}

				op, err := client.DeleteStoragePoolVolume(poolName, "image", oldFingerprint)
				if err == nil {
					err = op.WaitContext(s.ShutdownCtx)
				}

				if err != nil {
					logger.Error("Failed deleting old image from storage pool", logger.Ctx{"err": err, "remote": node.Address, "pool": poolName, "fingerprint": oldFingerprint})
				}
			}

			return nil
		}()
		if err != nil {
			return fmt.Errorf("Failed distributing image %q to %q: %w", newImage.Fingerprint, node.Name, err)
		}
	}

	return nil
}

// Update a single image.  The operation can be nil, if no progress tracking is needed.
// Returns whether the image has been updated.
func autoUpdateImage(ctx context.Context, s *state.State, op *operations.Operation, id int, info *api.Image, projectName string, manual bool) (*api.Image, error) {
	fingerprint := info.Fingerprint
	var source *api.ImageSource

	if !manual {
		var interval int64

		var project *api.Project
		err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			p, err := dbCluster.GetProject(ctx, tx.Tx(), projectName)
			if err != nil {
				return err
			}

			project, err = p.ToAPI(ctx, tx.Tx())
			return err
		})
		if err != nil {
			return nil, err
		}

		if project.Config["images.auto_update_interval"] != "" {
			interval, err = strconv.ParseInt(project.Config["images.auto_update_interval"], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Unable to fetch project configuration: %w", err)
			}
		} else {
			interval = s.GlobalConfig.ImagesAutoUpdateIntervalHours()
		}

		// Check if we're supposed to auto update at all (0 disables it)
		if interval <= 0 {
			return nil, nil
		}

		now := time.Now()
		elapsedHours := int64(math.Round(now.Sub(s.StartTime).Hours()))
		if elapsedHours%interval != 0 {
			return nil, nil
		}
	}

	var poolNames []string

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		_, source, err = dbCluster.GetImageSource(ctx, tx.Tx(), id)
		if err != nil {
			logger.Error("Error getting source image", logger.Ctx{"err": err, "fingerprint": fingerprint})
			return err
		}

		// Get the IDs of all storage pools on which a storage volume
		// for the requested image currently exists.
		poolIDs, err := tx.GetPoolsWithImage(ctx, fingerprint)
		if err != nil {
			logger.Error("Error getting image pools", logger.Ctx{"err": err, "fingerprint": fingerprint})
			return err
		}

		// Translate the IDs to poolNames.
		poolNames, err = tx.GetPoolNamesFromIDs(ctx, poolIDs)
		if err != nil {
			logger.Error("Error getting image pools", logger.Ctx{"err": err, "fingerprint": fingerprint})
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// If no optimized pools at least update the base store
	if len(poolNames) == 0 {
		poolNames = append(poolNames, "")
	}

	logger.Info("Checking image update", logger.Ctx{"member": s.ServerName, "poolNames": poolNames, "project": projectName, "fingerprint": fingerprint, "source": source.Server, "protocol": source.Protocol, "alias": source.Alias})

	// Set operation metadata to indicate whether a refresh happened
	setRefreshResult := func(result bool) {
		if op == nil {
			return
		}

		metadata := map[string]any{"refreshed": result}
		_ = op.UpdateMetadata(metadata)

		// Sent a lifecycle event if the refresh actually happened.
		if result {
			s.Events.SendLifecycle(projectName, lifecycle.ImageRefreshed.Event(fingerprint, projectName, op.EventLifecycleRequestor(), nil))
		}
	}

	// Update the image on each pool where it currently exists.
	hash := fingerprint
	var newInfo *api.Image

	for _, poolName := range poolNames {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		newInfo, err = ImageDownload(context.Background(), s, op, &ImageDownloadArgs{
			Server:      source.Server,
			Protocol:    source.Protocol,
			Certificate: source.Certificate,
			Alias:       source.Alias,
			Type:        info.Type,
			AutoUpdate:  true,
			Public:      info.Public,
			StoragePool: poolName,
			ProjectName: projectName,
			Budget:      -1,
		})
		if err != nil {
			logger.Error("Failed to update the image", logger.Ctx{"err": err, "fingerprint": fingerprint})
			continue
		}

		hash = newInfo.Fingerprint
		if hash == fingerprint {
			logger.Debug("Image already up to date", logger.Ctx{"fingerprint": fingerprint})
			continue
		}

		var newID int

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			newID, _, err = tx.GetImage(ctx, hash, dbCluster.ImageFilter{Project: &projectName})

			return err
		})
		if err != nil {
			logger.Error("Error loading image", logger.Ctx{"err": err, "fingerprint": hash})
			continue
		}

		if info.Cached {
			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.SetImageCachedAndLastUseDate(ctx, projectName, hash, info.LastUsedAt)
			})
			if err != nil {
				logger.Error("Error setting cached flag and last use date", logger.Ctx{"err": err, "fingerprint": hash})
				continue
			}
		} else {
			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				err = tx.UpdateImageLastUseDate(ctx, projectName, hash, info.LastUsedAt)
				if err != nil {
					logger.Error("Error setting last use date", logger.Ctx{"err": err, "fingerprint": hash})
					return err
				}

				return nil
			})
			if err != nil {
				continue
			}
		}

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.MoveImageAlias(ctx, id, newID)
		})
		if err != nil {
			logger.Error("Error moving aliases", logger.Ctx{"err": err, "fingerprint": hash})
			continue
		}

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.CopyDefaultImageProfiles(ctx, id, newID)
		})
		if err != nil {
			logger.Error("Copying default profiles", logger.Ctx{"err": err, "fingerprint": hash})
		}

		// If we do have optimized pools, make sure we remove the volumes associated with the image.
		if poolName != "" {
			pool, err := storagePools.LoadByName(s, poolName)
			if err != nil {
				logger.Error("Error loading storage pool to delete image", logger.Ctx{"err": err, "pool": poolName, "fingerprint": fingerprint})
				continue
			}

			err = pool.DeleteImage(fingerprint, op)
			if err != nil {
				logger.Error("Error deleting image from storage pool", logger.Ctx{"err": err, "pool": pool.Name(), "fingerprint": fingerprint})
				continue
			}
		}
	}

	// Image didn't change, nothing to do.
	if hash == fingerprint {
		setRefreshResult(false)
		return nil, nil
	}

	// Remove main image file and rootfs file from disk.
	err = imageDeleteFromDisk(s.ImagesStoragePath(projectName), fingerprint)
	if err != nil {
		logger.Error("Failed deleting image from disk", logger.Ctx{"project": projectName, "fingerprint": fingerprint, "err": err})
	}

	setRefreshResult(true)
	return newInfo, nil
}

func pruneExpiredImagesTask(stateFunc func() *state.State) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := stateFunc()

		opRun := func(ctx context.Context, op *operations.Operation) error {
			return pruneExpiredImages(ctx, s, op)
		}

		args := operations.OperationArgs{
			Type:    operationtype.ImagesExpire,
			Class:   operations.OperationClassTask,
			RunHook: opRun,
		}

		op, err := operations.ScheduleServerOperation(s, args)
		if err != nil {
			logger.Error("Failed creating expired image prune operation", logger.Ctx{"err": err})
			return
		}

		logger.Debug("Acquiring image task lock")
		imageTaskMu.Lock()
		defer imageTaskMu.Unlock()
		logger.Debug("Acquired image task lock")

		logger.Info("Pruning expired images")
		err = op.Start()
		if err != nil {
			logger.Error("Failed starting expired image prune operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed expiring images", logger.Ctx{"err": err})
			return
		}

		logger.Info("Done pruning expired images")
	}

	// Skip the first run, and instead run an initial pruning synchronously
	// before we start updating images later on in the start up process.
	f(context.Background())

	first := true
	schedule := func() (time.Duration, error) {
		interval := 24 * time.Hour
		if first {
			first = false
			return interval, task.ErrSkip
		}

		return interval, nil
	}

	return f, schedule
}

func pruneLeftoverImages(s *state.State) {
	isMultiNodeVolume := func(volume string) (bool, error) {
		if volume == "" {
			return false, nil
		}

		// Parse the source.
		poolName, _, err := daemonStorageSplitVolume(volume)
		if err != nil {
			return false, err
		}

		// Load the pool.
		pool, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			return false, err
		}

		return pool.Driver().Info().VolumeMultiNode, nil
	}

	opRun := func(ctx context.Context, op *operations.Operation) error {
		// Get all projects and images
		var imagesOnNode map[string][]string
		var projects []string
		err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			var err error
			imagesOnNode, err = tx.GetImagesOnLocalNode(ctx)
			if err != nil {
				return err
			}

			projects, err = dbCluster.GetProjectNames(ctx, tx.Tx())
			return err
		})
		if err != nil {
			return fmt.Errorf("Unable to retrieve the list of images: %w", err)
		}

		// First get list of storage volumes we'll be pruning images from.
		imagesInStorageVolumes := make(map[string][]string)
		// Make sure to include default daemon storage
		imagesInStorageVolumes[s.LocalConfig.StorageImagesVolume("")] = make([]string, 0)
		// And include all of the possibly configured project images storages too
		for _, project := range projects {
			storageVolume := s.LocalConfig.StorageImagesVolume(project)
			_, ok := imagesInStorageVolumes[storageVolume]
			if !ok {
				imagesInStorageVolumes[storageVolume] = make([]string, 0)
			}
		}

		// Get a list of image storage volumes, with a list of images per each storage volume.
		for image, projects := range imagesOnNode {
			for _, project := range projects {
				storageVolume := s.LocalConfig.StorageImagesVolume(project)
				imagesInStorageVolumes[storageVolume] = append(imagesInStorageVolumes[storageVolume], image)
			}
		}

		// Now walk all these image volumes and make sure these only contain the images they should.
		for volume, images := range imagesInStorageVolumes {
			isShared, err := isMultiNodeVolume(volume)
			if err != nil {
				return err
			}

			// Check if dealing with shared image storage.
			// Skip cleanup if image volume may be multi-node. When such a volume is used,
			// we may have images that are tied to other servers in the shared images folder
			// and don't want to delete those.
			if isShared {
				continue
			}

			// Look at what's in the images directory
			imagesDir := daemonStoragePath(volume, config.DaemonStorageTypeImages)
			entries, err := os.ReadDir(imagesDir)
			if err != nil {
				return fmt.Errorf("Unable to list the images directory: %w", err)
			}

			// Check and delete leftovers
			for _, entry := range entries {
				fp := strings.Split(entry.Name(), ".")[0]
				if !slices.Contains(images, fp) {
					err = os.Remove(filepath.Join(imagesDir, entry.Name()))
					if err != nil {
						return fmt.Errorf("Unable to remove leftover image: %v: %w", entry.Name(), err)
					}

					logger.Debugf("Removed leftover image file: %s", entry.Name())
				}
			}
		}

		return nil
	}

	args := operations.OperationArgs{
		Type:    operationtype.ImagesPruneLeftover,
		Class:   operations.OperationClassTask,
		RunHook: opRun,
	}

	op, err := operations.ScheduleServerOperation(s, args)
	if err != nil {
		logger.Error("Failed creating leftover image clean up operation", logger.Ctx{"err": err})
		return
	}

	logger.Debug("Acquiring image task lock")
	imageTaskMu.Lock()
	defer imageTaskMu.Unlock()
	logger.Debug("Acquired image task lock")

	logger.Info("Cleaning up leftover image files")
	err = op.Start()
	if err != nil {
		logger.Error("Failed starting leftover image clean up operation", logger.Ctx{"err": err})
		return
	}

	err = op.Wait(s.ShutdownCtx)
	if err != nil {
		logger.Error("Failed cleaning up leftover image files", logger.Ctx{"err": err})
		return
	}

	logger.Info("Done cleaning up leftover image files")
}

func pruneExpiredImages(ctx context.Context, s *state.State, op *operations.Operation) error {
	var err error
	var projectsImageRemoteCacheExpiryDays map[string]int64
	var allImages map[string][]dbCluster.Image

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Get an image remote cache expiry days value for each project and store keyed on project name.
		globalImageRemoteCacheExpiryDays := s.GlobalConfig.ImagesRemoteCacheExpiryDays()

		dbProjects, err := dbCluster.GetProjects(ctx, tx.Tx())
		if err != nil {
			return err
		}

		projectsImageRemoteCacheExpiryDays = make(map[string]int64, len(dbProjects))
		for _, p := range dbProjects {
			p, err := p.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			// If there is a project specific image expiry set use that.
			if p.Config["images.remote_cache_expiry"] != "" {
				expiry, err := strconv.ParseInt(p.Config["images.remote_cache_expiry"], 10, 64)
				if err != nil {
					return fmt.Errorf("Unable to fetch project configuration: %w", err)
				}

				projectsImageRemoteCacheExpiryDays[p.Name] = expiry
			} else {
				// Otherwise use the global default.
				projectsImageRemoteCacheExpiryDays[p.Name] = globalImageRemoteCacheExpiryDays
			}
		}

		// Get images across all projects and store them keyed on fingerprint.
		images, err := dbCluster.GetImages(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed getting images: %w", err)
		}

		allImages = make(map[string][]dbCluster.Image, len(images))
		for _, image := range images {
			allImages[image.Fingerprint] = append(allImages[image.Fingerprint], image)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Unable to retrieve project names: %w", err)
	}

	for fingerprint, dbImages := range allImages {
		// At each iteration we check if we got cancelled in the meantime. It is safe to abort here since
		// anything not expired now will be expired at the next run.
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		deleteAtStorage := make(map[string]bool)
		for _, dbImage := range dbImages {
			projectImagesStorageVolume := s.LocalConfig.StorageImagesVolume(dbImage.Project)

			if !dbImage.Cached {
				// Prevent image files from being deleted because the fingerprint is referenced by
				// a non-cached image.
				deleteAtStorage[projectImagesStorageVolume] = false
				continue // Skip because non-cached images must not be pruned.
			}

			_, imageSeenInThisStoragePreviously := deleteAtStorage[projectImagesStorageVolume]
			if !imageSeenInThisStoragePreviously {
				// First record of a cached image in this project, candidate for image files deletion.
				deleteAtStorage[projectImagesStorageVolume] = true
			}

			// Get expiry days for image's project.
			expiryDays := projectsImageRemoteCacheExpiryDays[dbImage.Project]

			// Skip if no project expiry time set.
			if expiryDays <= 0 {
				deleteAtStorage[projectImagesStorageVolume] = false
				continue
			}

			// Figure out the expiry of image.
			timestamp := dbImage.UploadDate
			if !dbImage.LastUseDate.Time.IsZero() {
				timestamp = dbImage.LastUseDate.Time
			}

			imageExpiry := timestamp.Add(time.Duration(expiryDays) * time.Hour * 24)

			// Skip if image is not expired.
			if imageExpiry.After(time.Now()) {
				deleteAtStorage[projectImagesStorageVolume] = false
				continue
			}

			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				// Remove the database entry for the image.
				return tx.DeleteImage(ctx, dbImage.ID)
			})
			if err != nil {
				return fmt.Errorf("Error deleting image %q in project %q from database: %w", fingerprint, dbImage.Project, err)
			}

			logger.Info("Deleted expired cached image record", logger.Ctx{"fingerprint": fingerprint, "project": dbImage.Project, "expiry": imageExpiry})

			s.Events.SendLifecycle(dbImage.Project, lifecycle.ImageDeleted.Event(fingerprint, dbImage.Project, op.EventLifecycleRequestor(), nil))
		}

		// Remove main image files from projects which don't have any images referenced.
		deleteStorageVolumes := true
		for projectImagesVolume, deleteImageFileInProject := range deleteAtStorage {
			if !deleteImageFileInProject {
				// Other projects are still using the image at this location.
				deleteStorageVolumes = false
				continue
			}

			err := imageDeleteFromDisk(projectImagesVolume, fingerprint)
			if err != nil {
				return err
			}
		}

		// Skip deleting the image files and image storage volumes on disk if image is not expired in all
		// of its projects.
		if !deleteStorageVolumes {
			continue
		}

		var poolIDs []int64
		var poolNames []string

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Get the IDs of all storage pools on which a storage volume for the image currently exists.
			poolIDs, err = tx.GetPoolsWithImage(ctx, fingerprint)
			if err != nil {
				return err
			}

			// Translate the IDs to poolNames.
			poolNames, err = tx.GetPoolNamesFromIDs(ctx, poolIDs)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			continue
		}

		for _, poolName := range poolNames {
			pool, err := storagePools.LoadByName(s, poolName)
			if err != nil {
				return fmt.Errorf("Error loading storage pool %q to delete image volume %q: %w", poolName, fingerprint, err)
			}

			err = pool.DeleteImage(fingerprint, op)
			if err != nil {
				return fmt.Errorf("Error deleting image volume %q from storage pool %q: %w", fingerprint, pool.Name(), err)
			}
		}

		logger.Info("Deleted expired cached image files and volumes", logger.Ctx{"fingerprint": fingerprint})
	}

	return nil
}

// swagger:operation DELETE /1.0/images/{fingerprint} images image_delete
//
//	Delete the image
//
//	Removes the image from the image store.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	effectiveProjectName, err := request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	details, err := request.GetContextValue[imageDetails](r.Context(), ctxImageDetails)
	if err != nil {
		return response.SmartError(err)
	}

	var opCreator operations.OperationScheduler = func(s *state.State, args operations.OperationArgs) (*operations.Operation, error) {
		return operations.ScheduleUserOperationFromRequest(s, r, args)
	}

	op, err := doImageDelete(r.Context(), opCreator, s, details.image.Fingerprint, details.imageID, projectName, effectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	return operations.OperationResponse(op)
}

// doImageDelete deletes an image with the given fingerprint and imageID in the given project.
func doImageDelete(ctx context.Context, opCreator operations.OperationScheduler, s *state.State, fingerprint string, imageID int, requestProjectName string, effectiveProjectName string) (*operations.Operation, error) {
	requestor, err := request.GetRequestor(ctx)
	if err != nil {
		return nil, err
	}

	isClusterNotification := requestor.IsClusterNotification()
	do := func(ctx context.Context, op *operations.Operation) error {
		// Lock this operation to ensure that concurrent image operations don't conflict.
		// Other operations will wait for this one to finish.
		unlock, err := imageOperationLock(fingerprint)
		if err != nil {
			return err
		}

		defer unlock()

		var exist bool

		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			// Check image still exists and another request hasn't removed it since we resolved the image
			// fingerprint above.
			exist, err = tx.ImageExists(ctx, requestProjectName, fingerprint)

			return err
		})
		if err != nil {
			return err
		}

		if !exist {
			return api.StatusErrorf(http.StatusNotFound, "Image not found")
		}

		if !isClusterNotification {
			var referenced bool

			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				// Check if the image being deleted is actually still
				// referenced by other projects. In that case we don't want to
				// physically delete it just yet, but just to remove the
				// relevant database entry.
				projects, err := tx.ProjectsWithImage(ctx, fingerprint)
				if err != nil {
					return err
				}

				// See if any other project with this image has the same storage volume
				projectImagesVolume := s.LocalConfig.StorageImagesVolume(requestProjectName)
				for _, project := range projects {
					if project == requestProjectName {
						continue
					}

					if s.LocalConfig.StorageImagesVolume(project) == projectImagesVolume {
						referenced = true
						break
					}
				}

				if referenced {
					err = tx.DeleteImage(ctx, imageID)
					if err != nil {
						return fmt.Errorf("Error deleting image info from the database: %w", err)
					}
				}

				return nil
			})
			if err != nil {
				return err
			}

			if referenced {
				return nil
			}

			// Notify the other nodes about the removed image so they can remove it from disk too.
			notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAll)
			if err != nil {
				return err
			}

			err = notifier(func(member db.NodeInfo, client lxd.InstanceServer) error {
				op, err := client.UseProject(requestProjectName).DeleteImage(fingerprint)
				if err != nil {
					return fmt.Errorf("Failed to request to delete image from peer node: %w", err)
				}

				err = op.Wait()
				if err != nil {
					return fmt.Errorf("Failed to delete image from peer node: %w", err)
				}

				return nil
			})
			if err != nil {
				return err
			}
		}

		var poolIDs []int64
		var poolNames []string

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Delete the pool volumes.
			poolIDs, err = tx.GetPoolsWithImage(ctx, fingerprint)
			if err != nil {
				return err
			}

			poolNames, err = tx.GetPoolNamesFromIDs(ctx, poolIDs)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return err
		}

		for _, poolName := range poolNames {
			pool, err := storagePools.LoadByName(s, poolName)
			if err != nil {
				return fmt.Errorf("Error loading storage pool %q to delete image %q: %w", poolName, fingerprint, err)
			}

			// Only perform the deletion of remote volumes on the server handling the request.
			if !isClusterNotification || !pool.Driver().Info().Remote {
				err = pool.DeleteImage(fingerprint, op)
				if err != nil {
					return fmt.Errorf("Error deleting image %q from storage pool %q: %w", fingerprint, pool.Name(), err)
				}
			}
		}

		// Remove main image file from disk.
		err = imageDeleteFromDisk(s.LocalConfig.StorageImagesVolume(requestProjectName), fingerprint)
		if err != nil {
			return err
		}

		// Remove the database entry.
		if !isClusterNotification {
			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.DeleteImage(ctx, imageID)
			})
			if err != nil {
				return fmt.Errorf("Error deleting image info from the database: %w", err)
			}
		}

		s.Events.SendLifecycle(requestProjectName, lifecycle.ImageDeleted.Event(fingerprint, requestProjectName, op.EventLifecycleRequestor(), nil))

		return nil
	}

	args := operations.OperationArgs{
		ProjectName: requestProjectName,
		EntityURL:   api.NewURL().Path(version.APIVersion, "images", fingerprint).Project(effectiveProjectName),
		Type:        operationtype.ImageDelete,
		Class:       operations.OperationClassTask,
		RunHook:     do,
	}

	op, err := opCreator(s, args)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// imageDeleteFromDisk removes the main image file and rootfs file of an image.
func imageDeleteFromDisk(daemonStorageVolume string, fingerprint string) error {
	// Remove main image file.
	fname := filepath.Join(daemonStoragePath(daemonStorageVolume, config.DaemonStorageTypeImages), fingerprint)
	if shared.PathExists(fname) {
		err := os.Remove(fname)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("Error deleting image file %s: %s", fname, err)
		}
	}

	// Remove the rootfs file for the image.
	fname = fname + ".rootfs"
	if shared.PathExists(fname) {
		err := os.Remove(fname)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("Error deleting image file %s: %s", fname, err)
		}
	}

	return nil
}

func doImageGet(ctx context.Context, tx *db.ClusterTx, project, fingerprint string, public bool) (*api.Image, error) {
	filter := dbCluster.ImageFilter{Project: &project}
	if public {
		filter.Public = &public
	}

	_, imgInfo, err := tx.GetImageByFingerprintPrefix(ctx, fingerprint, filter)
	if err != nil {
		return nil, err
	}

	return imgInfo, nil
}

// imageValidSecret searches for an image upload or download token operation running on any member in the given
// project that has a matching fingerprint and secret in its metadata.
// If an operation is found it is returned and the operation is cancelled. Otherwise nil is returned if not found.
func imageValidSecret(s *state.State, r *http.Request, projectName string, fingerprint string, secret string, tokenType operationtype.Type) (*api.Operation, error) {
	ops, err := operationsGetByType(r.Context(), s, projectName, tokenType)
	if err != nil {
		return nil, fmt.Errorf("Failed getting image token operations: %w", err)
	}

	for _, op := range ops {
		if op.Metadata == nil {
			continue
		}

		if op.Metadata["fingerprint"] != fingerprint {
			continue
		}

		opSecret, ok := op.Metadata["secret"]
		if !ok {
			continue
		}

		// Assert opSecret is a string then convert to []byte for constant time comparison.
		opSecretStr, ok := opSecret.(string)
		if !ok {
			continue
		}

		if subtle.ConstantTimeCompare([]byte(opSecretStr), []byte(secret)) == 1 {
			// Token is single-use, so cancel it now.
			err = operationCancel(r.Context(), s, projectName, op)
			if err != nil {
				return nil, fmt.Errorf("Failed to cancel operation %q: %w", op.ID, err)
			}

			return op, nil
		}
	}

	return nil, nil
}

// swagger:operation GET /1.0/images/{fingerprint}?public images image_get_untrusted
//
//  Get the public image
//
//  Gets a specific public image.
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: secret
//      description: Secret token to retrieve a private image
//      type: string
//      example: RANDOM-STRING
//  responses:
//    "200":
//      description: Image
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            $ref: "#/definitions/Image"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/images/{fingerprint} images image_get
//
//	Get the image
//
//	Gets a specific image.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: Image
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/Image"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	fingerprint, err := url.PathUnescape(mux.Vars(r)["fingerprint"])
	if err != nil {
		return response.SmartError(err)
	}

	err = validateImageFingerprintPrefix(fingerprint)
	if err != nil {
		return response.SmartError(err)
	}

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeImage, false)
	if err != nil {
		return response.SmartError(err)
	}

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	trusted := requestor.IsTrusted()
	secret := r.FormValue("secret")

	// Unauthenticated clients that do not provide a secret may only view public images.
	publicOnly := !trusted && secret == ""

	// Get the image. We need to do this before the permission check because the URL in the permission check will not
	// work with partial fingerprints.
	var info *api.Image
	effectiveProjectName := projectName
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		effectiveProjectName, err = projectutils.ImageProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		info, err = doImageGet(ctx, tx, projectName, fingerprint, publicOnly)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil && api.StatusErrorCheck(err, http.StatusNotFound) {
		// Return a generic not found. This is so that the caller cannot determine the existence of an image by the
		// contents of the error message.
		return response.NotFound(nil)
	} else if err != nil {
		return response.SmartError(err)
	}

	// Access check.
	var userCanViewImage bool
	if secret != "" {
		// If a secret was provided, validate it regardless of whether the image is public or the caller has sufficient
		// privilege. This is to ensure the image token operation is cancelled.
		op, err := imageValidSecret(s, r, projectName, info.Fingerprint, secret, operationtype.ImageDownloadToken)
		if err != nil {
			return response.SmartError(err)
		}

		// If an operation was found the caller has access, otherwise continue to other access checks.
		if op != nil {
			userCanViewImage = true
		}
	}

	// No operation found for the secret. Perform other access checks.
	if !userCanViewImage {
		if info.Public {
			// If the image is public any client can view it.
			userCanViewImage = true
		} else {
			// Otherwise perform an access check with the full image fingerprint.
			request.SetContextValue(r, request.CtxEffectiveProjectName, effectiveProjectName)
			err = s.Authorizer.CheckPermission(r.Context(), entity.ImageURL(projectName, info.Fingerprint), auth.EntitlementCanView)
			if err != nil && !auth.IsDeniedError(err) {
				return response.SmartError(err)
			} else if err == nil {
				userCanViewImage = true
			}
		}
	}

	// If the client still cannot view the image, return a generic not found error.
	if !userCanViewImage {
		return response.NotFound(nil)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeImage, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.ImageURL(projectName, fingerprint): info})
		if err != nil {
			return response.SmartError(err)
		}
	}

	etag := []any{info.Public, info.AutoUpdate, info.Properties}
	return response.SyncResponseETag(true, info, etag)
}

// swagger:operation PUT /1.0/images/{fingerprint} images image_put
//
//	Update the image
//
//	Updates the entire image definition.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: image
//	    description: Image configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImagePut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imagePut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get current value
	projectName := request.ProjectParam(r)
	details, err := request.GetContextValue[imageDetails](r.Context(), ctxImageDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate ETag
	etag := []any{details.image.Public, details.image.AutoUpdate, details.image.Properties}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.ImagePut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get ExpiresAt
	if !req.ExpiresAt.IsZero() {
		details.image.ExpiresAt = req.ExpiresAt
	}

	// Get profile IDs
	if req.Profiles == nil {
		req.Profiles = []string{"default"}
	}

	profileIDs := make([]int64, len(req.Profiles))

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectConfig, err := dbCluster.GetProjectConfig(ctx, tx.Tx(), projectName)
		if err != nil {
			return fmt.Errorf("Failed loading config for project %q: %w", projectName, err)
		}

		// Project to associate image with.
		imageProject := projectName

		// If "features.images" is disabled for the project, associate the image with the "default" project.
		if shared.IsFalseOrEmpty(projectConfig["features.images"]) {
			imageProject = api.ProjectDefaultName
		}

		// Project to associate profiles with.
		profileProject := projectName

		// If "features.profiles" is disabled for the project, associate the profiles with the "default" project.
		if shared.IsFalseOrEmpty(projectConfig["features.profiles"]) {
			profileProject = api.ProjectDefaultName
		}

		for i, profile := range req.Profiles {
			profileID, _, err := tx.GetProfile(ctx, profileProject, profile)
			if response.IsNotFoundError(err) {
				return fmt.Errorf("Profile %q doesn't exist", profile)
			} else if err != nil {
				return err
			}

			profileIDs[i] = profileID
		}

		// Handle the case when an image is being updated in the default project.
		if imageProject == api.ProjectDefaultName {
			// Find IDs of profiles that have matching names in projects with "features.images=false".
			otherProfileIDs, err := resolveProfileIDs(ctx, tx, profileProject, req.Profiles)
			if err != nil {
				return err
			}

			profileIDs = append(profileIDs, otherProfileIDs...)
		}

		return tx.UpdateImage(ctx, details.imageID, details.image.Filename, details.image.Size, req.Public, req.AutoUpdate, details.image.Architecture, details.image.CreatedAt, details.image.ExpiresAt, req.Properties, profileIDs)
	})
	if err != nil {
		if response.IsNotFoundError(err) {
			return response.BadRequest(err)
		}

		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(projectName, lifecycle.ImageUpdated.Event(details.image.Fingerprint, projectName, requestor, nil))

	return response.EmptySyncResponse
}

// swagger:operation PATCH /1.0/images/{fingerprint} images image_patch
//
//	Partially update the image
//
//	Updates a subset of the image definition.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: image
//	    description: Image configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImagePut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imagePatch(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get current value
	projectName := request.ProjectParam(r)
	details, err := request.GetContextValue[imageDetails](r.Context(), ctxImageDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate ETag
	etag := []any{details.image.Public, details.image.AutoUpdate, details.image.Properties}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return response.InternalError(err)
	}

	rdr1 := io.NopCloser(bytes.NewBuffer(body))
	rdr2 := io.NopCloser(bytes.NewBuffer(body))

	reqRaw := shared.Jmap{}
	err = json.NewDecoder(rdr1).Decode(&reqRaw)
	if err != nil {
		return response.BadRequest(err)
	}

	req := api.ImagePut{}
	err = json.NewDecoder(rdr2).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get AutoUpdate
	autoUpdate, err := reqRaw.GetBool("auto_update")
	if err == nil {
		details.image.AutoUpdate = autoUpdate
	}

	// Get Public
	public, err := reqRaw.GetBool("public")
	if err == nil {
		details.image.Public = public
	}

	// Get Properties
	_, ok := reqRaw["properties"]
	if ok {
		properties := req.Properties
		for k, v := range details.image.Properties {
			_, ok := req.Properties[k]
			if !ok {
				properties[k] = v
			}
		}

		details.image.Properties = properties
	}

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpdateImage(ctx, details.imageID, details.image.Filename, details.image.Size, details.image.Public, details.image.AutoUpdate, details.image.Architecture, details.image.CreatedAt, details.image.ExpiresAt, details.image.Properties, nil)
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(projectName, lifecycle.ImageUpdated.Event(details.image.Fingerprint, projectName, requestor, nil))

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/images/aliases images images_aliases_post
//
//	Add an image alias
//
//	Creates a new image alias.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: image alias
//	    description: Image alias
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImageAliasesPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageAliasesPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	req := api.ImageAliasesPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Name == "" || req.Target == "" {
		return response.BadRequest(errors.New("name and target are required"))
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// This is just to see if the alias name already exists.
		_, _, err = tx.GetImageAlias(ctx, projectName, req.Name, true)
		if !response.IsNotFoundError(err) {
			if err != nil {
				return err
			}

			return api.StatusErrorf(http.StatusConflict, "Alias %q already exists", req.Name)
		}

		imgID, _, err := tx.GetImageByFingerprintPrefix(ctx, req.Target, dbCluster.ImageFilter{Project: &projectName})
		if err != nil {
			return err
		}

		err = tx.CreateImageAlias(ctx, projectName, req.Name, imgID, req.Description)
		if err != nil {
			return err
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())
	lc := lifecycle.ImageAliasCreated.Event(req.Name, projectName, requestor, logger.Ctx{"target": req.Target})
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation GET /1.0/images/aliases images images_aliases_get
//
//  Get the image aliases
//
//  Returns a list of image aliases (URLs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/images/aliases/foo",
//                "/1.0/images/aliases/bar1"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/images/aliases?recursion=1 images images_aliases_get_recursion1
//
//	Get the image aliases
//
//	Returns a list of image aliases (structs).
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of image aliases
//	          items:
//	            $ref: "#/definitions/ImageAliasesEntry"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageAliasesGet(d *Daemon, r *http.Request) response.Response {
	recursion, _ := util.IsRecursionRequest(r)

	s := d.State()

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeImageAlias, true)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	var effectiveProjectName string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		effectiveProjectName, err = projectutils.ImageProject(ctx, tx.Tx(), projectName)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	request.SetContextValue(r, request.CtxEffectiveProjectName, effectiveProjectName)
	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), auth.EntitlementCanView, entity.TypeImageAlias)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to get a permission checker: %w", err))
	}

	var responseStr []string
	var responseMap []*api.ImageAliasesEntry
	urlToImageAlias := make(map[*api.URL]auth.EntitlementReporter)
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		names, err := tx.GetImageAliases(ctx, projectName)
		if err != nil {
			return err
		}

		if recursion > 0 {
			responseMap = make([]*api.ImageAliasesEntry, 0, len(names))
		} else {
			responseStr = make([]string, 0, len(names))
		}

		for _, name := range names {
			if !userHasPermission(entity.ImageAliasURL(projectName, name)) {
				continue
			}

			if recursion == 0 {
				responseStr = append(responseStr, api.NewURL().Path(version.APIVersion, "images", "aliases", name).String())
			} else {
				_, alias, err := tx.GetImageAlias(ctx, projectName, name, true)
				if err != nil {
					continue
				}

				responseMap = append(responseMap, &alias)
				urlToImageAlias[entity.ImageAliasURL(projectName, name)] = &alias
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if recursion == 0 {
		return response.SyncResponse(true, responseStr)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeImageAlias, withEntitlements, urlToImageAlias)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponse(true, responseMap)
}

// swagger:operation GET /1.0/images/aliases/{name}?public images image_alias_get_untrusted
//
//  Get the public image alias
//
//  Gets a specific public image alias.
//  This untrusted endpoint only works for aliases pointing to public images.
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//  responses:
//    "200":
//      description: Image alias
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            $ref: "#/definitions/ImageAliasesEntry"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/images/aliases/{name} images image_alias_get
//
//	Get the image alias
//
//	Gets a specific image alias.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: Image alias
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/ImageAliasesEntry"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageAliasGet(d *Daemon, r *http.Request) response.Response {
	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	s := d.State()

	withEntitlements, err := extractEntitlementsFromQuery(r, entity.TypeImageAlias, false)
	if err != nil {
		return response.SmartError(err)
	}

	var effectiveProjectName string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		effectiveProjectName, err = projectutils.ImageProject(ctx, tx.Tx(), projectName)
		return err
	})

	// Set `userCanViewImageAlias` to true only when the caller is authenticated and can view the alias.
	// We don't abort the request if this is false because the image alias may be for a public image.
	var userCanViewImageAlias bool
	request.SetContextValue(r, request.CtxEffectiveProjectName, effectiveProjectName)
	err = s.Authorizer.CheckPermission(r.Context(), entity.ImageAliasURL(projectName, name), auth.EntitlementCanView)
	if err != nil && !auth.IsDeniedError(err) {
		return response.SmartError(err)
	} else if err == nil {
		userCanViewImageAlias = true
	}

	var alias api.ImageAliasesEntry
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// If `userCanViewImageAlias` is false, the query will be restricted to public images only.
		_, alias, err = tx.GetImageAlias(ctx, projectName, name, userCanViewImageAlias)

		return err
	})
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return response.SmartError(err)
	} else if err != nil {
		// Return a generic not found error.
		return response.NotFound(nil)
	}

	if len(withEntitlements) > 0 {
		err = reportEntitlements(r.Context(), s.Authorizer, entity.TypeImageAlias, withEntitlements, map[*api.URL]auth.EntitlementReporter{entity.ImageAliasURL(projectName, name): &alias})
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponseETag(true, &alias, alias)
}

// swagger:operation DELETE /1.0/images/aliases/{name} images image_alias_delete
//
//	Delete the image alias
//
//	Deletes a specific image alias.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageAliasDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, _, err = tx.GetImageAlias(ctx, projectName, name, true)
		if err != nil {
			return err
		}

		err = tx.DeleteImageAlias(ctx, projectName, name)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(projectName, lifecycle.ImageAliasDeleted.Event(name, projectName, requestor, nil))

	return response.EmptySyncResponse
}

// swagger:operation PUT /1.0/images/aliases/{name} images images_aliases_put
//
//	Update the image alias
//
//	Updates the entire image alias configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: image alias
//	    description: Image alias configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImageAliasesEntryPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageAliasPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get current value
	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.ImageAliasesEntryPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Target == "" {
		return response.BadRequest(errors.New("The target field is required"))
	}

	var imgAlias api.ImageAliasesEntry
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var imgAliasID int

		imgAliasID, imgAlias, err = tx.GetImageAlias(ctx, projectName, name, true)
		if err != nil {
			return err
		}

		// Validate ETag
		err = util.EtagCheck(r, imgAlias)
		if err != nil {
			return err
		}

		imageID, _, err := tx.GetImageByFingerprintPrefix(ctx, req.Target, dbCluster.ImageFilter{Project: &projectName})
		if err != nil {
			return err
		}

		err = tx.UpdateImageAlias(ctx, imgAliasID, imageID, req.Description)
		if err != nil {
			return err
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(projectName, lifecycle.ImageAliasUpdated.Event(imgAlias.Name, projectName, requestor, logger.Ctx{"target": req.Target}))

	return response.EmptySyncResponse
}

// swagger:operation PATCH /1.0/images/aliases/{name} images images_alias_patch
//
//	Partially update the image alias
//
//	Updates a subset of the image alias configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: image alias
//	    description: Image alias configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImageAliasesEntryPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageAliasPatch(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get current value
	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	req := shared.Jmap{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	var imgAlias api.ImageAliasesEntry
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var imgAliasID int
		imgAliasID, imgAlias, err = tx.GetImageAlias(ctx, projectName, name, true)
		if err != nil {
			return err
		}

		// Validate ETag
		err = util.EtagCheck(r, imgAlias)
		if err != nil {
			return err
		}

		_, ok := req["target"]
		if ok {
			target, err := req.GetString("target")
			if err != nil {
				return api.StatusErrorf(http.StatusBadRequest, "%w", err)
			}

			imgAlias.Target = target
		}

		_, ok = req["description"]
		if ok {
			description, err := req.GetString("description")
			if err != nil {
				return api.StatusErrorf(http.StatusBadRequest, "%w", err)
			}

			imgAlias.Description = description
		}

		imageID, _, err := tx.GetImage(ctx, imgAlias.Target, dbCluster.ImageFilter{Project: &projectName})
		if err != nil {
			return err
		}

		err = tx.UpdateImageAlias(ctx, imgAliasID, imageID, imgAlias.Description)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())
	s.Events.SendLifecycle(projectName, lifecycle.ImageAliasUpdated.Event(imgAlias.Name, projectName, requestor, logger.Ctx{"target": imgAlias.Target}))

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/images/aliases/{name} images images_alias_post
//
//	Rename the image alias
//
//	Renames an existing image alias.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: image alias
//	    description: Image alias rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImageAliasesEntryPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageAliasPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.ImageAliasesEntryPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// This is just to see if the alias name already exists.
		_, _, err := tx.GetImageAlias(ctx, projectName, req.Name, true)
		if !response.IsNotFoundError(err) {
			if err != nil {
				return err
			}

			return api.StatusErrorf(http.StatusConflict, "Alias %q already exists", req.Name)
		}

		imgAliasID, _, err := tx.GetImageAlias(ctx, projectName, name, true)
		if err != nil {
			return err
		}

		return tx.RenameImageAlias(ctx, imgAliasID, req.Name)
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r.Context())
	lc := lifecycle.ImageAliasRenamed.Event(req.Name, projectName, requestor, logger.Ctx{"old_name": name})
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation GET /1.0/images/{fingerprint}/export?public images image_export_get_untrusted
//
//  Get the raw image file(s)
//
//  Download the raw image file(s) of a public image from the server.
//  If the image is in split format, a multipart http transfer occurs.
//
//  ---
//  produces:
//    - application/octet-stream
//    - multipart/form-data
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: secret
//      description: Secret token to retrieve a private image
//      type: string
//      example: RANDOM-STRING
//  responses:
//    "200":
//      description: Raw image data
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/images/{fingerprint}/export images image_export_get
//
//	Get the raw image file(s)
//
//	Download the raw image file(s) from the server.
//	If the image is in split format, a multipart http transfer occurs.
//
//	---
//	produces:
//	  - application/octet-stream
//	  - multipart/form-data
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: Raw image data
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageExport(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	fingerprint, err := url.PathUnescape(mux.Vars(r)["fingerprint"])
	if err != nil {
		return response.SmartError(err)
	}

	err = validateImageFingerprintPrefix(fingerprint)
	if err != nil {
		return response.SmartError(err)
	}

	// Verify the auth method in the request context to determine if the request comes from the /dev/lxd socket.
	secret := r.FormValue("secret")

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	trusted := requestor.IsTrusted()

	// Unauthenticated remote clients that do not provide a secret may only view public images.
	// For devlxd, we allow querying for private images. We'll subsequently perform additional access checks.
	publicOnly := !trusted && secret == ""

	// Get the image. We need to do this before the permission check because the URL in the permission check will not
	// work with partial fingerprints.
	var imgInfo *api.Image
	effectiveProjectName := projectName
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		effectiveProjectName, err = projectutils.ImageProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		filter := dbCluster.ImageFilter{Project: &projectName}
		if publicOnly {
			filter.Public = &publicOnly
		}

		_, imgInfo, err = tx.GetImage(ctx, fingerprint, filter)
		return err
	})
	if err != nil && api.StatusErrorCheck(err, http.StatusNotFound) {
		// Return a generic not found. This is so that the caller cannot determine the existence of an image by the
		// contents of the error message.
		return response.NotFound(nil)
	} else if err != nil {
		return response.SmartError(err)
	}

	// Access control.
	var userCanViewImage bool
	if secret != "" {
		// If a secret was provided, validate it regardless of whether the image is public or the caller has sufficient
		// privilege. This is to ensure the image token operation is cancelled.
		op, err := imageValidSecret(s, r, projectName, imgInfo.Fingerprint, secret, operationtype.ImageDownloadToken)
		if err != nil {
			return response.SmartError(err)
		}

		// If an operation was found the caller has access, otherwise continue to other access checks.
		if op != nil {
			userCanViewImage = true
		}
	}

	if !userCanViewImage {
		if imgInfo.Public {
			// If the image is public any client can view it.
			userCanViewImage = true
		} else {
			// Otherwise perform an access check with the full image fingerprint.
			request.SetContextValue(r, request.CtxEffectiveProjectName, effectiveProjectName)
			err = s.Authorizer.CheckPermission(r.Context(), entity.ImageURL(projectName, imgInfo.Fingerprint), auth.EntitlementCanView)
			if err != nil && !auth.IsDeniedError(err) {
				return response.SmartError(err)
			} else if err == nil {
				userCanViewImage = true
			}
		}
	}

	// If the client still cannot view the image, return a generic not found error.
	if !userCanViewImage {
		return response.NotFound(nil)
	}

	return imageExportFiles(r.Context(), s, imgInfo, projectName)
}

// imageExportFiles returns the [response.FileResponse] for the specified image files, the image can be local or remote.
func imageExportFiles(ctx context.Context, s *state.State, imgInfo *api.Image, requestProjectName string) response.Response {
	var address string
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Check if the image is only available on another node.
		var err error
		address, err = tx.LocateImage(ctx, imgInfo.Fingerprint)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	if address != "" {
		// Forward the request to the other node
		client, err := cluster.Connect(ctx, address, s.Endpoints.NetworkCert(), s.ServerCert(), false)
		if err != nil {
			return response.SmartError(err)
		}

		return response.ForwardedResponse(client)
	}

	imagePath := filepath.Join(s.ImagesStoragePath(requestProjectName), imgInfo.Fingerprint)
	rootfsPath := imagePath + ".rootfs"

	_, ext, _, err := shared.DetectCompression(imagePath)
	if err != nil {
		ext = ""
	}

	filename := imgInfo.Fingerprint + ext

	if shared.PathExists(rootfsPath) {
		files := make([]response.FileResponseEntry, 2)

		files[0].Identifier = "metadata"
		files[0].Path = imagePath
		files[0].Filename = "meta-" + filename

		// Recompute the extension for the root filesystem, it may use a different
		// compression algorithm than the metadata.
		_, ext, _, err = shared.DetectCompression(rootfsPath)
		if err != nil {
			ext = ""
		}

		filename = imgInfo.Fingerprint + ext

		if imgInfo.Type == "virtual-machine" {
			files[1].Identifier = "rootfs.img"
		} else {
			files[1].Identifier = "rootfs"
		}

		files[1].Path = rootfsPath
		files[1].Filename = filename

		requestor := request.CreateRequestor(ctx)
		s.Events.SendLifecycle(requestProjectName, lifecycle.ImageRetrieved.Event(imgInfo.Fingerprint, requestProjectName, requestor, nil))

		return response.FileResponse(files, nil)
	}

	files := make([]response.FileResponseEntry, 1)
	files[0].Identifier = filename
	files[0].Path = imagePath
	files[0].Filename = filename

	requestor := request.CreateRequestor(ctx)
	s.Events.SendLifecycle(requestProjectName, lifecycle.ImageRetrieved.Event(imgInfo.Fingerprint, requestProjectName, requestor, nil))

	return response.FileResponse(files, nil)
}

// swagger:operation POST /1.0/images/{fingerprint}/export images images_export_post
//
//	Make LXD push the image to a remote server
//
//	Gets LXD to connect to a remote server and push the image to it.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: image
//	    description: Image push request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImageExportPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageExportPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	details, err := request.GetContextValue[imageDetails](r.Context(), ctxImageDetails)
	if err != nil {
		return response.SmartError(err)
	}

	req := api.ImageExportPost{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		response.SmartError(err)
	}

	// Connect to the target and push the image
	args := &lxd.ConnectionArgs{
		TLSServerCert: req.Certificate,
		UserAgent:     version.UserAgent,
		Proxy:         s.Proxy,
		CachePath:     s.OS.CacheDir,
		CacheExpiry:   time.Hour,
	}

	// Setup LXD client
	remote, err := lxd.ConnectLXD(req.Target, args)
	if err != nil {
		return response.SmartError(err)
	}

	var imageCreateOp lxd.Operation

	run := func(ctx context.Context, op *operations.Operation) error {
		createArgs := &lxd.ImageCreateArgs{}
		imageMetaPath := filepath.Join(s.ImagesStoragePath(projectName), details.imageFingerprintPrefix)
		imageRootfsPath := imageMetaPath + ".rootfs"

		metaFile, err := os.Open(imageMetaPath)
		if err != nil {
			return err
		}

		defer func() { _ = metaFile.Close() }()

		createArgs.MetaFile = metaFile
		createArgs.MetaName = filepath.Base(imageMetaPath)

		if shared.PathExists(imageRootfsPath) {
			rootfsFile, err := os.Open(imageRootfsPath)
			if err != nil {
				return err
			}

			defer func() { _ = rootfsFile.Close() }()

			createArgs.RootfsFile = rootfsFile
			createArgs.RootfsName = filepath.Base(imageRootfsPath)
		}

		image := api.ImagesPost{
			Filename: createArgs.MetaName,
			Source: &api.ImagesPostSource{
				Fingerprint: details.imageFingerprintPrefix,
				Secret:      req.Secret,
				Mode:        "push",
			},
			ImagePut: api.ImagePut{
				Profiles: req.Profiles,
			},
		}

		if req.Project != "" {
			remote = remote.UseProject(req.Project)
		}

		imageCreateOp, err = remote.CreateImage(image, createArgs)
		if err != nil {
			return err
		}

		opAPI := imageCreateOp.Get()

		var secret string

		val, ok := opAPI.Metadata["secret"]
		if ok {
			secret, ok = val.(string)
			if !ok {
				return errors.New("Invalid type for field \"secret\"")
			}
		}

		opWaitAPI, _, err := remote.GetOperationWaitSecret(opAPI.ID, secret, -1)
		if err != nil {
			return err
		}

		if opWaitAPI.StatusCode != api.Success {
			return fmt.Errorf("Failed operation %q: %q", opWaitAPI.Status, opWaitAPI.Err)
		}

		s.Events.SendLifecycle(projectName, lifecycle.ImageRetrieved.Event(details.imageFingerprintPrefix, projectName, op.EventLifecycleRequestor(), logger.Ctx{"target": req.Target}))

		return nil
	}

	opArgs := operations.OperationArgs{
		ProjectName: projectName,
		EntityURL:   api.NewURL().Path(version.APIVersion, "images", details.image.Fingerprint).Project(details.image.Project),
		Type:        operationtype.ImageUpload,
		Class:       operations.OperationClassTask,
		RunHook:     run,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, opArgs)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation POST /1.0/images/{fingerprint}/secret images images_secret_post
//
//	Generate secret for retrieval of the image by an untrusted client
//
//	This generates a background operation including a secret one time key
//	in its metadata which can be used to fetch this image from an untrusted
//	client.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageSecret(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	details, err := request.GetContextValue[imageDetails](r.Context(), ctxImageDetails)
	if err != nil {
		return response.SmartError(err)
	}

	return createImageTokenResponse(s, r, projectName, details.image.Fingerprint, nil, operationtype.ImageDownloadToken)
}

func imageImportFromNode(imagesDir string, client lxd.InstanceServer, fingerprint string) error {
	// Prepare the temp files
	buildDir, err := os.MkdirTemp(imagesDir, "lxd_build_")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory for download: %w", err)
	}

	defer func() { _ = os.RemoveAll(buildDir) }()

	metaFile, err := os.CreateTemp(buildDir, "lxd_tar_")
	if err != nil {
		return err
	}

	defer func() { _ = metaFile.Close() }()

	rootfsFile, err := os.CreateTemp(buildDir, "lxd_tar_")
	if err != nil {
		return err
	}

	defer func() { _ = rootfsFile.Close() }()

	getReq := lxd.ImageFileRequest{
		MetaFile:   io.WriteSeeker(metaFile),
		RootfsFile: io.WriteSeeker(rootfsFile),
	}

	getResp, err := client.GetImageFile(fingerprint, getReq)
	if err != nil {
		return err
	}

	// Truncate down to size
	if getResp.RootfsSize > 0 {
		err = rootfsFile.Truncate(getResp.RootfsSize)
		if err != nil {
			return err
		}
	}

	err = metaFile.Truncate(getResp.MetaSize)
	if err != nil {
		return err
	}

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

// swagger:operation POST /1.0/images/{fingerprint}/refresh images images_refresh_post
//
//	Refresh an image
//
//	This causes LXD to check the image source server for an updated
//	version of the image and if available to refresh the local copy with the
//	new version.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageRefresh(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	details, err := request.GetContextValue[imageDetails](r.Context(), ctxImageDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Begin background operation
	run := func(ctx context.Context, op *operations.Operation) error {
		var nodes []db.NodeInfo

		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			nodeAddresses, err := tx.GetNodesWithImageAndAutoUpdate(ctx, details.imageFingerprintPrefix, true)
			if err != nil {
				return fmt.Errorf("Failed getting cluster members with auto-update images: %w", err)
			}

			for _, nodeAddress := range nodeAddresses {
				nodeInfo, err := tx.GetNodeByAddress(ctx, nodeAddress)
				if err != nil {
					return fmt.Errorf("Failed retrieving cluster member information for %q: %w", nodeAddress, err)
				}

				nodes = append(nodes, nodeInfo)
			}

			return nil
		})
		if err != nil {
			return fmt.Errorf("Error getting cluster members for refreshing image %q in project %q: %w", details.imageFingerprintPrefix, projectName, err)
		}

		newImage, err := autoUpdateImage(ctx, s, op, details.imageID, &details.image, projectName, true)
		if err != nil {
			return fmt.Errorf("Failed to update image %q in project %q: %w", details.imageFingerprintPrefix, projectName, err)
		}

		if newImage != nil {
			if len(nodes) > 1 {
				err := distributeImage(ctx, s, nodes, details.imageFingerprintPrefix, newImage)
				if err != nil {
					return fmt.Errorf("Failed to distribute new image %q: %w", newImage.Fingerprint, err)
				}
			}

			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				// Remove the database entry for the image after distributing to cluster members.
				return tx.DeleteImage(ctx, details.imageID)
			})
			if err != nil {
				logger.Error("Error deleting old image from database", logger.Ctx{"err": err, "fingerprint": details.imageFingerprintPrefix, "ID": details.imageID})
			}
		}

		return err
	}

	args := operations.OperationArgs{
		ProjectName: projectName,
		EntityURL:   api.NewURL().Path(version.APIVersion, "images", details.image.Fingerprint).Project(details.image.Project),
		Type:        operationtype.ImageRefresh,
		Class:       operations.OperationClassTask,
		RunHook:     run,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func autoSyncImagesTask(stateFunc func() *state.State) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := stateFunc()

		leaderInfo, err := s.LeaderInfo()
		if err != nil {
			logger.Error("Failed to get leader cluster member address", logger.Ctx{"err": err})
			return
		}

		if !leaderInfo.Clustered {
			return
		}

		// In order to only have one task operation executed per image when syncing the images
		// across the cluster, only leader node can launch the task, no others.
		if !leaderInfo.Leader {
			logger.Debug("Skipping image synchronization task since we're not leader")
			return
		}

		opRun := func(ctx context.Context, op *operations.Operation) error {
			return autoSyncImages(ctx, s)
		}

		args := operations.OperationArgs{
			Type:    operationtype.ImagesSynchronize,
			Class:   operations.OperationClassTask,
			RunHook: opRun,
		}

		op, err := operations.ScheduleServerOperation(s, args)
		if err != nil {
			logger.Error("Failed creating image synchronization operation", logger.Ctx{"err": err})
			return
		}

		logger.Debug("Acquiring image task lock")
		imageTaskMu.Lock()
		defer imageTaskMu.Unlock()
		logger.Debug("Acquired image task lock")

		logger.Info("Synchronizing images across the cluster")
		err = op.Start()
		if err != nil {
			logger.Error("Failed starting image synchronization operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed synchronizing images", logger.Ctx{"err": err})
			return
		}

		logger.Info("Done synchronizing images across the cluster")
	}

	return f, task.Hourly()
}

func autoSyncImages(ctx context.Context, s *state.State) error {
	var imageProjectInfo map[string][]string

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get all images.
		imageProjectInfo, err = tx.GetImages(ctx)

		return err
	})
	if err != nil {
		return fmt.Errorf("Failed to query image fingerprints: %w", err)
	}

	for fingerprint, projects := range imageProjectInfo {
		ch := make(chan error)
		go func(projectName string, fingerprint string) {
			err := imageSyncBetweenNodes(ctx, s, nil, projectName, fingerprint)
			if err != nil {
				logger.Error("Failed to synchronize images", logger.Ctx{"err": err, "project": projectName, "fingerprint": fingerprint})
			}

			ch <- nil
		}(projects[0], fingerprint)

		select {
		case <-ctx.Done():
			return nil
		case <-ch:
		}
	}

	return nil
}

func imageSyncBetweenNodes(ctx context.Context, s *state.State, r *http.Request, project string, fingerprint string) error {
	logger.Info("Syncing image to members started", logger.Ctx{"fingerprint": fingerprint, "project": project})
	defer logger.Info("Syncing image to members finished", logger.Ctx{"fingerprint": fingerprint, "project": project})

	var desiredSyncNodeCount int64
	var syncNodeAddresses []string

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		desiredSyncNodeCount = s.GlobalConfig.ImagesMinimalReplica()

		// -1 means that we want to replicate the image on all nodes
		if desiredSyncNodeCount == -1 {
			nodesCount, err := tx.GetNodesCount(ctx)
			if err != nil {
				return fmt.Errorf("Failed to get the number of nodes: %w", err)
			}

			desiredSyncNodeCount = int64(nodesCount)
		}

		var err error

		// Check how many nodes already have this image
		syncNodeAddresses, err = tx.GetNodesWithImage(ctx, fingerprint)
		if err != nil {
			return fmt.Errorf("Failed to get nodes for the image synchronization: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	// If none of the nodes have the image, there's nothing to sync.
	if len(syncNodeAddresses) == 0 {
		logger.Info("No members have image, nothing to do", logger.Ctx{"fingerprint": fingerprint, "project": project})
		return nil
	}

	nodeCount := desiredSyncNodeCount - int64(len(syncNodeAddresses))
	if nodeCount <= 0 {
		logger.Info("Sufficient members have image", logger.Ctx{"fingerprint": fingerprint, "project": project, "desiredSyncCount": desiredSyncNodeCount, "syncedCount": len(syncNodeAddresses)})
		return nil
	}

	// Pick a random node from that slice as the source.
	syncNodeAddress := syncNodeAddresses[rand.Intn(len(syncNodeAddresses))]

	reqContext := context.Background()
	if r != nil {
		reqContext = r.Context()
	}

	source, err := cluster.Connect(reqContext, syncNodeAddress, s.Endpoints.NetworkCert(), s.ServerCert(), true)
	if err != nil {
		return fmt.Errorf("Failed to connect to source node for image synchronization: %w", err)
	}

	source = source.UseProject(project)

	var image *api.Image

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the image.
		_, image, err = tx.GetImage(ctx, fingerprint, dbCluster.ImageFilter{Project: &project})

		return err
	})
	if err != nil {
		return fmt.Errorf("Failed to get image: %w", err)
	}

	// Populate the copy arguments with properties from the source image.
	args := lxd.ImageCopyArgs{
		Type:   image.Type,
		Public: image.Public,
	}

	// Replicate on as many nodes as needed.
	for range int(nodeCount) {
		var addresses []string

		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			// Get a list of nodes that do not have the image.
			addresses, err = tx.GetNodesWithoutImage(ctx, fingerprint)

			return err
		})
		if err != nil {
			return fmt.Errorf("Failed to get nodes for the image synchronization: %w", err)
		}

		if len(addresses) <= 0 {
			logger.Info("All members have image", logger.Ctx{"fingerprint": fingerprint, "project": project})
			return nil
		}

		// Pick a random node from that slice as the target.
		targetNodeAddress := addresses[rand.Intn(len(addresses))]

		client, err := cluster.Connect(reqContext, targetNodeAddress, s.Endpoints.NetworkCert(), s.ServerCert(), true)
		if err != nil {
			return fmt.Errorf("Failed to connect node for image synchronization: %w", err)
		}

		// Select the right project.
		client = client.UseProject(project)

		// Copy the image to the target server.
		logger.Info("Copying image to member", logger.Ctx{"fingerprint": fingerprint, "address": targetNodeAddress, "project": project, "public": args.Public, "type": args.Type})
		op, err := client.CopyImage(source, *image, &args)
		if err != nil {
			return fmt.Errorf("Failed to copy image to %q: %w", targetNodeAddress, err)
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	return nil
}

func createImageTokenResponse(s *state.State, r *http.Request, projectName string, fingerprint string, metadata shared.Jmap, tokenType operationtype.Type) response.Response {
	secret, err := shared.RandomCryptoString()
	if err != nil {
		return response.InternalError(err)
	}

	meta := shared.Jmap{}

	maps.Copy(meta, metadata)

	meta["secret"] = secret
	meta["fingerprint"] = fingerprint

	// If downloading an image, the image is the primary entity.
	// If uploading an image, the project is the primary entity.
	resources := make(map[entity.Type][]api.URL)
	var entityURL *api.URL
	switch tokenType {
	case operationtype.ImageUploadToken:
		entityURL = api.NewURL().Path(version.APIVersion, "projects", projectName)
		resources[entity.TypeProject] = []api.URL{*entityURL}
	case operationtype.ImageDownloadToken:
		entityURL = api.NewURL().Path(version.APIVersion, "images", fingerprint).Project(projectName)
		resources[entity.TypeImage] = []api.URL{*entityURL}
	default:
		return response.SmartError(errors.New("Not an image token operation type"))
	}

	args := operations.OperationArgs{
		ProjectName: projectName,
		EntityURL:   entityURL,
		Type:        tokenType,
		Class:       operations.OperationClassToken,
		Resources:   resources,
		Metadata:    meta,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.ImageSecretCreated.Event(fingerprint, projectName, op.EventLifecycleRequestor(), nil))

	return operations.OperationResponse(op)
}

// resolveProfileIDs finds profile IDs in other projects matching the given names.
// It searches the "default" project and all projects where "features.images" is false.
func resolveProfileIDs(ctx context.Context, tx *db.ClusterTx, currentProject string, targetNames []string) ([]int64, error) {
	// Get the "default" project and all projects with "features.images=false".
	candidateProjects, err := dbCluster.GetProjectsSharingDefaultImages(ctx, tx.Tx())
	if err != nil {
		return nil, fmt.Errorf("Failed to load project names: %w", err)
	}

	// Filter out the current project to avoid SQL constraint violation when adding duplicate profile IDs.
	searchableProjects := make([]string, 0, len(candidateProjects))
	for _, projectName := range candidateProjects {
		if projectName != currentProject {
			searchableProjects = append(searchableProjects, projectName)
		}
	}

	// If no projects remain after filtering (e.g. currentProject is the only candidate), return early.
	if len(searchableProjects) == 0 {
		return []int64{}, nil
	}

	// Fetch matching profile IDs.
	matchingIDs, err := dbCluster.GetProfileIDsByProjectAndName(ctx, tx.Tx(), searchableProjects, targetNames)
	if err != nil {
		return nil, fmt.Errorf("Failed resolving profile IDs: %w", err)
	}

	return matchingIDs, nil
}
