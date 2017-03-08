package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/simplestreams"
	"github.com/lxc/lxd/shared/version"

	log "gopkg.in/inconshreveable/log15.v2"
)

// Simplestream cache
type imageStreamCacheEntry struct {
	Aliases      []api.ImageAliasesEntry `yaml:"aliases"`
	Fingerprints []string                `yaml:"fingerprints"`
	expiry       time.Time
	ss           *simplestreams.SimpleStreams
}

var imageStreamCache = map[string]*imageStreamCacheEntry{}
var imageStreamCacheLock sync.Mutex

var imagesDownloading = map[string]chan bool{}
var imagesDownloadingLock sync.Mutex

func imageSaveStreamCache() error {
	data, err := yaml.Marshal(&imageStreamCache)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(shared.CachePath("simplestreams.yaml"), data, 0600)
	if err != nil {
		return err
	}

	return nil
}

func imageLoadStreamCache(d *Daemon) error {
	imageStreamCacheLock.Lock()
	defer imageStreamCacheLock.Unlock()

	if !shared.PathExists(shared.CachePath("simplestreams.yaml")) {
		return nil
	}

	content, err := ioutil.ReadFile(shared.CachePath("simplestreams.yaml"))
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(content, imageStreamCache)
	if err != nil {
		return err
	}

	for url, entry := range imageStreamCache {
		if entry.ss == nil {
			myhttp, err := d.httpClient("")
			if err != nil {
				return err
			}

			ss := simplestreams.NewClient(url, *myhttp, version.UserAgent)
			entry.ss = ss
		}
	}

	return nil
}

