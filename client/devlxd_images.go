package lxd

import (
	"errors"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// GetImageFile exports the image with a given fingerprint from the host's LXD.
// Note that only cached and public images can be exported.
func (r *ProtocolDevLXD) GetImageFile(fingerprint string, req ImageFileRequest) (*ImageFileResponse, error) {
	if req.MetaFile == nil {
		return nil, errors.New("The MetaFile field is required")
	}

	url := api.NewURL().Scheme(r.httpBaseURL.Scheme).Host(r.httpBaseURL.Host).Path(version.APIVersion, "images", fingerprint, "export").URL
	return lxdDownloadImage(fingerprint, url.String(), r.httpUserAgent, r.DoHTTP, req)
}
