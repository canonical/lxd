package lxd

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/cancel"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/units"
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

// GetImageSecret is a helper around CreateImageSecret that returns a secret for the image
func (r *ProtocolLXD) GetImageSecret(fingerprint string) (string, error) {
	op, err := r.CreateImageSecret(fingerprint)
	if err != nil {
		return "", err
	}
	opAPI := op.Get()

	return opAPI.Metadata["secret"].(string), nil
}

// GetPrivateImage is similar to GetImage but allows passing a secret download token
func (r *ProtocolLXD) GetPrivateImage(fingerprint string, secret string) (*api.Image, string, error) {
	image := api.Image{}

	// Build the API path
	path := fmt.Sprintf("/images/%s", url.PathEscape(fingerprint))
	var err error
	path, err = r.setQueryAttributes(path)
	if err != nil {
		return nil, "", err
	}

	if secret != "" {
		path, err = setQueryParam(path, "secret", secret)
		if err != nil {
			return nil, "", err
		}
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

	uri := fmt.Sprintf("/1.0/images/%s/export", url.PathEscape(fingerprint))

	var err error
	uri, err = r.setQueryAttributes(uri)
	if err != nil {
		return nil, err
	}

	// Attempt to download from host
	if secret == "" && shared.PathExists("/dev/lxd/sock") && os.Geteuid() == 0 {
		unixURI := fmt.Sprintf("http://unix.socket%s", uri)

		// Setup the HTTP client
		devlxdHTTP, err := unixHTTPClient(nil, "/dev/lxd/sock")
		if err == nil {
			resp, err := lxdDownloadImage(fingerprint, unixURI, r.httpUserAgent, devlxdHTTP, req)
			if err == nil {
				return resp, nil
			}
		}
	}

	// Build the URL
	uri = fmt.Sprintf("%s%s", r.httpHost, uri)
	if secret != "" {
		uri, err = setQueryParam(uri, "secret", secret)
		if err != nil {
			return nil, err
		}
	}

	return lxdDownloadImage(fingerprint, uri, r.httpUserAgent, r.http, req)
}

func lxdDownloadImage(fingerprint string, uri string, userAgent string, client *http.Client, req ImageFileRequest) (*ImageFileResponse, error) {
	// Prepare the response
	resp := ImageFileResponse{}

	// Prepare the download request
	request, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return nil, err
	}

	if userAgent != "" {
		request.Header.Set("User-Agent", userAgent)
	}

	// Start the request
	response, doneCh, err := cancel.CancelableDownload(req.Canceler, client, request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	defer close(doneCh)

	if response.StatusCode != http.StatusOK {
		_, _, err := lxdParseResponse(response)
		if err != nil {
			return nil, err
		}
	}

	ctype, ctypeParams, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		ctype = "application/octet-stream"
	}

	// Handle the data
	body := response.Body
	if req.ProgressHandler != nil {
		reader := &ioprogress.ProgressReader{
			ReadCloser: response.Body,
			Tracker: &ioprogress.ProgressTracker{
				Length: response.ContentLength,
			},
		}

		if response.ContentLength > 0 {
			reader.Tracker.Handler = func(percent int64, speed int64) {
				req.ProgressHandler(ioprogress.ProgressData{Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2))})
			}
		} else {
			reader.Tracker.Handler = func(received int64, speed int64) {
				req.ProgressHandler(ioprogress.ProgressData{Text: fmt.Sprintf("%s (%s/s)", units.GetByteSizeString(received, 2), units.GetByteSizeString(speed, 2))})

			}
		}

		body = reader
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

		if !shared.StringInSlice(part.FormName(), []string{"rootfs", "rootfs.img"}) {
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
		if !strings.HasPrefix(hash, fingerprint) {
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
	if !strings.HasPrefix(hash, fingerprint) {
		return nil, fmt.Errorf("Image fingerprint doesn't match. Got %s expected %s", hash, fingerprint)
	}

	return &resp, nil
}

// GetImageAliases returns the list of available aliases as ImageAliasesEntry structs
func (r *ProtocolLXD) GetImageAliases() ([]api.ImageAliasesEntry, error) {
	aliases := []api.ImageAliasesEntry{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/images/aliases?recursion=1", nil, "", &aliases)
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
	etag, err := r.queryStruct("GET", fmt.Sprintf("/images/aliases/%s", url.PathEscape(name)), nil, "", &alias)
	if err != nil {
		return nil, "", err
	}

	return &alias, etag, nil
}

// GetImageAliasType returns an existing alias as an ImageAliasesEntry struct
func (r *ProtocolLXD) GetImageAliasType(imageType string, name string) (*api.ImageAliasesEntry, string, error) {
	alias, etag, err := r.GetImageAlias(name)
	if err != nil {
		return nil, "", err
	}

	if imageType != "" {
		if alias.Type == "" {
			alias.Type = "container"
		}

		if alias.Type != imageType {
			return nil, "", fmt.Errorf("Alias doesn't exist for the specified type")
		}
	}

	return alias, etag, nil
}

// GetImageAliasArchitectures returns a map of architectures / targets
func (r *ProtocolLXD) GetImageAliasArchitectures(imageType string, name string) (map[string]*api.ImageAliasesEntry, error) {
	alias, _, err := r.GetImageAliasType(imageType, name)
	if err != nil {
		return nil, err
	}

	img, _, err := r.GetImage(alias.Target)
	if err != nil {
		return nil, err
	}

	return map[string]*api.ImageAliasesEntry{img.Architecture: alias}, nil
}

// CreateImage requests that LXD creates, copies or import a new image
func (r *ProtocolLXD) CreateImage(image api.ImagesPost, args *ImageCreateArgs) (Operation, error) {
	if image.CompressionAlgorithm != "" {
		if !r.HasExtension("image_compression_algorithm") {
			return nil, fmt.Errorf("The server is missing the required \"image_compression_algorithm\" API extension")
		}
	}

	// Send the JSON based request
	if args == nil {
		op, _, err := r.queryOperation("POST", "/images", image, "")
		if err != nil {
			return nil, err
		}

		return op, nil
	}

	// Prepare an image upload
	if args.MetaFile == nil {
		return nil, fmt.Errorf("Metadata file is required")
	}

	// Prepare the body
	var body io.Reader
	var contentType string
	if args.RootfsFile == nil {
		// If unified image, just pass it through
		body = args.MetaFile

		contentType = "application/octet-stream"
	} else {
		// If split image, we need mime encoding
		tmpfile, err := ioutil.TempFile("", "lxc_image_")
		if err != nil {
			return nil, err
		}
		defer os.Remove(tmpfile.Name())

		// Setup the multipart writer
		w := multipart.NewWriter(tmpfile)

		// Metadata file
		fw, err := w.CreateFormFile("metadata", args.MetaName)
		if err != nil {
			return nil, err
		}

		_, err = io.Copy(fw, args.MetaFile)
		if err != nil {
			return nil, err
		}

		// Rootfs file
		if args.Type == "virtual-machine" {
			fw, err = w.CreateFormFile("rootfs.img", args.RootfsName)
		} else {
			fw, err = w.CreateFormFile("rootfs", args.RootfsName)
		}
		if err != nil {
			return nil, err
		}

		_, err = io.Copy(fw, args.RootfsFile)
		if err != nil {
			return nil, err
		}

		// Done writing to multipart
		w.Close()

		// Figure out the size of the whole thing
		size, err := tmpfile.Seek(0, 2)
		if err != nil {
			return nil, err
		}

		_, err = tmpfile.Seek(0, 0)
		if err != nil {
			return nil, err
		}

		// Setup progress handler
		if args.ProgressHandler != nil {
			body = &ioprogress.ProgressReader{
				ReadCloser: tmpfile,
				Tracker: &ioprogress.ProgressTracker{
					Length: size,
					Handler: func(percent int64, speed int64) {
						args.ProgressHandler(ioprogress.ProgressData{Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2))})
					},
				},
			}
		} else {
			body = tmpfile
		}

		contentType = w.FormDataContentType()
	}

	// Prepare the HTTP request
	reqURL, err := r.setQueryAttributes(fmt.Sprintf("%s/1.0/images", r.httpHost))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", reqURL, body)
	if err != nil {
		return nil, err
	}

	// Setup the headers
	req.Header.Set("Content-Type", contentType)
	if image.Public {
		req.Header.Set("X-LXD-public", "true")
	}

	if image.Filename != "" {
		req.Header.Set("X-LXD-filename", image.Filename)
	}

	if len(image.Properties) > 0 {
		imgProps := url.Values{}

		for k, v := range image.Properties {
			imgProps.Set(k, v)
		}

		req.Header.Set("X-LXD-properties", imgProps.Encode())
	}

	// Set the user agent
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	if image.Source != nil && image.Source.Fingerprint != "" && image.Source.Secret != "" && image.Source.Mode == "push" {
		// Set fingerprint
		req.Header.Set("X-LXD-fingerprint", image.Source.Fingerprint)

		// Set secret
		req.Header.Set("X-LXD-secret", image.Source.Secret)
	}

	// Send the request
	resp, err := r.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Handle errors
	response, _, err := lxdParseResponse(resp)
	if err != nil {
		return nil, err
	}

	// Get to the operation
	respOperation, err := response.MetadataAsOperation()
	if err != nil {
		return nil, err
	}

	// Setup an Operation wrapper
	op := operation{
		Operation: *respOperation,
		r:         r,
		chActive:  make(chan bool),
	}

	return &op, nil
}