// ImageDownload checks if we have that Image Fingerprint else
// downloads the image from a remote server.
func (d *Daemon) ImageDownload(op *operation, server string, protocol string, certificate string, secret string, alias string, forContainer bool, autoUpdate bool, storagePool string) (string, error) {
	var err error
	var ss *simplestreams.SimpleStreams
	var ctxMap log.Ctx

	if protocol == "" {
		protocol = "lxd"
	}

	fp := alias

	// Expand aliases
	if protocol == "simplestreams" {
		imageStreamCacheLock.Lock()
		entry, _ := imageStreamCache[server]
		if entry == nil || entry.expiry.Before(time.Now()) {
			refresh := func() (*imageStreamCacheEntry, error) {
				// Setup simplestreams client
				myhttp, err := d.httpClient(certificate)
				if err != nil {
					return nil, err
				}

				ss = simplestreams.NewClient(server, *myhttp, version.UserAgent)

				// Get all aliases
				aliases, err := ss.ListAliases()
				if err != nil {
					return nil, err
				}

				// Get all fingerprints
				images, err := ss.ListImages()
				if err != nil {
					return nil, err
				}

				fingerprints := []string{}
				for _, image := range images {
					fingerprints = append(fingerprints, image.Fingerprint)
				}

				// Generate cache entry
				entry = &imageStreamCacheEntry{ss: ss, Aliases: aliases, Fingerprints: fingerprints, expiry: time.Now().Add(time.Hour)}
				imageStreamCache[server] = entry
				imageSaveStreamCache()

				return entry, nil
			}

			newEntry, err := refresh()
			if err == nil {
				// Cache refreshed
				entry = newEntry
			} else if entry != nil {
				// Failed to fetch entry but existing cache
				shared.LogWarn("Unable to refresh cache, using stale entry", log.Ctx{"server": server})
				entry.expiry = time.Now().Add(time.Hour)
			} else {
				// Failed to fetch entry and nothing in cache
				imageStreamCacheLock.Unlock()
				return "", err
			}
		} else {
			shared.LogDebug("Using SimpleStreams cache entry", log.Ctx{"server": server, "expiry": entry.expiry})
			ss = entry.ss
		}
		imageStreamCacheLock.Unlock()

		// Expand aliases
		for _, alias := range entry.Aliases {
			if alias.Name != fp {
				continue
			}

			fp = alias.Target
			break
		}

		// Expand fingerprint
		for _, fingerprint := range entry.Fingerprints {
			if !strings.HasPrefix(fingerprint, fp) {
				continue
			}

			if fp == alias {
				alias = fingerprint
			}
			fp = fingerprint
			break
		}
	} else if protocol == "lxd" {
		target, err := remoteGetImageFingerprint(d, server, certificate, fp)
		if err == nil && target != "" {
			fp = target
		}
	}

	// Check if the image already exists on any storage pool.
	_, imgInfo, err := dbImageGet(d.db, fp, false, false)
	if err == nil {
		shared.LogDebug("Image already exists in the db", log.Ctx{"image": fp})

		if storagePool == "" {
			return fp, nil
		}

		// Get the ID of the storage pool on which a storage volume for
		// the image needs to exist.
		poolID, err := dbStoragePoolGetID(d.db, storagePool)
		if err != nil {
			return "", err
		}

		// Get the IDs of all storage pools on which a storage volume
		// for the requested image currently exists.
		poolIDs, err := dbImageGetPools(d.db, imgInfo.Fingerprint)
		if err != nil {
			return "", err
		}

		// Check if the image already exists on the current storage
		// pool.
		if shared.Int64InSlice(poolID, poolIDs) {
			shared.LogDebugf("Image already exists on storage pool \"%s\".", storagePool)
			return fp, nil
		}

		shared.LogDebugf("Image does not exist on storage pool \"%s\".", storagePool)

		// Create a duplicate entry for the image.
		err = imageCreateInPool(d, imgInfo, storagePool)
		if err != nil {
			shared.LogDebugf("Failed to create image on storage pool \"%s\": %s.", storagePool, err)
			return "", err
		}

		shared.LogDebugf("Created image on storage pool \"%s\".", storagePool)
		return fp, nil
	}

	// Now check if we already downloading the image
	imagesDownloadingLock.Lock()
	if waitChannel, ok := imagesDownloading[fp]; ok {
		// We already download the image
		imagesDownloadingLock.Unlock()

		shared.LogDebug(
			"Already downloading the image, waiting for it to succeed",
			log.Ctx{"image": fp})

		// Wait until the download finishes (channel closes)
		if _, ok := <-waitChannel; ok {
			shared.LogWarnf("Value transmitted over image lock semaphore?")
		}

		if _, _, err := dbImageGet(d.db, fp, false, true); err != nil {
			shared.LogError(
				"Previous download didn't succeed",
				log.Ctx{"image": fp})

			return "", fmt.Errorf("Previous download didn't succeed")
		}

		shared.LogDebug(
			"Previous download succeeded",
			log.Ctx{"image": fp})

		return fp, nil
	}

	// Add the download to the queue
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

	shared.LogInfo("Downloading image", ctxMap)

	exporturl := server

	var info api.Image
	info.Fingerprint = fp

	destDir := shared.VarPath("images")
	destName := filepath.Join(destDir, fp)
	if shared.PathExists(destName) {
		os.Remove(filepath.Join(destDir, fp))
		os.Remove(filepath.Join(destDir, fp+".root"))
	}

	progress := func(progressInt int64, speedInt int64) {
		if op == nil {
			return
		}

		meta := op.metadata
		if meta == nil {
			meta = make(map[string]interface{})
		}

		progress := fmt.Sprintf("%d%% (%s/s)", progressInt, shared.GetByteSizeString(speedInt, 2))

		if meta["download_progress"] != progress {
			meta["download_progress"] = progress
			op.UpdateMetadata(meta)
		}
	}

	if protocol == "lxd" {
		/* grab the metadata from /1.0/images/%s */
		var url string
		if secret != "" {
			url = fmt.Sprintf(
				"%s/%s/images/%s?secret=%s",
				server, version.APIVersion, fp, secret)
		} else {
			url = fmt.Sprintf("%s/%s/images/%s", server, version.APIVersion, fp)
		}

		resp, err := d.httpGetSync(url, certificate)
		if err != nil {
			shared.LogError(
				"Failed to download image metadata",
				log.Ctx{"image": fp, "err": err})

			return "", err
		}

		if err := resp.MetadataAsStruct(&info); err != nil {
			return "", err
		}

		/* now grab the actual file from /1.0/images/%s/export */
		if secret != "" {
			exporturl = fmt.Sprintf(
				"%s/%s/images/%s/export?secret=%s",
				server, version.APIVersion, fp, secret)

		} else {
			exporturl = fmt.Sprintf(
				"%s/%s/images/%s/export",
				server, version.APIVersion, fp)
		}
	} else if protocol == "simplestreams" {
		err := ss.Download(fp, "meta", destName, nil)
		if err != nil {
			return "", err
		}

		err = ss.Download(fp, "root", destName+".rootfs", progress)
		if err != nil {
			return "", err
		}

		info, err := ss.GetImage(fp)
		if err != nil {
			return "", err
		}

		info.Public = false
		info.AutoUpdate = autoUpdate

		if storagePool != "" {
			err = imageCreateInPool(d, info, storagePool)
			if err != nil {
				return "", err
			}
		}

		// Create the database entry
		err = dbImageInsert(d.db, info.Fingerprint, info.Filename, info.Size, info.Public, info.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties)
		if err != nil {
			return "", err
		}

		if alias != fp {
			id, _, err := dbImageGet(d.db, fp, false, true)
			if err != nil {
				return "", err
			}

			err = dbImageSourceInsert(d.db, id, server, protocol, "", alias)
			if err != nil {
				return "", err
			}
		}

		shared.LogInfo("Image downloaded", ctxMap)

		if forContainer {
			return fp, dbImageLastAccessInit(d.db, fp)
		}

		return fp, nil
	}

	raw, err := d.httpGetFile(exporturl, certificate)
	if err != nil {
		shared.LogError(
			"Failed to download image",
			log.Ctx{"image": fp, "err": err})
		return "", err
	}
	info.Size = raw.ContentLength

	ctype, ctypeParams, err := mime.ParseMediaType(raw.Header.Get("Content-Type"))
	if err != nil {
		ctype = "application/octet-stream"
	}

	body := &ioprogress.ProgressReader{
		ReadCloser: raw.Body,
		Tracker: &ioprogress.ProgressTracker{
			Length:  raw.ContentLength,
			Handler: progress,
		},
	}

	if ctype == "multipart/form-data" {
		// Parse the POST data
		mr := multipart.NewReader(body, ctypeParams["boundary"])

		// Get the metadata tarball
		part, err := mr.NextPart()
		if err != nil {
			shared.LogError(
				"Invalid multipart image",
				log.Ctx{"image": fp, "err": err})

			return "", err
		}

		if part.FormName() != "metadata" {
			shared.LogError(
				"Invalid multipart image",
				log.Ctx{"image": fp, "err": err})

			return "", fmt.Errorf("Invalid multipart image")
		}

		destName = filepath.Join(destDir, info.Fingerprint)
		f, err := os.Create(destName)
		if err != nil {
			shared.LogError(
				"Failed to save image",
				log.Ctx{"image": fp, "err": err})

			return "", err
		}

		_, err = io.Copy(f, part)
		f.Close()

		if err != nil {
			shared.LogError(
				"Failed to save image",
				log.Ctx{"image": fp, "err": err})

			return "", err
		}

		// Get the rootfs tarball
		part, err = mr.NextPart()
		if err != nil {
			shared.LogError(
				"Invalid multipart image",
				log.Ctx{"image": fp, "err": err})

			return "", err
		}

		if part.FormName() != "rootfs" {
			shared.LogError(
				"Invalid multipart image",
				log.Ctx{"image": fp})
			return "", fmt.Errorf("Invalid multipart image")
		}

		destName = filepath.Join(destDir, info.Fingerprint+".rootfs")
		f, err = os.Create(destName)
		if err != nil {
			shared.LogError(
				"Failed to save image",
				log.Ctx{"image": fp, "err": err})
			return "", err
		}

		_, err = io.Copy(f, part)
		f.Close()

		if err != nil {
			shared.LogError(
				"Failed to save image",
				log.Ctx{"image": fp, "err": err})
			return "", err
		}
	} else {
		destName = filepath.Join(destDir, info.Fingerprint)

		f, err := os.Create(destName)
		if err != nil {
			shared.LogError(
				"Failed to save image",
				log.Ctx{"image": fp, "err": err})

			return "", err
		}

		_, err = io.Copy(f, body)
		f.Close()

		if err != nil {
			shared.LogError(
				"Failed to save image",
				log.Ctx{"image": fp, "err": err})
			return "", err
		}
	}

	if protocol == "direct" {
		imageMeta, err := getImageMetadata(destName)
		if err != nil {
			return "", err
		}

		info.Architecture = imageMeta.Architecture
		info.CreatedAt = time.Unix(imageMeta.CreationDate, 0)
		info.ExpiresAt = time.Unix(imageMeta.ExpiryDate, 0)
		info.Properties = imageMeta.Properties
	}

	// By default, make all downloaded images private
	info.Public = false

	if alias != fp && secret == "" {
		info.AutoUpdate = autoUpdate
	}

	if storagePool != "" {
		err = imageCreateInPool(d, &info, storagePool)
		if err != nil {
			return "", err
		}
	}

	// Create the database entry
	err = dbImageInsert(d.db, info.Fingerprint, info.Filename, info.Size, info.Public, info.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties)
	if err != nil {
		shared.LogError(
			"Failed to create image",
			log.Ctx{"image": fp, "err": err})

		return "", err
	}

	if alias != fp {
		id, _, err := dbImageGet(d.db, fp, false, true)
		if err != nil {
			return "", err
		}

		err = dbImageSourceInsert(d.db, id, server, protocol, "", alias)
		if err != nil {
			return "", err
		}
	}

	shared.LogInfo("Image downloaded", ctxMap)

	if forContainer {
		return fp, dbImageLastAccessInit(d.db, fp)
	}

	return fp, nil
}
