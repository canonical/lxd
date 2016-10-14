package main

import (
	"encoding/json"
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

	log "gopkg.in/inconshreveable/log15.v2"
)

// Simplestream cache
type imageStreamCacheEntry struct {
	Aliases      shared.ImageAliases `yaml:"aliases"`
	Fingerprints []string            `yaml:"fingerprints"`
	expiry       time.Time
	ss           *shared.SimpleStreams
}

var imageStreamCache = map[string]*imageStreamCacheEntry{}
var imageStreamCacheLock sync.Mutex

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
			ss, err := shared.SimpleStreamsClient(url, d.proxy)
			if err != nil {
				return err
			}

			entry.ss = ss
		}
	}

	return nil
}

// ImageDownload checks if we have that Image Fingerprint else
// downloads the image from a remote server.
func (d *Daemon) ImageDownload(op *operation, server string, protocol string, certificate string, secret string, alias string, forContainer bool, autoUpdate bool) (string, error) {
	var err error
	var ss *shared.SimpleStreams
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
				ss, err = shared.SimpleStreamsClient(server, d.proxy)
				if err != nil {
					return nil, err
				}

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

	if _, _, err := dbImageGet(d.db, fp, false, false); err == nil {
		shared.LogDebug("Image already exists in the db", log.Ctx{"image": fp})
		// already have it
		return fp, nil
	}

	// Now check if we already downloading the image
	d.imagesDownloadingLock.RLock()
	if waitChannel, ok := d.imagesDownloading[fp]; ok {
		// We already download the image
		d.imagesDownloadingLock.RUnlock()

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

	d.imagesDownloadingLock.RUnlock()

	if op == nil {
		ctxMap = log.Ctx{"alias": alias, "server": server}
	} else {
		ctxMap = log.Ctx{"trigger": op.url, "image": fp, "operation": op.id, "alias": alias, "server": server}
	}

	shared.LogInfo("Downloading image", ctxMap)

	// Add the download to the queue
	d.imagesDownloadingLock.Lock()
	d.imagesDownloading[fp] = make(chan bool)
	d.imagesDownloadingLock.Unlock()

	// Unlock once this func ends.
	defer func() {
		d.imagesDownloadingLock.Lock()
		if waitChannel, ok := d.imagesDownloading[fp]; ok {
			close(waitChannel)
			delete(d.imagesDownloading, fp)
		}
		d.imagesDownloadingLock.Unlock()
	}()

	exporturl := server

	var info shared.ImageInfo
	info.Fingerprint = fp

	destDir := shared.VarPath("images")
	destName := filepath.Join(destDir, fp)
	if shared.PathExists(destName) {
		d.Storage.ImageDelete(fp)
	}

	progress := func(progressInt int) {
		if op == nil {
			return
		}

		meta := op.metadata
		if meta == nil {
			meta = make(map[string]interface{})
		}

		progress := fmt.Sprintf("%d%%", progressInt)

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
				server, shared.APIVersion, fp, secret)
		} else {
			url = fmt.Sprintf("%s/%s/images/%s", server, shared.APIVersion, fp)
		}

		resp, err := d.httpGetSync(url, certificate)
		if err != nil {
			shared.LogError(
				"Failed to download image metadata",
				log.Ctx{"image": fp, "err": err})

			return "", err
		}

		if err := json.Unmarshal(resp.Metadata, &info); err != nil {
			return "", err
		}

		/* now grab the actual file from /1.0/images/%s/export */
		if secret != "" {
			exporturl = fmt.Sprintf(
				"%s/%s/images/%s/export?secret=%s",
				server, shared.APIVersion, fp, secret)

		} else {
			exporturl = fmt.Sprintf(
				"%s/%s/images/%s/export",
				server, shared.APIVersion, fp)
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

		info, err := ss.GetImageInfo(fp)
		if err != nil {
			return "", err
		}

		info.Public = false
		info.AutoUpdate = autoUpdate

		_, err = imageBuildFromInfo(d, *info)
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

	body := &shared.TransferProgress{Reader: raw.Body, Length: raw.ContentLength, Handler: progress}

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
		info.CreationDate = time.Unix(imageMeta.CreationDate, 0)
		info.ExpiryDate = time.Unix(imageMeta.ExpiryDate, 0)
		info.Properties = imageMeta.Properties
	}

	// By default, make all downloaded images private
	info.Public = false

	if alias != fp && secret == "" {
		info.AutoUpdate = autoUpdate
	}

	_, err = imageBuildFromInfo(d, info)
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
