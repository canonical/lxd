package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/sys"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/cancel"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"

	log "github.com/lxc/lxd/shared/log15"
)

// Simplestream cache
type imageStreamCacheEntry struct {
	Aliases      []api.ImageAliasesEntry `yaml:"aliases"`
	Certificate  string                  `yaml:"certificate"`
	Fingerprints []string                `yaml:"fingerprints"`

	expiry time.Time
	remote lxd.ImageServer
}

var imageStreamCache = map[string]*imageStreamCacheEntry{}
var imageStreamCacheLock sync.Mutex

var imagesDownloading = map[string]chan bool{}
var imagesDownloadingLock sync.Mutex

func imageSaveStreamCache(os *sys.OS) error {
	data, err := yaml.Marshal(&imageStreamCache)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(filepath.Join(os.CacheDir, "simplestreams.yaml"), data, 0600)
	if err != nil {
		return err
	}

	return nil
}

func imageLoadStreamCache(d *Daemon) error {
	imageStreamCacheLock.Lock()
	defer imageStreamCacheLock.Unlock()

	simplestreamsPath := filepath.Join(d.os.CacheDir, "simplestreams.yaml")
	if !shared.PathExists(simplestreamsPath) {
		return nil
	}

	content, err := ioutil.ReadFile(simplestreamsPath)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(content, imageStreamCache)
	if err != nil {
		return err
	}

	for url, entry := range imageStreamCache {
		if entry.remote == nil {
			remote, err := lxd.ConnectSimpleStreams(url, &lxd.ConnectionArgs{
				TLSServerCert: entry.Certificate,
				UserAgent:     version.UserAgent,
				Proxy:         d.proxy,
			})
			if err != nil {
				continue
			}

			entry.remote = remote
		}
	}

	return nil
}

