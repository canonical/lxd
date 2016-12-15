package lxd

import (
	"crypto/sha256"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
)

// Image handling functions

// GetImages returns a list of available images as Image structs
func (r *ProtocolLXD) GetImages() ([]api.Image, error) {
	images := []api.Image{}

	_, err := r.queryStruct("GET", "/images?recursion=1", nil, "", &images)
	if err != nil {
		return nil, err
	}

	return images, nil
}

// GetImageFingerprints returns a list of available image fingerprints
func (r *ProtocolLXD) GetImageFingerprints() ([]string, error) {
	urls := []string{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/images", nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it
	fingerprints := []string{}
	for _, url := range urls {
		fields := strings.Split(url, "/images/")
		fingerprints = append(fingerprints, fields[len(fields)-1])
	}

	return fingerprints, nil
}

// GetImage returns an Image struct for the provided fingerprint
func (r *ProtocolLXD) GetImage(fingerprint string) (*api.Image, string, error) {
	return r.GetPrivateImage(fingerprint, "")
}

// GetImageFile downloads an image from the server, returning an ImageFileRequest struct
func (r *ProtocolLXD) GetImageFile(fingerprint string, req ImageFileRequest) (*ImageFileResponse, error) {
	return r.GetPrivateImageFile(fingerprint, "", req)
}

// GetPrivateImage is similar to GetImage but allows passing a secret download token
func (r *ProtocolLXD) GetPrivateImage(fingerprint string, secret string) (*api.Image, string, error) {
	image := api.Image{}

	// Build the API path
	path := fmt.Sprintf("/images/%s", fingerprint)
	if secret != "" {
		path = fmt.Sprintf("%s?secret=%s", path, secret)
	}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", path, nil, "", &image)
	if err != nil {
		return nil, "", err
	}

	return &image, etag, nil
}

// GetPrivateImageFile is similar to GetImageFile but allows passing a secret download token
func (r *ProtocolLXD) GetPrivateImageFile(fingerprint string, secret string, req ImageFileRequest) (*ImageFileResponse, error) {
	// Sanity checks
	if req.MetaFile == nil && req.RootfsFile == nil {
		return nil, fmt.Errorf("No file requested")
	}

	// Prepare the response
	resp := ImageFileResponse{}

	// Build the URL
	url := fmt.Sprintf("%s/1.0/images/%s/export", r.httpHost, fingerprint)
	if secret != "" {
		url = fmt.Sprintf("%s?secret=%s", url, secret)
	}

	// Prepare the download request
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	if r.httpUserAgent != "" {
		request.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Start the request
	response, err := r.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Unable to fetch %s: %s", url, response.Status)
	}

	ctype, ctypeParams, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		ctype = "application/octet-stream"
	}

	// Handle the data
	body := response.Body
	if req.ProgressHandler != nil {
		body = &ioprogress.ProgressReader{
			ReadCloser: response.Body,
			Tracker: &ioprogress.ProgressTracker{
				Length: response.ContentLength,
				Handler: func(percent int64, speed int64) {
					req.ProgressHandler(ProgressData{Text: fmt.Sprintf("%d%% (%s/s)", percent, shared.GetByteSizeString(speed, 2))})
				},
			},
		}
	}

	// Hashing
	sha256 := sha256.New()

	// Deal with split images
	if ctype == "multipart/form-data" {
		if req.MetaFile == nil || req.RootfsFile == nil {
			return nil, fmt.Errorf("Multi-part image but only one target file provided")
		}

		// Parse the POST data
		mr := multipart.NewReader(body, ctypeParams["boundary"])

		// Get the metadata tarball
		part, err := mr.NextPart()
		if err != nil {
			return nil, err
		}

		if part.FormName() != "metadata" {
			return nil, fmt.Errorf("Invalid multipart image")
		}

		size, err := io.Copy(io.MultiWriter(req.MetaFile, sha256), part)
		if err != nil {
			return nil, err
		}
		resp.MetaSize = size
		resp.MetaName = part.FileName()

		// Get the rootfs tarball
		part, err = mr.NextPart()
		if err != nil {
			return nil, err
		}

		if part.FormName() != "rootfs" {
			return nil, fmt.Errorf("Invalid multipart image")
		}

		size, err = io.Copy(io.MultiWriter(req.RootfsFile, sha256), part)
		if err != nil {
			return nil, err
		}
		resp.RootfsSize = size
		resp.RootfsName = part.FileName()

		// Check the hash
		hash := fmt.Sprintf("%x", sha256.Sum(nil))
		if hash != fingerprint {
			return nil, fmt.Errorf("Image fingerprint doesn't match. Got %s expected %s", hash, fingerprint)
		}

		return &resp, nil
	}

	// Deal with unified images
	_, cdParams, err := mime.ParseMediaType(response.Header.Get("Content-Disposition"))
	if err != nil {
		return nil, err
	}

	filename, ok := cdParams["filename"]
	if !ok {
		return nil, fmt.Errorf("No filename in Content-Disposition header")
	}

	size, err := io.Copy(io.MultiWriter(req.MetaFile, sha256), body)
	if err != nil {
		return nil, err
	}
	resp.MetaSize = size
	resp.MetaName = filename

	// Check the hash
	hash := fmt.Sprintf("%x", sha256.Sum(nil))
	if hash != fingerprint {
		return nil, fmt.Errorf("Image fingerprint doesn't match. Got %s expected %s", hash, fingerprint)
	}

	return &resp, nil
}

