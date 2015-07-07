package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"
)

const (
	COMPRESSION_TAR = iota
	COMPRESSION_GZIP
	COMPRESSION_BZ2
	COMPRESSION_LZMA
	COMPRESSION_XZ
)

func getSize(f *os.File) (int64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func detectCompression(fname string) (int, string, error) {

	f, err := os.Open(fname)
	if err != nil {
		return -1, "", err
	}
	defer f.Close()

	// read header parts to detect compression method
	// bz2 - 2 bytes, 'BZ' signature/magic number
	// gz - 2 bytes, 0x1f 0x8b
	// lzma - 6 bytes, { [0x000, 0xE0], '7', 'z', 'X', 'Z', 0x00 } -
	// xy - 6 bytes,  header format { 0xFD, '7', 'z', 'X', 'Z', 0x00 }
	// tar - 263 bytes, trying to get ustar from 257 - 262
	header := make([]byte, 263)
	_, err = f.Read(header)

	switch {
	case bytes.Equal(header[0:2], []byte{'B', 'Z'}):
		return COMPRESSION_BZ2, ".tar.bz2", nil
	case bytes.Equal(header[0:2], []byte{0x1f, 0x8b}):
		return COMPRESSION_GZIP, ".tar.gz", nil
	case (bytes.Equal(header[1:5], []byte{'7', 'z', 'X', 'Z'}) && header[0] == 0xFD):
		return COMPRESSION_XZ, ".tar.xz", nil
	case (bytes.Equal(header[1:5], []byte{'7', 'z', 'X', 'Z'}) && header[0] != 0xFD):
		return COMPRESSION_LZMA, ".tar.lzma", nil
	case bytes.Equal(header[257:262], []byte{'u', 's', 't', 'a', 'r'}):
		return COMPRESSION_TAR, ".tar", nil
	default:
		return -1, "", fmt.Errorf("Unsupported compression.")
	}

}

func untarImage(imagefname string, destpath string) error {
	compression, _, err := detectCompression(imagefname)
	if err != nil {
		return err
	}

	args := []string{"-C", destpath, "--numeric-owner"}
	switch compression {
	case COMPRESSION_TAR:
		args = append(args, "-xf")
	case COMPRESSION_GZIP:
		args = append(args, "-zxf")
	case COMPRESSION_BZ2:
		args = append(args, "--jxf")
	case COMPRESSION_LZMA:
		args = append(args, "--lzma", "-xf")
	default:
		args = append(args, "-Jxf")
	}
	args = append(args, imagefname)

	output, err := exec.Command("tar", args...).CombinedOutput()
	if err != nil {
		shared.Debugf("image unpacking failed\n")
		shared.Debugf(string(output))
		return err
	}

	return nil
}

type imageFromContainerPostReq struct {
	Filename   string            `json:"filename"`
	Public     bool              `json:"public"`
	Source     map[string]string `json:"source"`
	Properties map[string]string `json:"properties"`
}

type imageMetadata struct {
	Architecture string                    `yaml:"architecture"`
	CreationDate int64                     `yaml:"creation_date"`
	ExpiryDate   int64                     `yaml:"expiry_date"`
	Properties   map[string]interface{}    `yaml:"properties"`
	Templates    map[string]*TemplateEntry `yaml:"templates"`
}

/*
 * This function takes a container or snapshot from the local image server and
 * exports it as an image.
 */
func imgPostContInfo(d *Daemon, r *http.Request, req imageFromContainerPostReq,
	builddir string) (info shared.ImageInfo, err error) {

	info.Properties = map[string]string{}
	name := req.Source["name"]
	ctype := req.Source["type"]
	if ctype == "" || name == "" {
		return info, fmt.Errorf("No source provided")
	}

	switch ctype {
	case "snapshot":
		if !shared.IsSnapshot(name) {
			return info, fmt.Errorf("Not a snapshot")
		}
	case "container":
		if shared.IsSnapshot(name) {
			return info, fmt.Errorf("This is a snapshot")
		}
	default:
		return info, fmt.Errorf("Bad type")
	}

	info.Filename = req.Filename
	switch req.Public {
	case true:
		info.Public = 1
	case false:
		info.Public = 0
	}

	snap := ""
	if ctype == "snapshot" {
		fields := strings.SplitN(name, "/", 2)
		if len(fields) != 2 {
			return info, fmt.Errorf("Not a snapshot")
		}
		name = fields[0]
		snap = fields[1]
	}

	c, err := newLxdContainer(name, d)
	if err != nil {
		return info, err
	}

	// Build the actual image file
	tarfname := fmt.Sprintf("%s.tar", name)
	tarpath := filepath.Join(builddir, tarfname)
	tarfile, err := os.OpenFile(tarpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return info, err
	}
	if err := c.exportToTar(snap, tarfile); err != nil {
		tarfile.Close()
		return info, fmt.Errorf("imgPostContInfo: exportToTar failed: %s\n", err)
	}
	tarfile.Close()

	args := []string{tarpath}
	_, err = exec.Command("gzip", args...).CombinedOutput()
	if err != nil {
		shared.Debugf("image compression\n")
		return info, err
	}
	gztarpath := fmt.Sprintf("%s.gz", tarpath)

	sha256 := sha256.New()
	tarf, err := os.Open(gztarpath)
	if err != nil {
		return info, err
	}
	info.Size, err = io.Copy(sha256, tarf)
	tarf.Close()
	if err != nil {
		return info, err
	}
	info.Fingerprint = fmt.Sprintf("%x", sha256.Sum(nil))

	/* rename the the file to the expected name so our caller can use it */
	imagefname := filepath.Join(builddir, info.Fingerprint)
	err = os.Rename(gztarpath, imagefname)
	if err != nil {
		return info, err
	}

	info.Architecture = c.architecture
	info.Properties = req.Properties

	return info, nil
}

func getImgPostInfo(d *Daemon, r *http.Request, builddir string) (info shared.ImageInfo,
	err error) {
	// Is this a container request?
	decoder := json.NewDecoder(r.Body)
	req := imageFromContainerPostReq{}
	if err = decoder.Decode(&req); err == nil {
		return imgPostContInfo(d, r, req, builddir)
	}

	// ok we've got an image in the body
	info.Public, _ = strconv.Atoi(r.Header.Get("X-LXD-public"))
	info.Filename = r.Header.Get("X-LXD-filename")
	propHeaders := r.Header[http.CanonicalHeaderKey("X-LXD-properties")]

	info.Properties = map[string]string{}
	if len(propHeaders) > 0 {
		for _, ph := range propHeaders {
			p, _ := url.ParseQuery(ph)
			for pkey, pval := range p {
				info.Properties[pkey] = pval[0]
			}
		}
	}

	// Create a file for the tarball
	tarf, err := ioutil.TempFile(builddir, "lxd_tar_")
	if err != nil {
		return info, err
	}

	tarfname := tarf.Name()
	sha256 := sha256.New()

	var size1, size2 int64
	size1, err = io.Copy(io.MultiWriter(tarf, sha256), decoder.Buffered())
	if err == nil {
		size2, err = io.Copy(io.MultiWriter(tarf, sha256), r.Body)
	}
	info.Size = size1 + size2
	tarf.Close()
	if err != nil {
		return info, err
	}

	info.Fingerprint = fmt.Sprintf("%x", sha256.Sum(nil))
	expectedFingerprint := r.Header.Get("X-LXD-fingerprint")
	if expectedFingerprint != "" && info.Fingerprint != expectedFingerprint {
		err = fmt.Errorf("fingerprints don't match, got %s expected %s", info.Fingerprint, expectedFingerprint)
		return info, err
	}

	imgfname := filepath.Join(builddir, info.Fingerprint)
	err = os.Rename(tarfname, imgfname)
	if err != nil {
		return info, err
	}

	var imageMeta *imageMetadata
	imageMeta, err = getImageMetadata(imgfname)
	if err != nil {
		return info, err
	}

	info.Architecture, _ = shared.ArchitectureId(imageMeta.Architecture)
	info.CreationDate = imageMeta.CreationDate
	info.ExpiryDate = imageMeta.ExpiryDate

	return info, nil
}

func makeBtrfsSubvol(imagefname, subvol string) error {
	output, err := exec.Command("btrfs", "subvolume", "create", subvol).CombinedOutput()
	if err != nil {
		shared.Debugf("btrfs subvolume creation failed\n")
		shared.Debugf(string(output))
		return err
	}

	err = untarImage(imagefname, subvol)
	if err != nil {
		return err
	}

	return nil
}

func removeImgWorkdir(d *Daemon, builddir string) {

	vgname, _, err := getServerConfigValue(d, "core.lvm_vg_name")
	if err != nil {
		shared.Debugf("Error checking server config: %v", err)
	}

	matches, _ := filepath.Glob(fmt.Sprintf("%s/*.lv", builddir))
	if len(matches) > 0 {
		if len(matches) > 1 {
			shared.Debugf("Unexpected - more than one .lv file in builddir. using first: %v", matches)
		}
		lvsymlink := matches[0]
		if lvpath, err := os.Readlink(lvsymlink); err != nil {
			shared.Debugf("Error reading target of symlink '%s'", lvsymlink)
		} else {
			err = shared.LVMRemoveLV(vgname, filepath.Base(lvpath))
			if err != nil {
				shared.Debugf("Error removing LV '%s': %v", lvpath, err)
			}
		}
	}

	if d.BackingFs == "btrfs" {
		/* cannot rm -rf /a if /a/b is a subvolume, so first delete subvolumes */
		/* todo: find the .btrfs file under dir */
		fnamelist, _ := shared.ReadDir(builddir)
		for _, fname := range fnamelist {
			subvol := filepath.Join(builddir, fname)
			exec.Command("btrfs", "subvolume", "delete", subvol).Run()
		}
	}
	if remErr := os.RemoveAll(builddir); remErr != nil {
		shared.Debugf("Error deleting temporary directory: %s", remErr)
	}
}

// We've got an image with the directory, create .btrfs or .lv
func buildOtherFs(d *Daemon, builddir string, fp string) error {
	vgname, vgnameIsSet, err := getServerConfigValue(d, "core.lvm_vg_name")
	if err != nil {
		return fmt.Errorf("Error checking server config: %v", err)
	}

	if vgnameIsSet {
		return createImageLV(d, builddir, fp, vgname)
	}

	switch d.BackingFs {
	case "btrfs":
		imagefname := filepath.Join(builddir, fp)
		subvol := fmt.Sprintf("%s.btrfs", imagefname)
		if err := makeBtrfsSubvol(imagefname, subvol); err != nil {
			return err
		}
	}
	return nil
}

func createImageLV(d *Daemon, builddir string, fingerprint string, vgname string) error {
	imagefname := filepath.Join(builddir, fingerprint)
	poolname, poolnameIsSet, err := getServerConfigValue(d, "core.lvm_thinpool_name")
	if err != nil {
		return fmt.Errorf("Error checking server config: %v", err)
	}

	if !poolnameIsSet {
		poolname, err = shared.LVMCreateDefaultThinPool(vgname)
		if err != nil {
			return fmt.Errorf("Error creating LVM thin pool: %v", err)
		}
		err = setLVMThinPoolNameConfig(d, poolname)
		if err != nil {
			shared.Debugf("Error setting thin pool name: '%s'", err)
			return fmt.Errorf("Error setting LVM thin pool config: %v", err)
		}
	}

	lvpath, err := shared.LVMCreateThinLV(fingerprint, poolname, vgname)
	if err != nil {
		shared.Logf("Error from LVMCreateThinLV: '%v'", err)
		return fmt.Errorf("Error Creating LVM LV for new image: %v", err)
	}

	err = os.Symlink(lvpath, fmt.Sprintf("%s.lv", imagefname))
	if err != nil {
		return err
	}

	output, err := exec.Command("mkfs.ext4", "-E", "nodiscard,lazy_itable_init=0,lazy_journal_init=0", lvpath).CombinedOutput()
	if err != nil {
		shared.Logf("Error output from mkfs.ext4: '%s'", output)
		return fmt.Errorf("Error making filesystem on image LV: %v", err)
	}

	tempLVMountPoint, err := ioutil.TempDir(builddir, "tmp_lv_mnt")
	if err != nil {
		return err
	}

	output, err = exec.Command("mount", "-o", "discard", lvpath, tempLVMountPoint).CombinedOutput()
	if err != nil {
		shared.Logf("Error mounting image LV for untarring: '%s'", output)
		return fmt.Errorf("Error mounting image LV: %v", err)

	}

	untar_err := untarImage(imagefname, tempLVMountPoint)

	output, err = exec.Command("umount", tempLVMountPoint).CombinedOutput()
	if err != nil {
		shared.Logf("WARNING: could not unmount LV '%s' from '%s'. Will not remove. Error: %v", lvpath, tempLVMountPoint, err)
		if untar_err == nil {
			return err
		} else {
			return fmt.Errorf("Error unmounting '%s' during cleanup of error %v", tempLVMountPoint, untar_err)
		}
	}

	return untar_err
}

// Copy imagefile and btrfs file out of the tmpdir
func pullOutImagefiles(d *Daemon, builddir string, fingerprint string) error {
	imagefname := filepath.Join(builddir, fingerprint)
	finalName := shared.VarPath("images", fingerprint)

	err := os.Rename(imagefname, finalName)
	if err != nil {
		return err
	}

	lvsymlink := fmt.Sprintf("%s.lv", imagefname)
	if shared.PathExists(lvsymlink) {
		dst := shared.VarPath("images", fmt.Sprintf("%s.lv", fingerprint))
		return os.Rename(lvsymlink, dst)
	}

	switch d.BackingFs {
	case "btrfs":
		subvol := fmt.Sprintf("%s.btrfs", imagefname)
		dst := shared.VarPath("images", fmt.Sprintf("%s.btrfs", fingerprint))
		if err := os.Rename(subvol, dst); err != nil {
			return err
		}
	}

	return nil
}

func dbInsertImage(d *Daemon, fp string, fname string, sz int64, public int,
	arch int, creation_date int64, expiry_date int64, properties map[string]string) error {
	tx, err := shared.DbBegin(d.db)
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`INSERT INTO images (fingerprint, filename, size, public, architecture, creation_date, expiry_date, upload_date) VALUES (?, ?, ?, ?, ?, ?, ?, strftime("%s"))`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	result, err := stmt.Exec(fp, fname, sz, public, arch, creation_date, expiry_date)
	if err != nil {
		tx.Rollback()
		return err
	}

	if len(properties) > 0 {

		id64, err := result.LastInsertId()
		if err != nil {
			tx.Rollback()
			return err
		}
		id := int(id64)

		pstmt, err := tx.Prepare(`INSERT INTO images_properties (image_id, type, key, value) VALUES (?, 0, ?, ?)`)
		if err != nil {
			tx.Rollback()
			return err
		}
		defer pstmt.Close()

		for k, v := range properties {

			// we can assume, that there is just one
			// value per key
			_, err = pstmt.Exec(id, k, v)
			if err != nil {
				tx.Rollback()
				return err
			}
		}

	}

	if err := shared.TxCommit(tx); err != nil {
		return err
	}

	return nil
}

func imagesPost(d *Daemon, r *http.Request) Response {

	dirname := shared.VarPath("images")
	if err := os.MkdirAll(dirname, 0700); err != nil {
		return InternalError(err)
	}

	// create a directory under which we keep everything while building
	builddir, err := ioutil.TempDir(dirname, "lxd_build_")
	if err != nil {
		return InternalError(err)
	}

	/* remove the builddir when done */
	defer removeImgWorkdir(d, builddir)

	/* Grab all info from the web request */

	info, err := getImgPostInfo(d, r, builddir)
	if err != nil {
		return SmartError(err)
	}

	metadata, err := buildImageFromInfo(d, info, builddir)
	if err != nil {
		return SmartError(err)
	}
	return SyncResponse(true, metadata)
}

func buildImageFromInfo(d *Daemon, info shared.ImageInfo, builddir string) (metadata map[string]string, err error) {
	if err := buildOtherFs(d, builddir, info.Fingerprint); err != nil {
		return nil, err
	}

	err = dbInsertImage(d, info.Fingerprint, info.Filename, info.Size, info.Public, info.Architecture, info.CreationDate, info.ExpiryDate, info.Properties)
	if err != nil {
		return nil, err
	}

	metadata = make(map[string]string)
	metadata["fingerprint"] = info.Fingerprint
	metadata["size"] = strconv.FormatInt(info.Size, 10)

	err = pullOutImagefiles(d, builddir, info.Fingerprint)
	if err != nil {
		return nil, err
	}

	// now we can let the deferred cleanup fn remove the tmpdir

	return metadata, nil
}

func xzReader(r io.Reader) io.ReadCloser {
	rpipe, wpipe := io.Pipe()

	cmd := exec.Command("xz", "--decompress", "--stdout")
	cmd.Stdin = r
	cmd.Stdout = wpipe

	go func() {
		err := cmd.Run()
		wpipe.CloseWithError(err)
	}()

	return rpipe
}

func getImageMetadata(fname string) (*imageMetadata, error) {

	metadataName := "metadata.yaml"

	compression, _, err := detectCompression(fname)

	if err != nil {
		return nil, err
	}

	args := []string{"-O"}
	switch compression {
	case COMPRESSION_TAR:
		args = append(args, "-xf")
	case COMPRESSION_GZIP:
		args = append(args, "-zxf")
	case COMPRESSION_BZ2:
		args = append(args, "--jxf")
	case COMPRESSION_LZMA:
		args = append(args, "--lzma", "-xf")
	default:
		args = append(args, "-Jxf")
	}
	args = append(args, fname, metadataName)

	shared.Debugf("Extracting tarball using command: tar %s", strings.Join(args, " "))

	// read the metadata.yaml
	output, err := exec.Command("tar", args...).CombinedOutput()

	if err != nil {
		outputLines := strings.Split(string(output), "\n")
		return nil, fmt.Errorf("Could not extract image metadata %s from tar: %v (%s)", metadataName, err, outputLines[0])
	}

	metadata := new(imageMetadata)
	err = yaml.Unmarshal(output, &metadata)

	if err != nil {
		return nil, fmt.Errorf("Could not parse %s: %v", metadataName, err)
	}

	return metadata, nil
}

func imagesGet(d *Daemon, r *http.Request) Response {
	public := !d.isTrustedClient(r)

	result, err := doImagesGet(d, d.isRecursionRequest(r), public)
	if err != nil {
		return SmartError(err)
	}
	return SyncResponse(true, result)
}

func doImagesGet(d *Daemon, recursion bool, public bool) (interface{}, error) {
	result_string := make([]string, 0)
	result_map := make([]shared.ImageInfo, 0)

	q := "SELECT fingerprint FROM images"
	var name string
	if public == true {
		q = "SELECT fingerprint FROM images WHERE public=1"
	}
	inargs := []interface{}{}
	outfmt := []interface{}{name}
	results, err := shared.DbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	for _, r := range results {
		name = r[0].(string)
		if !recursion {
			url := fmt.Sprintf("/%s/images/%s", shared.APIVersion, name)
			result_string = append(result_string, url)
		} else {
			image, response := doImageGet(d, name, public)
			if response != nil {
				continue
			}
			result_map = append(result_map, image)
		}
	}

	if !recursion {
		return result_string, nil
	} else {
		return result_map, nil
	}
}

var imagesCmd = Command{name: "images", post: imagesPost, untrustedGet: true, get: imagesGet}

func imageDelete(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	imgInfo, err := dbImageGet(d.db, fingerprint, false)
	if err != nil {
		return SmartError(err)
	}

	fname := shared.VarPath("images", imgInfo.Fingerprint)
	err = os.Remove(fname)
	if err != nil {
		shared.Debugf("Error deleting image file %s: %s\n", fname, err)
	}

	vgname, vgnameIsSet, err := getServerConfigValue(d, "core.lvm_vg_name")
	if err != nil {
		return InternalError(fmt.Errorf("Error checking server config: %v", err))
	}

	if vgnameIsSet {
		err = shared.LVMRemoveLV(vgname, imgInfo.Fingerprint)
		if err != nil {
			return InternalError(fmt.Errorf("Failed to remove deleted image LV: %v", err))
		}

		lvsymlink := fmt.Sprintf("%s.lv", fname)
		err = os.Remove(lvsymlink)
		if err != nil {
			return InternalError(fmt.Errorf("Failed to remove symlink to deleted image LV: '%s': %v", lvsymlink, err))
		}
	} else if d.BackingFs == "btrfs" {
		subvol := fmt.Sprintf("%s.btrfs", fname)
		exec.Command("btrfs", "subvolume", "delete", subvol).Run()
	}

	tx, err := shared.DbBegin(d.db)
	if err != nil {
		return InternalError(err)
	}

	_, _ = tx.Exec("DELETE FROM images_aliases WHERE image_id=?", imgInfo.Id)
	_, _ = tx.Exec("DELETE FROM images_properties WHERE image_id?", imgInfo.Id)
	_, _ = tx.Exec("DELETE FROM images WHERE id=?", imgInfo.Id)

	if err := shared.TxCommit(tx); err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

func doImageGet(d *Daemon, fingerprint string, public bool) (shared.ImageInfo, Response) {
	imgInfo, err := dbImageGet(d.db, fingerprint, public)
	if err != nil {
		return shared.ImageInfo{}, SmartError(err)
	}

	q := "SELECT key, value FROM images_properties where image_id=?"
	var key, value, name, desc string
	inargs := []interface{}{imgInfo.Id}
	outfmt := []interface{}{key, value}
	results, err := shared.DbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return shared.ImageInfo{}, SmartError(err)
	}
	properties := map[string]string{}
	for _, r := range results {
		key = r[0].(string)
		value = r[1].(string)
		properties[key] = value
	}

	q = "SELECT name, description FROM images_aliases WHERE image_id=?"
	inargs = []interface{}{imgInfo.Id}
	outfmt = []interface{}{name, desc}
	results, err = shared.DbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return shared.ImageInfo{}, InternalError(err)
	}
	aliases := shared.ImageAliases{}
	for _, r := range results {
		name = r[0].(string)
		desc = r[0].(string)
		a := shared.ImageAlias{Name: name, Description: desc}
		aliases = append(aliases, a)
	}

	info := shared.ImageInfo{Fingerprint: imgInfo.Fingerprint,
		Filename:     imgInfo.Filename,
		Properties:   properties,
		Aliases:      aliases,
		Public:       imgInfo.Public,
		Size:         imgInfo.Size,
		Architecture: imgInfo.Architecture,
		CreationDate: imgInfo.CreationDate,
		ExpiryDate:   imgInfo.ExpiryDate,
		UploadDate:   imgInfo.UploadDate}

	return info, nil
}

