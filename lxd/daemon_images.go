package main

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"sync"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

// ImageDownload checks if we have that Image Fingerprint else
// downloads the image from a remote server.
func (d *Daemon) ImageDownload(
	server, fp string, secret string, forContainer bool) error {

	if _, err := dbImageGet(d.db, fp, false, true); err == nil {
		// already have it
		return nil
	}

	// Now check if we already downloading the image
	d.imagesDownloadingLock.RLock()
	if lock, ok := d.imagesDownloading[fp]; ok {
		// We already download the image
		d.imagesDownloadingLock.RUnlock()

		shared.Log.Info(
			"Already downloading the image, waiting for it to succeed",
			log.Ctx{"image": fp})

		// Now we use a little trick
		// we wait until we can lock the download once
		// we are able to lock it we return we have it on succeed.
		lock.Lock()
		lock.Unlock()

		// Somehow dbImageGet fails here so i use dbImagesGet.
		{
			results, err := dbImagesGet(d.db, false)
			if err != nil {
				shared.Log.Error("Failed to get the image list")
				return fmt.Errorf("Failed to get the image list")
			}

			found := false
			for _, imagesFP := range results {
				if imagesFP == fp {
					found = true
					break
				}
			}

			if !found {
				shared.Log.Error(
					"Previous download didn't succeed",
					log.Ctx{"image": fp, "err": err})

				return fmt.Errorf("Previous download didn't succeed")
			}
		}

		shared.Log.Info(
			"Previous download succeeded",
			log.Ctx{"image": fp})

		return nil
	}

	d.imagesDownloadingLock.RUnlock()

	shared.Log.Info(
		"Downloading the image",
		log.Ctx{"image": fp})

	// Add the download to the queue
	lock := sync.Mutex{}
	lock.Lock()
	d.imagesDownloadingLock.Lock()
	d.imagesDownloading[fp] = &lock
	d.imagesDownloadingLock.Unlock()

	// Unlock once this func ends.
	defer func() {
		if lock, ok := d.imagesDownloading[fp]; ok {
			d.imagesDownloadingLock.Lock()
			lock.Unlock()

			delete(d.imagesDownloading, fp)

			d.imagesDownloadingLock.Unlock()
		}
	}()

	/* grab the metadata from /1.0/images/%s */
	var url string
	if secret != "" {
		url = fmt.Sprintf(
			"%s/%s/images/%s?secret=%s",
			server, shared.APIVersion, fp, secret)

	} else {
		url = fmt.Sprintf("%s/%s/images/%s", server, shared.APIVersion, fp)
	}

	resp, err := d.httpGetSync(url)
	if err != nil {
		shared.Log.Error(
			"Failed to download image metadata",
			log.Ctx{"image": fp, "err": err})

		return nil
	}

	info := shared.ImageInfo{}
	if err := json.Unmarshal(resp.Metadata, &info); err != nil {
		return err
	}

	/* now grab the actual file from /1.0/images/%s/export */
	var exporturl string
	if secret != "" {
		exporturl = fmt.Sprintf(
			"%s/%s/images/%s/export?secret=%s",
			server, shared.APIVersion, fp, secret)

	} else {
		exporturl = fmt.Sprintf(
			"%s/%s/images/%s/export",
			server, shared.APIVersion, fp)
	}

	raw, err := d.httpGetFile(exporturl)
	if err != nil {
		shared.Log.Error(
			"Failed to download image",
			log.Ctx{"image": fp, "err": err})
		return err
	}

	destDir := shared.VarPath("images")
	destName := filepath.Join(destDir, fp)
	if shared.PathExists(destName) {
		d.Storage.ImageDelete(fp)
	}

	ctype, ctypeParams, err := mime.ParseMediaType(raw.Header.Get("Content-Type"))
	if err != nil {
		ctype = "application/octet-stream"
	}

	if ctype == "multipart/form-data" {
		// Parse the POST data
		mr := multipart.NewReader(raw.Body, ctypeParams["boundary"])

		// Get the metadata tarball
		part, err := mr.NextPart()
		if err != nil {
			shared.Log.Error(
				"Invalid multipart image",
				log.Ctx{"image": fp, "err": err})

			return err
		}

		if part.FormName() != "metadata" {
			shared.Log.Error(
				"Invalid multipart image",
				log.Ctx{"image": fp, "err": err})

			return fmt.Errorf("Invalid multipart image")
		}

		destName := filepath.Join(destDir, info.Fingerprint)
		f, err := os.Create(destName)
		if err != nil {
			shared.Log.Error(
				"Failed to save image",
				log.Ctx{"image": fp, "err": err})

			return err
		}

		_, err = io.Copy(f, part)
		f.Close()

		if err != nil {
			shared.Log.Error(
				"Failed to save image",
				log.Ctx{"image": fp, "err": err})

			return err
		}

		// Get the rootfs tarball
		part, err = mr.NextPart()
		if err != nil {
			shared.Log.Error(
				"Invalid multipart image",
				log.Ctx{"image": fp, "err": err})

			return err
		}

		if part.FormName() != "rootfs" {
			shared.Log.Error(
				"Invalid multipart image",
				log.Ctx{"image": fp})
			return fmt.Errorf("Invalid multipart image")
		}

		destName = filepath.Join(destDir, info.Fingerprint+".rootfs")
		f, err = os.Create(destName)
		if err != nil {
			shared.Log.Error(
				"Failed to save image",
				log.Ctx{"image": fp, "err": err})
			return err
		}

		_, err = io.Copy(f, part)
		f.Close()

		if err != nil {
			shared.Log.Error(
				"Failed to save image",
				log.Ctx{"image": fp, "err": err})
			return err
		}
	} else {
		destName := filepath.Join(destDir, info.Fingerprint)

		f, err := os.Create(destName)
		if err != nil {
			shared.Log.Error(
				"Failed to save image",
				log.Ctx{"image": fp, "err": err})

			return err
		}

		_, err = io.Copy(f, raw.Body)
		f.Close()

		if err != nil {
			shared.Log.Error(
				"Failed to save image",
				log.Ctx{"image": fp, "err": err})
			return err
		}
	}

	_, err = imageBuildFromInfo(d, info)
	if err != nil {
		shared.Log.Error(
			"Failed to create image",
			log.Ctx{"image": fp, "err": err})

		return err
	}

	if forContainer {
		return dbInitImageLastAccess(d, fp)
	}

	shared.Log.Info(
		"Download succeeded",
		log.Ctx{"image": fp})

	return nil
}
