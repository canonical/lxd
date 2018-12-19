package lxd

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// Image handling functions

// GetImages returns a list of available images as Image structs
func (r *ProtocolSimpleStreams) GetImages() ([]api.Image, error) {
	return r.ssClient.ListImages()
}

// GetImageFingerprints returns a list of available image fingerprints
func (r *ProtocolSimpleStreams) GetImageFingerprints() ([]string, error) {
	// Get all the images from simplestreams
	images, err := r.ssClient.ListImages()
	if err != nil {
		return nil, err
	}

	// And now extract just the fingerprints
	fingerprints := []string{}
	for _, img := range images {
		fingerprints = append(fingerprints, img.Fingerprint)
	}

	return fingerprints, nil
}

// GetImage returns an Image struct for the provided fingerprint
func (r *ProtocolSimpleStreams) GetImage(fingerprint string) (*api.Image, string, error) {
	image, err := r.ssClient.GetImage(fingerprint)
	if err != nil {
		return nil, "", err
	}

	return image, "", err
}

// GetImageFile downloads an image from the server, returning an ImageFileResponse struct
func (r *ProtocolSimpleStreams) GetImageFile(fingerprint string, req ImageFileRequest) (*ImageFileResponse, error) {
	// Sanity checks
	if req.MetaFile == nil && req.RootfsFile == nil {
		return nil, fmt.Errorf("No file requested")
	}

	// Attempt to download from host
	if shared.PathExists("/dev/lxd/sock") && os.Geteuid() == 0 {
		unixURI := fmt.Sprintf("http://unix.socket/1.0/images/%s/export", url.QueryEscape(fingerprint))

		// Setup the HTTP client
		devlxdHTTP, err := unixHTTPClient(nil, "/dev/lxd/sock")
		if err == nil {
			resp, err := lxdDownloadImage(fingerprint, unixURI, r.httpUserAgent, devlxdHTTP, req)
			if err == nil {
				return resp, nil
			}
		}
	}

	// Get the file list
	files, err := r.ssClient.GetFiles(fingerprint)
	if err != nil {
		return nil, err
	}

	// Prepare the response
	resp := ImageFileResponse{}

	// Download function
	download := func(path string, filename string, hash string, target io.WriteSeeker) (int64, error) {
		// Try over http
		url := fmt.Sprintf("http://%s/%s", strings.TrimPrefix(r.httpHost, "https://"), path)

		size, err := shared.DownloadFileHash(r.http, r.httpUserAgent, req.ProgressHandler, req.Canceler, filename, url, hash, sha256.New(), target)
		if err != nil {
			// Handle cancelation
			if err.Error() == "net/http: request canceled" {
				return -1, err
			}

			// Try over https
			url = fmt.Sprintf("%s/%s", r.httpHost, path)
			size, err = shared.DownloadFileHash(r.http, r.httpUserAgent, req.ProgressHandler, req.Canceler, filename, url, hash, sha256.New(), target)
			if err != nil {
				return -1, err
			}
		}

		return size, nil
	}

	// Download the LXD image file
	meta, ok := files["meta"]
	if ok && req.MetaFile != nil {
		size, err := download(meta.Path, "metadata", meta.Sha256, req.MetaFile)
		if err != nil {
			return nil, err
		}

		parts := strings.Split(meta.Path, "/")
		resp.MetaName = parts[len(parts)-1]
		resp.MetaSize = size
	}

	// Download the rootfs
	rootfs, ok := files["root"]
	if ok && req.RootfsFile != nil {
		// Look for deltas (requires xdelta3)
		downloaded := false
		_, err := exec.LookPath("xdelta3")
		if err == nil && req.DeltaSourceRetriever != nil {
			for filename, file := range files {
				if !strings.HasPrefix(filename, "root.delta-") {
					continue
				}

				// Check if we have the source file for the delta
				srcFingerprint := strings.Split(filename, "root.delta-")[1]
				srcPath := req.DeltaSourceRetriever(srcFingerprint, "rootfs")
				if srcPath == "" {
					continue
				}

				// Create temporary file for the delta
				deltaFile, err := ioutil.TempFile("", "lxc_image_")
				if err != nil {
					return nil, err
				}
				defer deltaFile.Close()
				defer os.Remove(deltaFile.Name())

				// Download the delta
				_, err = download(file.Path, "rootfs delta", file.Sha256, deltaFile)
				if err != nil {
					return nil, err
				}

				// Create temporary file for the delta
				patchedFile, err := ioutil.TempFile("", "lxc_image_")
				if err != nil {
					return nil, err
				}
				defer patchedFile.Close()
				defer os.Remove(patchedFile.Name())

				// Apply it
				_, err = shared.RunCommand("xdelta3", "-f", "-d", "-s", srcPath, deltaFile.Name(), patchedFile.Name())
				if err != nil {
					return nil, err
				}

				// Copy to the target
				size, err := io.Copy(req.RootfsFile, patchedFile)
				if err != nil {
					return nil, err
				}

				parts := strings.Split(rootfs.Path, "/")
				resp.RootfsName = parts[len(parts)-1]
				resp.RootfsSize = size
				downloaded = true
			}
		}

		// Download the whole file
		if !downloaded {
			size, err := download(rootfs.Path, "rootfs", rootfs.Sha256, req.RootfsFile)
			if err != nil {
				return nil, err
			}

			parts := strings.Split(rootfs.Path, "/")
			resp.RootfsName = parts[len(parts)-1]
			resp.RootfsSize = size
		}
	}

	return &resp, nil
}

// GetImageSecret isn't relevant for the simplestreams protocol
func (r *ProtocolSimpleStreams) GetImageSecret(fingerprint string) (string, error) {
	return "", fmt.Errorf("Private images aren't supported by the simplestreams protocol")
}

// GetPrivateImage isn't relevant for the simplestreams protocol
func (r *ProtocolSimpleStreams) GetPrivateImage(fingerprint string, secret string) (*api.Image, string, error) {
	return nil, "", fmt.Errorf("Private images aren't supported by the simplestreams protocol")
}

// GetPrivateImageFile isn't relevant for the simplestreams protocol
func (r *ProtocolSimpleStreams) GetPrivateImageFile(fingerprint string, secret string, req ImageFileRequest) (*ImageFileResponse, error) {
	return nil, fmt.Errorf("Private images aren't supported by the simplestreams protocol")
}

// GetImageAliases returns the list of available aliases as ImageAliasesEntry structs
func (r *ProtocolSimpleStreams) GetImageAliases() ([]api.ImageAliasesEntry, error) {
	return r.ssClient.ListAliases()
}

// GetImageAliasNames returns the list of available alias names
func (r *ProtocolSimpleStreams) GetImageAliasNames() ([]string, error) {
	// Get all the images from simplestreams
	aliases, err := r.ssClient.ListAliases()
	if err != nil {
		return nil, err
	}

	// And now extract just the names
	names := []string{}
	for _, alias := range aliases {
		names = append(names, alias.Name)
	}

	return names, nil
}

// GetImageAlias returns an existing alias as an ImageAliasesEntry struct
func (r *ProtocolSimpleStreams) GetImageAlias(name string) (*api.ImageAliasesEntry, string, error) {
	alias, err := r.ssClient.GetAlias(name)
	if err != nil {
		return nil, "", err
	}

	return alias, "", err
}