func imageValidSecret(fingerprint string, secret string) bool {
	lock.Lock()
	for _, op := range operations {
		if op.Resources == nil {
			continue
		}

		opImages, ok := op.Resources["images"]
		if ok == false {
			continue
		}

		found := false
		for img := range opImages {
			toScan := strings.Replace(opImages[img], "/", " ", -1)
			imgVersion := ""
			imgFingerprint := ""
			count, err := fmt.Sscanf(toScan, " %s images %s", &imgVersion, &imgFingerprint)
			if err != nil || count != 2 {
				continue
			}

			if imgFingerprint == fingerprint {
				found = true
				break
			}
		}

		if found == false {
			continue
		}

		opMetadata, err := op.MetadataAsMap()
		if err != nil {
			continue
		}

		opSecret, err := opMetadata.GetString("secret")
		if err != nil {
			continue
		}

		if opSecret == secret {
			lock.Unlock()
			return true
		}
	}
	lock.Unlock()

	return false
}

func imageGet(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]
	public := !d.isTrustedClient(r)
	secret := r.FormValue("secret")

	if public == true && imageValidSecret(fingerprint, secret) == true {
		public = false
	}

	info, response := doImageGet(d, fingerprint, public)
	if response != nil {
		return response
	}

	return SyncResponse(true, info)
}

