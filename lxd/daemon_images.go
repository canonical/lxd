package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/lifecycle"
	"github.com/lxc/lxd/lxd/locking"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/cancel"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/units"
	"github.com/lxc/lxd/shared/version"
)

// ImageDownloadArgs used with ImageDownload.
type ImageDownloadArgs struct {
	ProjectName       string
	Server            string
	Protocol          string
	Certificate       string
	Secret            string
	Alias             string
	Type              string
	SetCached         bool
	PreferCached      bool
	AutoUpdate        bool
	Public            bool
	StoragePool       string
	Budget            int64
	SourceProjectName string
}

// imageOperationLock acquires a lock for operating on an image and returns the unlock function.
func imageOperationLock(fingerprint string) locking.UnlockFunc {
	l := logger.AddContext(logger.Log, logger.Ctx{"fingerprint": fingerprint})
	l.Debug("Acquiring lock for image")
	defer l.Debug("Lock acquired for image")

	return locking.Lock(context.TODO(), fmt.Sprintf("ImageOperation_%s", fingerprint))
}

// ImageDownload resolves the image fingerprint and if not in the database, downloads it.
func ImageDownload(r *http.Request, s *state.State, op *operations.Operation, args *ImageDownloadArgs) (*api.Image, error) {
	var err error
	var ctxMap logger.Ctx

	var remote lxd.ImageServer
	var info *api.Image

	// Default protocol is LXD. Copy so that local modifications aren't propgated to args.
	protocol := args.Protocol
	if protocol == "" {
		protocol = "lxd"
	}

	// Copy so that local modifications aren't propgated to args.
	alias := args.Alias

	// Default the fingerprint to the alias string we received
	fp := alias

	// Attempt to resolve the alias
	if shared.StringInSlice(protocol, []string{"lxd", "simplestreams"}) {
		clientArgs := &lxd.ConnectionArgs{
			TLSServerCert: args.Certificate,
			UserAgent:     version.UserAgent,
			Proxy:         s.Proxy,
			CachePath:     s.OS.CacheDir,
			CacheExpiry:   time.Hour,
		}

		if protocol == "lxd" {
			// Setup LXD client
			remote, err = lxd.ConnectPublicLXD(args.Server, clientArgs)
			if err != nil {
				return nil, fmt.Errorf("Failed to connect to LXD server %q: %w", args.Server, err)
			}

			server, ok := remote.(lxd.InstanceServer)
			if ok {
				remote = server.UseProject(args.SourceProjectName)
			}
		} else {
			// Setup simplestreams client
			remote, err = lxd.ConnectSimpleStreams(args.Server, clientArgs)
			if err != nil {
				return nil, fmt.Errorf("Failed to connect to simple streams server %q: %w", args.Server, err)
			}
		}

		// For public images, handle aliases and initial metadata
		if args.Secret == "" {
			// Look for a matching alias
			entry, _, err := remote.GetImageAliasType(args.Type, fp)
			if err == nil {
				fp = entry.Target
			}

			// Expand partial fingerprints
			info, _, err = remote.GetImage(fp)
			if err != nil {
				return nil, fmt.Errorf("Failed getting remote image info: %w", err)
			}

			fp = info.Fingerprint
		}
	}

	// Ensure we are the only ones operating on this image.
	unlock := imageOperationLock(fp)
	defer unlock()

	// If auto-update is on and we're being given the image by
	// alias, try to use a locally cached image matching the given
	// server/protocol/alias, regardless of whether it's stale or
	// not (we can assume that it will be not *too* stale since
	// auto-update is on).
	interval := s.GlobalConfig.ImagesAutoUpdateIntervalHours()

	if args.PreferCached && interval > 0 && alias != fp {
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			for _, architecture := range s.OS.Architectures {
				cachedFingerprint, err := tx.GetCachedImageSourceFingerprint(ctx, args.Server, args.Protocol, alias, args.Type, architecture)
				if err == nil && cachedFingerprint != fp {
					fp = cachedFingerprint
					break
				}
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// Check if the image already exists in this project (partial hash match).
	_, imgInfo, err := s.DB.Cluster.GetImage(fp, cluster.ImageFilter{Project: &args.ProjectName})
	if err == nil {
		// Check if the image is available locally or it's on another node.
		nodeAddress, err := s.DB.Cluster.LocateImage(imgInfo.Fingerprint)
		if err != nil {
			return nil, fmt.Errorf("Failed locating image %q in the cluster: %w", imgInfo.Fingerprint, err)
		}

		if nodeAddress != "" {
			// The image is available from another node, let's try to import it.
			err = instanceImageTransfer(s, r, args.ProjectName, imgInfo.Fingerprint, nodeAddress)
			if err != nil {
				return nil, fmt.Errorf("Failed transferring image %q from %q: %w", imgInfo.Fingerprint, nodeAddress, err)
			}

			// As the image record already exists in the project, just add the node ID to the image.
			err = s.DB.Cluster.AddImageToLocalNode(args.ProjectName, imgInfo.Fingerprint)
			if err != nil {
				return nil, fmt.Errorf("Failed adding transferred image %q to local cluster member: %w", imgInfo.Fingerprint, err)
			}
		}
	} else if response.IsNotFoundError(err) {
		// Check if the image already exists in some other project.
		_, imgInfo, err = s.DB.Cluster.GetImageFromAnyProject(fp)
		if err == nil {
			// Check if the image is available locally or it's on another node. Do this before creating
			// the missing DB record so we don't include ourself in the search results.
			nodeAddress, err := s.DB.Cluster.LocateImage(imgInfo.Fingerprint)
			if err != nil {
				return nil, fmt.Errorf("Locate image %q in the cluster: %w", imgInfo.Fingerprint, err)
			}

			// We need to insert the database entry for this project, including the node ID entry.
			err = s.DB.Cluster.CreateImage(args.ProjectName, imgInfo.Fingerprint, imgInfo.Filename, imgInfo.Size, args.Public, imgInfo.AutoUpdate, imgInfo.Architecture, imgInfo.CreatedAt, imgInfo.ExpiresAt, imgInfo.Properties, imgInfo.Type, nil)
			if err != nil {
				return nil, fmt.Errorf("Failed creating image record for project: %w", err)
			}

			// Mark the image as "cached" if downloading for an instance.
			if args.SetCached {
				err := s.DB.Cluster.SetImageCachedAndLastUseDate(args.ProjectName, imgInfo.Fingerprint, time.Now().UTC())
				if err != nil {
					return nil, fmt.Errorf("Failed setting cached flag and last use date: %w", err)
				}
			}

			var id int
			id, imgInfo, err = s.DB.Cluster.GetImage(fp, cluster.ImageFilter{Project: &args.ProjectName})
			if err != nil {
				return nil, err
			}

			err = s.DB.Cluster.CreateImageSource(id, args.Server, args.Protocol, args.Certificate, alias)
			if err != nil {
				return nil, err
			}

			// Transfer image if needed (after database record has been created above).
			if nodeAddress != "" {
				// The image is available from another node, let's try to import it.
				err = instanceImageTransfer(s, r, args.ProjectName, info.Fingerprint, nodeAddress)
				if err != nil {
					return nil, fmt.Errorf("Failed transferring image: %w", err)
				}
			}
		}
	}

	if imgInfo != nil {
		info = imgInfo
		ctxMap = logger.Ctx{"fingerprint": info.Fingerprint}
		logger.Debug("Image already exists in the DB", ctxMap)

		// If not requested in a particular pool, we're done.
		if args.StoragePool == "" {
			return info, nil
		}

		ctxMap["pool"] = args.StoragePool

		// Get the ID of the target storage pool.
		poolID, err := s.DB.Cluster.GetStoragePoolID(args.StoragePool)
		if err != nil {
			return nil, err
		}

		// Check if the image is already in the pool.
		poolIDs, err := s.DB.Cluster.GetPoolsWithImage(info.Fingerprint)
		if err != nil {
			return nil, err
		}

		if shared.Int64InSlice(poolID, poolIDs) {
			logger.Debug("Image already exists on storage pool", ctxMap)
			return info, nil
		}

		// Import the image in the pool.
		logger.Debug("Image does not exist on storage pool", ctxMap)

		err = imageCreateInPool(s, info, args.StoragePool)
		if err != nil {
			ctxMap["err"] = err
			logger.Debug("Failed to create image on storage pool", ctxMap)
			return nil, fmt.Errorf("Failed to create image %q on storage pool %q: %w", info.Fingerprint, args.StoragePool, err)
		}

		logger.Debug("Created image on storage pool", ctxMap)
		return info, nil
	}

	// Begin downloading
	if op == nil {
		ctxMap = logger.Ctx{"alias": alias, "server": args.Server}
	} else {
		ctxMap = logger.Ctx{"trigger": op.URL(), "fingerprint": fp, "operation": op.ID(), "alias": alias, "server": args.Server}
	}

	logger.Info("Downloading image", ctxMap)

	// Cleanup any leftover from a past attempt
	destDir := shared.VarPath("images")
	destName := filepath.Join(destDir, fp)

	failure := true
	cleanup := func() {
		if failure {
			_ = os.Remove(destName)
			_ = os.Remove(destName + ".rootfs")
		}
	}
	defer cleanup()

	// Setup a progress handler
	progress := func(progress ioprogress.ProgressData) {
		if op == nil {
			return
		}

		meta := op.Metadata()
		if meta == nil {
			meta = make(map[string]any)
		}

		if meta["download_progress"] != progress.Text {
			meta["download_progress"] = progress.Text
			_ = op.UpdateMetadata(meta)
		}
	}

	var canceler *cancel.HTTPRequestCanceller
	if op != nil {
		canceler = cancel.NewHTTPRequestCanceller()
		op.SetCanceler(canceler)
	}

	if protocol == "lxd" || protocol == "simplestreams" {
		// Create the target files
		dest, err := os.Create(destName)
		if err != nil {
			return nil, err
		}

		defer func() { _ = dest.Close() }()

		destRootfs, err := os.Create(destName + ".rootfs")
		if err != nil {
			return nil, err
		}

		defer func() { _ = destRootfs.Close() }()

		// Get the image information
		if info == nil {
			if args.Secret != "" {
				info, _, err = remote.GetPrivateImage(fp, args.Secret)
				if err != nil {
					return nil, err
				}

				// Expand the fingerprint now and mark alias string to match
				fp = info.Fingerprint
				alias = info.Fingerprint
			} else {
				info, _, err = remote.GetImage(fp)
				if err != nil {
					return nil, err
				}
			}
		}

		// Compatibility with older LXD servers
		if info.Type == "" {
			info.Type = "container"
		}

		if args.Budget > 0 && info.Size > args.Budget {
			return nil, fmt.Errorf("Remote image with size %d exceeds allowed bugdget of %d", info.Size, args.Budget)
		}

		// Download the image
		var resp *lxd.ImageFileResponse
		request := lxd.ImageFileRequest{
			MetaFile:        io.WriteSeeker(dest),
			RootfsFile:      io.WriteSeeker(destRootfs),
			ProgressHandler: progress,
			Canceler:        canceler,
			DeltaSourceRetriever: func(fingerprint string, file string) string {
				path := shared.VarPath("images", fmt.Sprintf("%s.%s", fingerprint, file))
				if shared.PathExists(path) {
					return path
				}

				return ""
			},
		}

		if args.Secret != "" {
			resp, err = remote.GetPrivateImageFile(fp, args.Secret, request)
		} else {
			resp, err = remote.GetImageFile(fp, request)
		}

		if err != nil {
			return nil, err
		}

		// Truncate down to size
		if resp.RootfsSize > 0 {
			err = destRootfs.Truncate(resp.RootfsSize)
			if err != nil {
				return nil, err
			}
		}

		err = dest.Truncate(resp.MetaSize)
		if err != nil {
			return nil, err
		}

		// Deal with unified images
		if resp.RootfsSize == 0 {
			err := os.Remove(destName + ".rootfs")
			if err != nil {
				return nil, err
			}
		}

		err = dest.Close()
		if err != nil {
			return nil, err
		}

		err = destRootfs.Close()
		if err != nil {
			return nil, err
		}
	} else if protocol == "direct" {
		// Setup HTTP client
		httpClient, err := util.HTTPClient(args.Certificate, s.Proxy)
		if err != nil {
			return nil, err
		}

		// Use relatively short response header timeout so as not to hold the image lock open too long.
		httpTransport := httpClient.Transport.(*http.Transport)
		httpTransport.ResponseHeaderTimeout = 30 * time.Second

		req, err := http.NewRequest("GET", args.Server, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("User-Agent", version.UserAgent)

		// Make the request
		raw, doneCh, err := cancel.CancelableDownload(canceler, httpClient.Do, req)
		if err != nil {
			return nil, err
		}

		defer close(doneCh)

		if raw.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Unable to fetch %q: %s", args.Server, raw.Status)
		}

		// Progress handler
		body := &ioprogress.ProgressReader{
			ReadCloser: raw.Body,
			Tracker: &ioprogress.ProgressTracker{
				Length: raw.ContentLength,
				Handler: func(percent int64, speed int64) {
					progress(ioprogress.ProgressData{Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2))})
				},
			},
		}

		// Create the target files
		f, err := os.Create(destName)
		if err != nil {
			return nil, err
		}

		defer func() { _ = f.Close() }()

		// Hashing
		sha256 := sha256.New()

		// Download the image
		writer := shared.NewQuotaWriter(io.MultiWriter(f, sha256), args.Budget)
		size, err := io.Copy(writer, body)
		if err != nil {
			return nil, err
		}

		// Validate hash
		result := fmt.Sprintf("%x", sha256.Sum(nil))
		if result != fp {
			return nil, fmt.Errorf("Hash mismatch for %q: %s != %s", args.Server, result, fp)
		}

		// Parse the image
		imageMeta, imageType, err := getImageMetadata(destName)
		if err != nil {
			return nil, err
		}

		info = &api.Image{}
		info.Fingerprint = fp
		info.Size = size
		info.Architecture = imageMeta.Architecture
		info.CreatedAt = time.Unix(imageMeta.CreationDate, 0)
		info.ExpiresAt = time.Unix(imageMeta.ExpiryDate, 0)
		info.Properties = imageMeta.Properties
		info.Type = imageType

		err = f.Close()
		if err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("Unsupported protocol: %v", protocol)
	}

	// Override visiblity
	info.Public = args.Public

	// We want to enable auto-update only if we were passed an
	// alias name, so we can figure when the associated
	// fingerprint changes in the remote.
	if alias != fp {
		info.AutoUpdate = args.AutoUpdate
	}

	// Create the database entry
	err = s.DB.Cluster.CreateImage(args.ProjectName, info.Fingerprint, info.Filename, info.Size, info.Public, info.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties, info.Type, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed creating image record: %w", err)
	}

	// Image is in the DB now, don't wipe on-disk files on failure
	failure = false

	// Check if the image path changed (private images)
	newDestName := filepath.Join(destDir, fp)
	if newDestName != destName {
		err = shared.FileMove(destName, newDestName)
		if err != nil {
			return nil, err
		}

		if shared.PathExists(destName + ".rootfs") {
			err = shared.FileMove(destName+".rootfs", newDestName+".rootfs")
			if err != nil {
				return nil, err
			}
		}
	}

	// Record the image source
	if alias != fp {
		id, _, err := s.DB.Cluster.GetImage(fp, cluster.ImageFilter{Project: &args.ProjectName})
		if err != nil {
			return nil, err
		}

		err = s.DB.Cluster.CreateImageSource(id, args.Server, protocol, args.Certificate, alias)
		if err != nil {
			return nil, err
		}
	}

	// Import into the requested storage pool
	if args.StoragePool != "" {
		err = imageCreateInPool(s, info, args.StoragePool)
		if err != nil {
			return nil, err
		}
	}

	// Mark the image as "cached" if downloading for an instance
	if args.SetCached {
		err := s.DB.Cluster.SetImageCachedAndLastUseDate(args.ProjectName, fp, time.Now().UTC())
		if err != nil {
			return nil, fmt.Errorf("Failed setting cached flag and last use date: %w", err)
		}
	}

	logger.Info("Image downloaded", ctxMap)

	var requestor *api.EventLifecycleRequestor
	if op != nil {
		requestor = op.Requestor()
	} else if r != nil {
		requestor = request.CreateRequestor(r)
	}

	s.Events.SendLifecycle(args.ProjectName, lifecycle.ImageCreated.Event(info.Fingerprint, args.ProjectName, requestor, logger.Ctx{"type": info.Type}))

	return info, nil
}
