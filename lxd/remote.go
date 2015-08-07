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
)

func remoteGetImageFingerprint(d *Daemon, server string, alias string) (string, error) {
	url := fmt.Sprintf("%s/%s/images/aliases/%s", server, shared.APIVersion, alias)

	resp, err := d.httpGetSync(url)
	if err != nil {
		return "", err
	}

	var result shared.ImageAlias
	if err = json.Unmarshal(resp.Metadata, &result); err != nil {
		return "", fmt.Errorf("Error reading alias\n")
	}
	return result.Name, nil
}

func ensureLocalImage(d *Daemon, server, fp string, secret string, forContainer bool) error {
	var url string
	var exporturl string

	if dbImageIDGet(d.db, fp) != -1 {
		// already have it
		return nil
	}

	/* grab the metadata from /1.0/images/%s */
	if secret != "" {
		url = fmt.Sprintf("%s/%s/images/%s?secret=%s", server, shared.APIVersion, fp, secret)
	} else {
		url = fmt.Sprintf("%s/%s/images/%s", server, shared.APIVersion, fp)
	}

	resp, err := d.httpGetSync(url)
	if err != nil {
		return nil
	}

	info := shared.ImageInfo{}
	if err := json.Unmarshal(resp.Metadata, &info); err != nil {
		return err
	}

	/* now grab the actual file from /1.0/images/%s/export */
	if secret != "" {
		exporturl = fmt.Sprintf("%s/%s/images/%s/export?secret=%s", server, shared.APIVersion, fp, secret)
	} else {
		exporturl = fmt.Sprintf("%s/%s/images/%s/export", server, shared.APIVersion, fp)
	}

	raw, err := d.httpGetFile(exporturl)
	if err != nil {
		return err
	}

	destDir := shared.VarPath("images")
	err = os.MkdirAll(destDir, 0700)
	if err != nil {
		return err
	}
	destName := filepath.Join(destDir, fp)
	if shared.PathExists(destName) {
		os.Remove(destName)
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
			return err
		}

		if part.FormName() != "metadata" {
			return fmt.Errorf("Invalid multipart image")
		}

		destName := filepath.Join(destDir, info.Fingerprint)
		f, err := os.Create(destName)
		if err != nil {
			return err
		}

		_, err = io.Copy(f, part)
		f.Close()

		if err != nil {
			return err
		}

		// Get the rootfs tarball
		part, err = mr.NextPart()
		if err != nil {
			return err
		}

		if part.FormName() != "rootfs" {
			return fmt.Errorf("Invalid multipart image")
		}

		destName = filepath.Join(destDir, info.Fingerprint+".rootfs")
		f, err = os.Create(destName)
		if err != nil {
			return err
		}

		_, err = io.Copy(f, part)
		f.Close()

		if err != nil {
			return err
		}
	} else {
		destName := filepath.Join(destDir, info.Fingerprint)

		f, err := os.Create(destName)
		if err != nil {
			return err
		}

		_, err = io.Copy(f, raw.Body)
		f.Close()

		if err != nil {
			return err
		}
	}

	_, err = imageBuildFromInfo(d, info)
	if err != nil {
		return err
	}

	if forContainer {
		return dbInitImageLastAccess(d, fp)
	}

	return nil
}