// tryCopyImage iterates through the source server URLs until one lets it download the image
func (r *ProtocolLXD) tryCopyImage(req api.ImagesPost, urls []string) (RemoteOperation, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("The source server isn't listening on the network")
	}

	rop := remoteOperation{
		chDone: make(chan bool),
	}

	// For older servers, apply the aliases after copy
	if !r.HasExtension("image_create_aliases") && req.Aliases != nil {
		rop.chPost = make(chan bool)

		go func() {
			defer close(rop.chPost)

			// Wait for the main operation to finish
			<-rop.chDone
			if rop.err != nil {
				return
			}

			// Get the operation data
			op, err := rop.GetTarget()
			if err != nil {
				return
			}

			// Extract the fingerprint
			fingerprint := op.Metadata["fingerprint"].(string)

			// Add the aliases
			for _, entry := range req.Aliases {
				alias := api.ImageAliasesPost{}
				alias.Name = entry.Name
				alias.Target = fingerprint

				r.CreateImageAlias(alias)
			}
		}()
	}

	// Forward targetOp to remote op
	go func() {
		success := false
		errors := map[string]error{}
		for _, serverURL := range urls {
			req.Source.Server = serverURL

			op, err := r.CreateImage(req, nil)
			if err != nil {
				errors[serverURL] = err
				continue
			}

			rop.handlerLock.Lock()
			rop.targetOp = op
			rop.handlerLock.Unlock()

			for _, handler := range rop.handlers {
				rop.targetOp.AddHandler(handler)
			}

			err = rop.targetOp.Wait()
			if err != nil {
				errors[serverURL] = err
				continue
			}

			success = true
			break
		}

		if !success {
			rop.err = remoteOperationError("Failed remote image download", errors)
		}

		close(rop.chDone)
	}()

	return &rop, nil
}