// ImageDownload resolves the image fingerprint and if not in the database, downloads it
func (d *Daemon) ImageDownload(op *operation, server string, protocol string, certificate string, secret string, alias string, forContainer bool, autoUpdate bool, storagePool string, preferCached bool, project string) (*api.Image, error) {
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
	if protocol == "simplestreams" {
		imageStreamCacheLock.Lock()
		entry, _ := imageStreamCache[server]
		if entry == nil || entry.expiry.Before(time.Now()) {
			// Add a new entry to the cache
			refresh := func() (*imageStreamCacheEntry, error) {
				// Setup simplestreams client
				remote, err = lxd.ConnectSimpleStreams(server, &lxd.ConnectionArgs{
					TLSServerCert: certificate,
					UserAgent:     version.UserAgent,
					Proxy:         d.proxy,
				})
				if err != nil {
					return nil, err
				}

				// Get all aliases
				aliases, err := remote.GetImageAliases()
				if err != nil {
					return nil, err
				}

				// Get all fingerprints
				images, err := remote.GetImages()
				if err != nil {
					return nil, err
				}

				fingerprints := []string{}
				for _, image := range images {
					fingerprints = append(fingerprints, image.Fingerprint)
				}

				// Generate cache entry
				entry = &imageStreamCacheEntry{remote: remote, Aliases: aliases, Certificate: certificate, Fingerprints: fingerprints, expiry: time.Now().Add(time.Hour)}
				imageStreamCache[server] = entry
				imageSaveStreamCache(d.os)

				return entry, nil
			}

			newEntry, err := refresh()
			if err == nil {
				// Cache refreshed
				entry = newEntry
			} else if entry != nil {
				// Failed to fetch entry but existing cache
				logger.Warn("Unable to refresh cache, using stale entry", log.Ctx{"server": server})
				entry.expiry = time.Now().Add(time.Hour)
			} else {
				// Failed to fetch entry and nothing in cache
				imageStreamCacheLock.Unlock()
				return nil, err
			}
		} else {
			// use the existing entry
			logger.Debug("Using SimpleStreams cache entry", log.Ctx{"server": server, "expiry": entry.expiry})
			remote = entry.remote
		}
		imageStreamCacheLock.Unlock()

		// Look for a matching alias
		for _, entry := range entry.Aliases {
			if entry.Name != fp {
				continue
			}

			fp = entry.Target
			break
		}

		// Expand partial fingerprints
		matches := []string{}
		for _, entry := range entry.Fingerprints {
			if strings.HasPrefix(entry, fp) {
				matches = append(matches, entry)
			}
		}

		if len(matches) == 1 {
			fp = matches[0]
		} else if len(matches) > 1 {
			return nil, fmt.Errorf("Provided partial image fingerprint matches more than one image")
		} else {
			return nil, fmt.Errorf("The requested image couldn't be found")
		}
	} else if protocol == "lxd" {
		// Setup LXD client
		remote, err = lxd.ConnectPublicLXD(server, &lxd.ConnectionArgs{
			TLSServerCert: certificate,
			UserAgent:     version.UserAgent,
			Proxy:         d.proxy,
		})
		if err != nil {
			return nil, err
		}

		// For public images, handle aliases and initial metadata
		if secret == "" {
			// Look for a matching alias
			entry, _, err := remote.GetImageAlias(fp)
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
		cachedFingerprint, err := d.cluster.ImageSourceGetCachedFingerprint(server, protocol, alias)
		if err == nil && cachedFingerprint != fp {
			fp = cachedFingerprint
		}
	}

	// Check if the image already exists in this project (partial hash match)
	_, imgInfo, err := d.cluster.ImageGet(project, fp, false, true)
	if err == db.ErrNoSuchObject {
		// Check if the image already exists in some other project.
		_, imgInfo, err = d.cluster.ImageGetFromAnyProject(fp)
		if err == nil {
			// We just need to insert the database data, no actual download necessary.
			err = d.cluster.ImageInsert(
				project, imgInfo.Fingerprint, imgInfo.Filename, imgInfo.Size, false,
				imgInfo.AutoUpdate, imgInfo.Architecture, imgInfo.CreatedAt, imgInfo.ExpiresAt,
				imgInfo.Properties)
			if err != nil {
				return nil, err
			}

			var id int
			id, imgInfo, err = d.cluster.ImageGet(project, fp, false, true)
			if err != nil {
				return nil, err
			}
			err = d.cluster.ImageSourceInsert(id, server, protocol, certificate, alias)
			if err != nil {
				return nil, err
			}
		}
	}

	if err == nil {
		logger.Debug("Image already exists in the db", log.Ctx{"image": fp})
		info = imgInfo

		// If not requested in a particular pool, we're done.
		if storagePool == "" {
			return info, nil
		}

		// Get the ID of the target storage pool
		poolID, err := d.cluster.StoragePoolGetID(storagePool)
		if err != nil {
			return nil, err
		}

		// Check if the image is already in the pool
		poolIDs, err := d.cluster.ImageGetPools(info.Fingerprint)
		if err != nil {
			return nil, err
		}

		if shared.Int64InSlice(poolID, poolIDs) {
			logger.Debugf("Image already exists on storage pool \"%s\"", storagePool)
			return info, nil
		}

		// Import the image in the pool
		logger.Debugf("Image does not exist on storage pool \"%s\"", storagePool)

		err = imageCreateInPool(d, info, storagePool)
		if err != nil {
			logger.Debugf("Failed to create image on storage pool \"%s\": %s", storagePool, err)
			return nil, err
		}

		logger.Debugf("Created image on storage pool \"%s\"", storagePool)
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
		_, imgInfo, err := d.cluster.ImageGet(project, fp, false, true)
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
		ctxMap = log.Ctx{"trigger": op.url, "image": fp, "operation": op.id, "alias": alias, "server": server}
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

		meta := op.metadata
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
		op.canceler = canceler
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
			return nil, fmt.Errorf("Unable to fetch %s: %s", server, raw.Status)
		}

		// Progress handler
		body := &ioprogress.ProgressReader{
			ReadCloser: raw.Body,
			Tracker: &ioprogress.ProgressTracker{
				Length: raw.ContentLength,
				Handler: func(percent int64, speed int64) {
					progress(ioprogress.ProgressData{Text: fmt.Sprintf("%d%% (%s/s)", percent, shared.GetByteSizeString(speed, 2))})
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
		size, err := io.Copy(io.MultiWriter(f, sha256), body)
		if err != nil {
			return nil, err
		}

		// Validate hash
		result := fmt.Sprintf("%x", sha256.Sum(nil))
		if result != fp {
			return nil, fmt.Errorf("Hash mismatch for %s: %s != %s", server, result, fp)
		}

		// Parse the image
		imageMeta, err := getImageMetadata(destName)
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
	err = d.cluster.ImageInsert(project, info.Fingerprint, info.Filename, info.Size, info.Public, info.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties)
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
		id, _, err := d.cluster.ImageGet(project, fp, false, true)
		if err != nil {
			return nil, err
		}

		err = d.cluster.ImageSourceInsert(id, server, protocol, certificate, alias)
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
		err := d.cluster.ImageLastAccessInit(fp)
		if err != nil {
			return nil, err
		}
	}

	logger.Info("Image downloaded", ctxMap)
	return info, nil
}