// GetImageAliases returns the list of available aliases as ImageAliasesEntry structs
func (r *ProtocolLXD) GetImageAliases() ([]api.ImageAliasesEntry, error) {
	aliases := []api.ImageAliasesEntry{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/images/aliases?recursion=1", nil, "", aliases)
	if err != nil {
		return nil, err
	}

	return aliases, nil
}

// GetImageAliasNames returns the list of available alias names
func (r *ProtocolLXD) GetImageAliasNames() ([]string, error) {
	urls := []string{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/images/aliases", nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it
	names := []string{}
	for _, url := range urls {
		fields := strings.Split(url, "/images/aliases/")
		names = append(names, fields[len(fields)-1])
	}

	return names, nil
}

// GetImageAlias returns an existing alias as an ImageAliasesEntry struct
func (r *ProtocolLXD) GetImageAlias(name string) (*api.ImageAliasesEntry, string, error) {
	alias := api.ImageAliasesEntry{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/images/aliases/%s", name), nil, "", &alias)
	if err != nil {
		return nil, "", err
	}

	return &alias, etag, nil
}

// CreateImage requests that LXD creates, copies or import a new image
func (r *ProtocolLXD) CreateImage(image api.ImagesPost) (*Operation, error) {
	if image.CompressionAlgorithm != "" {
		if !r.HasExtension("image_compression_algorithm") {
			return nil, fmt.Errorf("The server is missing the required \"image_compression_algorithm\" API extension")
		}
	}

	// Send the request
	op, _, err := r.queryOperation("POST", "/images", image, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// CopyImage copies an existing image to a remote server. Additional options can be passed using ImageCopyArgs
func (r *ProtocolLXD) CopyImage(image api.Image, target ContainerServer, args *ImageCopyArgs) (*Operation, error) {
	// Prepare the copy request
	req := api.ImagesPost{
		Source: &api.ImagesPostSource{
			ImageSource: api.ImageSource{
				Certificate: r.httpCertificate,
				Protocol:    "lxd",
				Server:      r.httpHost,
			},
			Fingerprint: image.Fingerprint,
			Mode:        "pull",
			Type:        "image",
		},
	}

	// Process the arguments
	if args != nil {
		req.Aliases = args.Aliases
		req.AutoUpdate = args.AutoUpdate
		req.Public = args.Public

		if args.CopyAliases {
			req.Aliases = image.Aliases
			if args.Aliases != nil {
				req.Aliases = append(req.Aliases, args.Aliases...)
			}
		}
	}

	return target.CreateImage(req)
}

// UpdateImage updates the image definition
func (r *ProtocolLXD) UpdateImage(fingerprint string, image api.ImagePut, ETag string) error {
	// Send the request
	_, _, err := r.query("PUT", fmt.Sprintf("/images/%s", fingerprint), image, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteImage requests that LXD removes an image from the store
func (r *ProtocolLXD) DeleteImage(fingerprint string) (*Operation, error) {
	// Send the request
	op, _, err := r.queryOperation("DELETE", fmt.Sprintf("/images/%s", fingerprint), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// CreateImageSecret requests that LXD issues a temporary image secret
func (r *ProtocolLXD) CreateImageSecret(fingerprint string) (*Operation, error) {
	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/images/%s/secret", fingerprint), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// CreateImageAlias sets up a new image alias
func (r *ProtocolLXD) CreateImageAlias(alias api.ImageAliasesPost) error {
	// Send the request
	_, _, err := r.query("POST", "/images/aliases", alias, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateImageAlias updates the image alias definition
func (r *ProtocolLXD) UpdateImageAlias(name string, alias api.ImageAliasesEntryPut, ETag string) error {
	// Send the request
	_, _, err := r.query("PUT", fmt.Sprintf("/images/aliases/%s", name), alias, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameImageAlias renames an existing image alias
func (r *ProtocolLXD) RenameImageAlias(name string, alias api.ImageAliasesEntryPost) error {
	// Send the request
	_, _, err := r.query("POST", fmt.Sprintf("/images/aliases/%s", name), alias, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteImageAlias removes an alias from the LXD image store
func (r *ProtocolLXD) DeleteImageAlias(name string) error {
	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/images/aliases/%s", name), nil, "")
	if err != nil {
		return err
	}

	return nil
}