// CopyImage copies an image from a remote server. Additional options can be passed using ImageCopyArgs
func (r *ProtocolLXD) CopyImage(source ImageServer, image api.Image, args *ImageCopyArgs) (RemoteOperation, error) {
	// Sanity checks
	if r == source {
		return nil, fmt.Errorf("The source and target servers must be different")
	}

	// Get source server connection information
	info, err := source.GetConnectionInfo()
	if err != nil {
		return nil, err
	}

	// Push mode
	if args != nil && args.Mode == "push" {
		// Get certificate and URL
		info, err := r.GetConnectionInfo()
		if err != nil {
			return nil, err
		}

		imagesPost := api.ImagesPost{
			Source: &api.ImagesPostSource{
				Fingerprint: image.Fingerprint,
				Mode:        args.Mode,
			},
		}

		if args.CopyAliases {
			imagesPost.Aliases = image.Aliases
		}

		imagesPost.ExpiresAt = image.ExpiresAt
		imagesPost.Properties = image.Properties
		imagesPost.Public = args.Public

		// Receive token from target server. This token is later passed to the source which will use
		// it, together with the URL and certificate, to connect to the target.
		tokenOp, err := r.CreateImage(imagesPost, nil)
		if err != nil {
			return nil, err
		}

		opAPI := tokenOp.Get()

		secret, ok := opAPI.Metadata["secret"]
		if !ok {
			return nil, fmt.Errorf("No token provided")
		}

		req := api.ImageExportPost{
			Target:      info.URL,
			Certificate: info.Certificate,
			Secret:      secret.(string),
			Aliases:     image.Aliases,
		}

		exportOp, err := source.ExportImage(image.Fingerprint, req)
		if err != nil {
			tokenOp.Cancel()
			return nil, err
		}

		rop := remoteOperation{
			targetOp: exportOp,
			chDone:   make(chan bool),
		}

		// Forward targetOp to remote op
		go func() {
			rop.err = rop.targetOp.Wait()
			tokenOp.Cancel()
			close(rop.chDone)
		}()

		return &rop, nil
	}

	// Relay mode
	if args != nil && args.Mode == "relay" {
		metaFile, err := ioutil.TempFile("", "lxc_image_")
		if err != nil {
			return nil, err
		}
		defer os.Remove(metaFile.Name())

		rootfsFile, err := ioutil.TempFile("", "lxc_image_")
		if err != nil {
			return nil, err
		}
		defer os.Remove(rootfsFile.Name())

		// Import image
		req := ImageFileRequest{
			MetaFile:   metaFile,
			RootfsFile: rootfsFile,
		}

		resp, err := source.GetImageFile(image.Fingerprint, req)
		if err != nil {
			return nil, err
		}

		// Export image
		_, err = metaFile.Seek(0, 0)
		if err != nil {
			return nil, err
		}

		_, err = rootfsFile.Seek(0, 0)
		if err != nil {
			return nil, err
		}

		imagePost := api.ImagesPost{}
		imagePost.Public = args.Public

		if args.CopyAliases {
			imagePost.Aliases = image.Aliases
			if args.Aliases != nil {
				imagePost.Aliases = append(imagePost.Aliases, args.Aliases...)
			}
		}

		createArgs := &ImageCreateArgs{
			MetaFile: metaFile,
			MetaName: image.Filename,
			Type:     image.Type,
		}

		if resp.RootfsName != "" {
			// Deal with split images
			createArgs.RootfsFile = rootfsFile
			createArgs.RootfsName = image.Filename
		}

		rop := remoteOperation{
			chDone: make(chan bool),
		}

		go func() {
			defer close(rop.chDone)

			op, err := r.CreateImage(imagePost, createArgs)
			if err != nil {
				rop.err = remoteOperationError("Failed to copy image", nil)
				return
			}

			rop.handlerLock.Lock()
			rop.targetOp = op
			rop.handlerLock.Unlock()

			for _, handler := range rop.handlers {
				rop.targetOp.AddHandler(handler)
			}

			err = rop.targetOp.Wait()
			if err != nil {
				rop.err = remoteOperationError("Failed to copy image", nil)
				return
			}
		}()

		return &rop, nil
	}

	// Prepare the copy request
	req := api.ImagesPost{
		Source: &api.ImagesPostSource{
			ImageSource: api.ImageSource{
				Certificate: info.Certificate,
				Protocol:    info.Protocol,
			},
			Fingerprint: image.Fingerprint,
			Mode:        "pull",
			Type:        "image",
		},
	}

	if args != nil {
		req.Source.ImageType = args.Type
	}

	// Generate secret token if needed
	if !image.Public {
		secret, err := source.GetImageSecret(image.Fingerprint)
		if err != nil {
			return nil, err
		}

		req.Source.Secret = secret
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

	return r.tryCopyImage(req, info.Addresses)
}

// UpdateImage updates the image definition
func (r *ProtocolLXD) UpdateImage(fingerprint string, image api.ImagePut, ETag string) error {
	// Send the request
	_, _, err := r.query("PUT", fmt.Sprintf("/images/%s", url.PathEscape(fingerprint)), image, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteImage requests that LXD removes an image from the store
func (r *ProtocolLXD) DeleteImage(fingerprint string) (Operation, error) {
	// Send the request
	op, _, err := r.queryOperation("DELETE", fmt.Sprintf("/images/%s", url.PathEscape(fingerprint)), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// RefreshImage requests that LXD issues an image refresh
func (r *ProtocolLXD) RefreshImage(fingerprint string) (Operation, error) {
	if !r.HasExtension("image_force_refresh") {
		return nil, fmt.Errorf("The server is missing the required \"image_force_refresh\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/images/%s/refresh", url.PathEscape(fingerprint)), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// CreateImageSecret requests that LXD issues a temporary image secret
func (r *ProtocolLXD) CreateImageSecret(fingerprint string) (Operation, error) {
	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/images/%s/secret", url.PathEscape(fingerprint)), nil, "")
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
	_, _, err := r.query("PUT", fmt.Sprintf("/images/aliases/%s", url.PathEscape(name)), alias, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameImageAlias renames an existing image alias
func (r *ProtocolLXD) RenameImageAlias(name string, alias api.ImageAliasesEntryPost) error {
	// Send the request
	_, _, err := r.query("POST", fmt.Sprintf("/images/aliases/%s", url.PathEscape(name)), alias, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteImageAlias removes an alias from the LXD image store
func (r *ProtocolLXD) DeleteImageAlias(name string) error {
	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/images/aliases/%s", url.PathEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// ExportImage exports (copies) an image to a remote server
func (r *ProtocolLXD) ExportImage(fingerprint string, image api.ImageExportPost) (Operation, error) {
	if !r.HasExtension("images_push_relay") {
		return nil, fmt.Errorf("The server is missing the required \"images_push_relay\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/images/%s/export", url.PathEscape(fingerprint)), &image, "")
	if err != nil {
		return nil, err
	}

	return op, nil

}