type imagePutReq struct {
	Properties map[string]string `json:"properties"`
}

func imagePut(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	imageRaw := imagePutReq{}
	if err := json.NewDecoder(r.Body).Decode(&imageRaw); err != nil {
		return BadRequest(err)
	}

	imgInfo, err := dbImageGet(d.db, fingerprint, false)
	if err != nil {
		return SmartError(err)
	}

	tx, err := shared.DbBegin(d.db)
	if err != nil {
		return InternalError(err)
	}

	_, err = tx.Exec(`DELETE FROM images_properties WHERE image_id=?`, imgInfo.Id)

	stmt, err := tx.Prepare(`INSERT INTO images_properties (image_id, type, key, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return InternalError(err)
	}

	for key, value := range imageRaw.Properties {
		_, err = stmt.Exec(imgInfo.Id, 0, key, value)
		if err != nil {
			tx.Rollback()
			return InternalError(err)
		}
	}

	if err := shared.TxCommit(tx); err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

var imageCmd = Command{name: "images/{fingerprint}", untrustedGet: true, get: imageGet, put: imagePut, delete: imageDelete}

type aliasPostReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Target      string `json:"target"`
}

func aliasesPost(d *Daemon, r *http.Request) Response {
	req := aliasPostReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	if req.Name == "" || req.Target == "" {
		return BadRequest(fmt.Errorf("name and target are required"))
	}
	if req.Description == "" {
		req.Description = req.Name
	}

	// This is just to see if the alias name already exists.
	_, err := dbAliasGet(d.db, req.Name)
	if err == nil {
		return Conflict
	}

	imgInfo, err := dbImageGet(d.db, req.Target, false)
	if err != nil {
		return SmartError(err)
	}

	err = dbAddAlias(d.db, req.Name, imgInfo.Id, req.Description)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

func aliasesGet(d *Daemon, r *http.Request) Response {
	recursion := d.isRecursionRequest(r)

	q := "SELECT name FROM images_aliases"
	var name string
	inargs := []interface{}{}
	outfmt := []interface{}{name}
	results, err := shared.DbQueryScan(d.db, q, inargs, outfmt)
	if err != nil {
		return BadRequest(err)
	}
	response_str := make([]string, 0)
	response_map := make([]shared.ImageAlias, 0)
	for _, res := range results {
		name = res[0].(string)
		if !recursion {
			url := fmt.Sprintf("/%s/images/aliases/%s", shared.APIVersion, name)
			response_str = append(response_str, url)

		} else {
			alias, err := doAliasGet(d, name, d.isTrustedClient(r))
			if err != nil {
				continue
			}
			response_map = append(response_map, alias)
		}
	}

	if !recursion {
		return SyncResponse(true, response_str)
	} else {
		return SyncResponse(true, response_map)
	}
}

func aliasGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	alias, err := doAliasGet(d, name, d.isTrustedClient(r))
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, alias)
}

func doAliasGet(d *Daemon, name string, isTrustedClient bool) (shared.ImageAlias, error) {
	q := `SELECT images.fingerprint, images_aliases.description
			 FROM images_aliases
			 INNER JOIN images
			 ON images_aliases.image_id=images.id
			 WHERE images_aliases.name=?`
	if !isTrustedClient {
		q = q + ` AND images.public=1`
	}

	var fingerprint, description string
	arg1 := []interface{}{name}
	arg2 := []interface{}{&fingerprint, &description}
	err := shared.DbQueryRowScan(d.db, q, arg1, arg2)
	if err != nil {
		return shared.ImageAlias{}, err
	}

	return shared.ImageAlias{Name: fingerprint, Description: description}, nil
}

func aliasDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	_, _ = shared.DbExec(d.db, "DELETE FROM images_aliases WHERE name=?", name)

	return EmptySyncResponse
}

func imageExport(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]

	public := !d.isTrustedClient(r)
	secret := r.FormValue("secret")

	if public == true && imageValidSecret(fingerprint, secret) == true {
		public = false
	}

	imgInfo, err := dbImageGet(d.db, fingerprint, public)
	if err != nil {
		return SmartError(err)
	}

	path := shared.VarPath("images", imgInfo.Fingerprint)
	filename := imgInfo.Filename

	if filename == "" {
		_, ext, err := detectCompression(path)
		if err != nil {
			ext = ""
		}
		filename = fmt.Sprintf("%s%s", fingerprint, ext)
	}

	headers := map[string]string{
		"Content-Disposition": fmt.Sprintf("inline;filename=%s", filename),
	}

	return FileResponse(r, path, filename, headers, false)
}

func imageSecret(d *Daemon, r *http.Request) Response {
	fingerprint := mux.Vars(r)["fingerprint"]
	_, err := dbImageGet(d.db, fingerprint, false)
	if err != nil {
		return SmartError(err)
	}

	secret, err := shared.RandomCryptoString()

	if err != nil {
		return InternalError(err)
	}

	meta := shared.Jmap{}
	meta["secret"] = secret

	resources := make(map[string][]string)
	resources["images"] = []string{fingerprint}
	return &asyncResponse{resources: resources, metadata: meta}
}

var imagesExportCmd = Command{name: "images/{fingerprint}/export", untrustedGet: true, get: imageExport}
var imagesSecretCmd = Command{name: "images/{fingerprint}/secret", post: imageSecret}

var aliasesCmd = Command{name: "images/aliases", post: aliasesPost, get: aliasesGet}

var aliasCmd = Command{name: "images/aliases/{name:.*}", untrustedGet: true, get: aliasGet, delete: aliasDelete}
