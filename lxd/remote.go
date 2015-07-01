package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
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

func (d *Daemon) dbGetimage(fp string) int {
	q := `SELECT id FROM images WHERE fingerprint=?`
	id := -1
	arg1 := []interface{}{fp}
	arg2 := []interface{}{&id}
	err := shared.DbQueryRowScan(d.db, q, arg1, arg2)
	if err != nil {
		return -1
	}
	return id
}

func ensureLocalImage(d *Daemon, server, fp string, secret string) error {
	var url string
	var exporturl string

	if d.dbGetimage(fp) != -1 {
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

	builddir, err := ioutil.TempDir(destDir, "lxd_build_")
	if err != nil {
		return err
	}

	/* remove the builddir when done */
	defer removeImgWorkdir(d, builddir)

	destName := filepath.Join(builddir, fp)
	if _, err := os.Stat(destName); err == nil {
		os.Remove(destName)
	}

	f, err := os.Create(destName)
	if err != nil {
		return err
	}
	var wr io.Writer
	wr = f

	_, err = io.Copy(wr, raw.Body)
	f.Close()
	if err != nil {
		os.Remove(destName)
		return err
	}

	_, err = buildImageFromInfo(d, info, builddir)
	if err != nil {
		return err
	}

	return nil
}
