package main

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"

	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

type Progress struct {
	io.Reader
	total      int64
	length     int64
	percentage float64
	op         *operation
}

func (pt *Progress) Read(p []byte) (int, error) {
	n, err := pt.Reader.Read(p)
	if n > 0 {
		pt.total += int64(n)
		percentage := float64(pt.total) / float64(pt.length) * float64(100)

		if percentage-pt.percentage > 0.9 && pt.op != nil {
			meta := pt.op.metadata
			if meta == nil {
				meta = make(map[string]interface{})
			}

			progressInt := 1 - (int(percentage) % 1) + int(percentage)
			if progressInt > 100 {
				progressInt = 100
			}
			progress := fmt.Sprintf("%d%%", progressInt)

			if meta["download_progress"] != progress {
				meta["download_progress"] = progress
				pt.op.UpdateMetadata(meta)
			}

			pt.percentage = percentage
		}
	}

	return n, err
}

// ImageDownload checks if we have that Image Fingerprint else
// downloads the image from a remote server.
func (d *Daemon) ImageDownload(op *operation,
	server, fp string, secret string, forContainer bool, directDownload bool) error {

	if _, err := dbImageGet(d.db, fp, false, false); err == nil {
		shared.Log.Debug("Image already exists in the db", log.Ctx{"image": fp})
		// already have it
		return nil
	}

	shared.Log.Info(
		"Image not in the db, downloading it",
		log.Ctx{"image": fp, "server": server})

	// Now check if we already downloading the image
	d.imagesDownloadingLock.RLock()
	if waitChannel, ok := d.imagesDownloading[fp]; ok {
		// We already download the image
		d.imagesDownloadingLock.RUnlock()

		shared.Log.Info(
			"Already downloading the image, waiting for it to succeed",
			log.Ctx{"image": fp})

		// Wait until the download finishes (channel closes)
		if _, ok := <-waitChannel; ok {
			shared.Log.Warn("Value transmitted over image lock semaphore?")
		}

		if _, err := dbImageGet(d.db, fp, false, true); err != nil {
			shared.Log.Error(
				"Previous download didn't succeed",
				log.Ctx{"image": fp})

			return fmt.Errorf("Previous download didn't succeed")
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

	if !directDownload {
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

			return err
		}

		if err := json.Unmarshal(resp.Metadata, &info); err != nil {
			return err
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
	}

	raw, err := d.httpGetFile(exporturl)
	if err != nil {
		shared.Log.Error(
			"Failed to download image",
			log.Ctx{"image": fp, "err": err})
		return err
	}
	info.Size = raw.ContentLength

	destDir := shared.VarPath("images")
	destName := filepath.Join(destDir, fp)
	if shared.PathExists(destName) {
		d.Storage.ImageDelete(fp)
	}

	ctype, ctypeParams, err := mime.ParseMediaType(raw.Header.Get("Content-Type"))
	if err != nil {
		ctype = "application/octet-stream"
	}

	body := &Progress{Reader: raw.Body, length: raw.ContentLength, op: op}

	if ctype == "multipart/form-data" {
		// Parse the POST data
		mr := multipart.NewReader(body, ctypeParams["boundary"])

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

		destName = filepath.Join(destDir, info.Fingerprint)
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
		destName = filepath.Join(destDir, info.Fingerprint)

		f, err := os.Create(destName)
		if err != nil {
			shared.Log.Error(
				"Failed to save image",
				log.Ctx{"image": fp, "err": err})

			return err
		}

		_, err = io.Copy(f, body)
		f.Close()

		if err != nil {
			shared.Log.Error(
				"Failed to save image",
				log.Ctx{"image": fp, "err": err})
			return err
		}
	}

	if directDownload {
		imageMeta, err := getImageMetadata(destName)
		if err != nil {
			return err
		}

		info.Architecture, _ = shared.ArchitectureId(imageMeta.Architecture)
		info.CreationDate = imageMeta.CreationDate
		info.ExpiryDate = imageMeta.ExpiryDate
		info.Properties = imageMeta.Properties
	}

	// By default, make all downloaded images private
	info.Public = false

	_, err = imageBuildFromInfo(d, info)
	if err != nil {
		shared.Log.Error(
			"Failed to create image",
			log.Ctx{"image": fp, "err": err})

		return err
	}

	shared.Log.Info(
		"Download succeeded",
		log.Ctx{"image": fp})

	if forContainer {
		return dbImageLastAccessInit(d.db, fp)
	}

	return nil
}
