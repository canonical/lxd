package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/cancel"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/units"
	"github.com/lxc/lxd/shared/version"

	log "github.com/lxc/lxd/shared/log15"
)

var imagesDownloading = map[string]chan bool{}
var imagesDownloadingLock sync.Mutex

// ImageDownload resolves the image fingerprint and if not in the database, downloads it
func (d *Daemon) ImageDownload(op *operations.Operation, server string, protocol string, certificate string, secret string, alias string, imageType string, forContainer bool, autoUpdate bool, storagePool string, preferCached bool, project string, budget int64) (*api.Image, error) {
	var err error
	var ctxMap log.Ctx

	var remote lxd.ImageServer
	var info *api.Image

	// Default protocol is LXD
	if protocol == "" {
		protocol = "lxd"
	}

	// Default the fingerprint to the alias string we received
	fp := alias

	// Attempt to resolve the alias
	if shared.StringInSlice(protocol, []string{"lxd", "simplestreams"}) {
		args := &lxd.ConnectionArgs{
			TLSServerCert: certificate,
			UserAgent:     version.UserAgent,
			Proxy:         d.proxy,
			CachePath:     d.os.CacheDir,
			CacheExpiry:   time.Hour,
		}

		if protocol == "lxd" {
			// Setup LXD client
			remote, err = lxd.ConnectPublicLXD(server, args)
			if err != nil {
				return nil, err
			}
		} else {
			// Setup simplestreams client
			remote, err = lxd.ConnectSimpleStreams(server, args)
			if err != nil {
				return nil, err
			}
		}

		// For public images, handle aliases and initial metadata
		if secret == "" {
			// Look for a matching alias
			entry, _, err := remote.GetImageAliasType(imageType, fp)
			if err == nil {
				fp = entry.Target
			}

			// Expand partial fingerprints
			info, _, err = remote.GetImage(fp)
			if err != nil {
				return nil, err
			}

			fp = info.Fingerprint
		}
	}

	// If auto-update is on and we're being given the image by
	// alias, try to use a locally cached image matching the given
	// server/protocol/alias, regardless of whether it's stale or
	// not (we can assume that it will be not *too* stale since
	// auto-update is on).
	interval, err := cluster.ConfigGetInt64(d.cluster, "images.auto_update_interval")
	if err != nil {
		return nil, err
	}
	if preferCached && interval > 0 && alias != fp {
		for _, architecture := range d.os.Architectures {
			cachedFingerprint, err := d.cluster.GetCachedImageSourceFingerprint(server, protocol, alias, imageType, architecture)
			if err == nil && cachedFingerprint != fp {
				fp = cachedFingerprint
				break
			}
		}
	}

	// Check if the image already exists in this project (partial hash match).
	_, imgInfo, err := d.cluster.GetImage(project, fp, false)
	if err == nil {
		// Check if the image is available locally or it's on another node.
		nodeAddress, err := d.State().Cluster.LocateImage(imgInfo.Fingerprint)
		if err != nil {
			return nil, errors.Wrapf(err, "Locate image %q in the cluster", imgInfo.Fingerprint)
		}

		if nodeAddress != "" {
			// The image is available from another node, let's try to import it.
			err = instanceImageTransfer(d, project, imgInfo.Fingerprint, nodeAddress)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed transferring image")
			}

			// As the image record already exists in the project, just add the node ID to the image.
			err = d.cluster.AddImageToLocalNode(project, imgInfo.Fingerprint)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed adding image to local node")
			}
		}
	} else if err == db.ErrNoSuchObject {
		// Check if the image already exists in some other project.
		_, imgInfo, err = d.cluster.GetImageFromAnyProject(fp)
		if err == nil {
			// Check if the image is available locally or it's on another node. Do this before creating
			// the missing DB record so we don't include ourself in the search results.
			nodeAddress, err := d.State().Cluster.LocateImage(imgInfo.Fingerprint)
			if err != nil {
				return nil, errors.Wrapf(err, "Locate image %q in the cluster", imgInfo.Fingerprint)
			}

			// We need to insert the database entry for this project, including the node ID entry.
			err = d.cluster.CreateImage(
				project, imgInfo.Fingerprint, imgInfo.Filename, imgInfo.Size, false,
				imgInfo.AutoUpdate, imgInfo.Architecture, imgInfo.CreatedAt, imgInfo.ExpiresAt,
				imgInfo.Properties, imgInfo.Type)
			if err != nil {
				return nil, err
			}

			var id int
			id, imgInfo, err = d.cluster.GetImage(project, fp, false)
			if err != nil {
				return nil, err
			}

			err = d.cluster.CreateImageSource(id, server, protocol, certificate, alias)
			if err != nil {
				return nil, err
			}

			// Transfer image if needed (after database record has been created above).
			if nodeAddress != "" {
				// The image is available from another node, let's try to import it.
				err = instanceImageTransfer(d, project, info.Fingerprint, nodeAddress)
				if err != nil {
					return nil, errors.Wrapf(err, "Failed transferring image")
				}
			}
		}
	}

	if imgInfo != nil {
		logger.Debugf("Image %q already exists in the DB", fp)
		info = imgInfo

		// If not requested in a particular pool, we're done.
		if storagePool == "" {
			return info, nil
		}

		// Get the ID of the target storage pool
		poolID, err := d.cluster.GetStoragePoolID(storagePool)
		if err != nil {
			return nil, err
		}

		// Check if the image is already in the pool
		poolIDs, err := d.cluster.GetPoolsWithImage(info.Fingerprint)
		if err != nil {
			return nil, err
		}

		if shared.Int64InSlice(poolID, poolIDs) {
			logger.Debugf("Image already exists on storage pool %q", storagePool)
			return info, nil
		}

		// Import the image in the pool
		logger.Debugf("Image does not exist on storage pool %q", storagePool)

		err = imageCreateInPool(d, info, storagePool)
		if err != nil {
			logger.Debugf("Failed to create image on storage pool %q: %v", storagePool, err)
			return nil, err
		}

		logger.Debugf("Created image on storage pool %q", storagePool)
		return info, nil
	}

	// Deal with parallel downloads
	imagesDownloadingLock.Lock()
	if waitChannel, ok := imagesDownloading[fp]; ok {
		// We are already downloading the image
		imagesDownloadingLock.Unlock()

		logger.Debug(
			"Already downloading the image, waiting for it to succeed",
			log.Ctx{"image": fp})

		// Wait until the download finishes (channel closes)
		<-waitChannel

		// Grab the database entry
		_, imgInfo, err := d.cluster.GetImage(project, fp, false)
		if err != nil {
			// Other download failed, lets try again
			logger.Error("Other image download didn't succeed", log.Ctx{"image": fp})
		} else {
			// Other download succeeded, we're done
			return imgInfo, nil
		}
	} else {
		imagesDownloadingLock.Unlock()
	}

	// Add the download to the queue
	imagesDownloadingLock.Lock()
	imagesDownloading[fp] = make(chan bool)
	imagesDownloadingLock.Unlock()

	// Unlock once this func ends.
	defer func() {
		imagesDownloadingLock.Lock()
		if waitChannel, ok := imagesDownloading[fp]; ok {
			close(waitChannel)
			delete(imagesDownloading, fp)
		}
		imagesDownloadingLock.Unlock()
	}()

	// Begin downloading
	if op == nil {
		ctxMap = log.Ctx{"alias": alias, "server": server}
	} else {
		ctxMap = log.Ctx{"trigger": op.URL(), "image": fp, "operation": op.ID(), "alias": alias, "server": server}
	}
	logger.Info("Downloading image", ctxMap)

	// Cleanup any leftover from a past attempt
	destDir := shared.VarPath("images")
	destName := filepath.Join(destDir, fp)

	failure := true
	cleanup := func() {
		if failure {
			os.Remove(destName)
			os.Remove(destName + ".rootfs")
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
			meta = make(map[string]interface{})
		}

		if meta["download_progress"] != progress.Text {
			meta["download_progress"] = progress.Text
			op.UpdateMetadata(meta)
		}
	}

	var canceler *cancel.Canceler
	if op != nil {
		canceler = cancel.NewCanceler()
		op.SetCanceler(canceler)
	}

	if protocol == "lxd" || protocol == "simplestreams" {
		// Create the target files
		dest, err := os.Create(destName)
		if err != nil {
			return nil, err
		}
		defer dest.Close()

		destRootfs, err := os.Create(destName + ".rootfs")
		if err != nil {
			return nil, err
		}
		defer destRootfs.Close()

		// Get the image information
		if info == nil {
			if secret != "" {
				info, _, err = remote.GetPrivateImage(fp, secret)
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
		if budget > 0 && info.Size > budget {
			return nil, fmt.Errorf(
				"Remote image with size %d exceeds allowed bugdget of %d",
				info.Size, budget)
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

		if secret != "" {
			resp, err = remote.GetPrivateImageFile(fp, secret, request)
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
	} else if protocol == "direct" {
		// Setup HTTP client
		httpClient, err := util.HTTPClient(certificate, d.proxy)
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequest("GET", server, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("User-Agent", version.UserAgent)

		// Make the request
		raw, doneCh, err := cancel.CancelableDownload(canceler, httpClient, req)
		defer close(doneCh)
		if err != nil {
			return nil, err
		}

		if raw.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Unable to fetch %q: %s", server, raw.Status)
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
		defer f.Close()

		// Hashing
		sha256 := sha256.New()

		// Download the image
		writer := shared.NewQuotaWriter(io.MultiWriter(f, sha256), budget)
		size, err := io.Copy(writer, body)
		if err != nil {
			return nil, err
		}

		// Validate hash
		result := fmt.Sprintf("%x", sha256.Sum(nil))
		if result != fp {
			return nil, fmt.Errorf("Hash mismatch for %q: %s != %s", server, result, fp)
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
	} else {
		return nil, fmt.Errorf("Unsupported protocol: %v", protocol)
	}

	// Override visiblity
	info.Public = false

	// We want to enable auto-update only if we were passed an
	// alias name, so we can figure when the associated
	// fingerprint changes in the remote.
	if alias != fp {
		info.AutoUpdate = autoUpdate
	}

	// Create the database entry
	err = d.cluster.CreateImage(project, info.Fingerprint, info.Filename, info.Size, info.Public, info.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties, info.Type)
	if err != nil {
		return nil, err
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
		id, _, err := d.cluster.GetImage(project, fp, false)
		if err != nil {
			return nil, err
		}

		err = d.cluster.CreateImageSource(id, server, protocol, certificate, alias)
		if err != nil {
			return nil, err
		}
	}

	// Import into the requested storage pool
	if storagePool != "" {
		err = imageCreateInPool(d, info, storagePool)
		if err != nil {
			return nil, err
		}
	}

	// Mark the image as "cached" if downloading for a container
	if forContainer {
		err := d.cluster.InitImageLastUseDate(fp)
		if err != nil {
			return nil, err
		}
	}

	logger.Info("Image downloaded", ctxMap)
	return info, nil
}
