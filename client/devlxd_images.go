package lxd

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
)

// ExportImage exports the image that is cached in host's LXD to the guest LXD.
func (r *ProtocolDevLXD) ExportImage(fingerprint string, receiveFileFunc func(filename string, content io.Reader) error) error {
	var req *http.Request

	url := api.NewURL().Scheme(r.httpBaseURL.Scheme).Host(r.httpBaseURL.Host).Path(version.APIVersion, "images", fingerprint, "export").URL

	// No data to be sent along with the request
	req, err := http.NewRequest(http.MethodGet, url.String(), nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", r.httpUserAgent)

	// Send the request.
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	// Handle error response.
	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("Failed to read response body from %q: %v", resp.Request.URL.String(), err)
		}

		// XXX: Devlxd image export does not consistently return the devLXD response.
		// Therefore, try to parse the api.Response first. If that fails, then
		// parse the devLXD response.
		apiResp := api.Response{}
		err = json.Unmarshal(body, &apiResp)
		if err != nil {
			// Report response body in error.
			return api.NewStatusError(resp.StatusCode, strings.TrimSpace(string(body)))
		}

		// Return apiResponse error.
		return api.StatusErrorf(apiResp.Code, apiResp.Error)
	}

	// Handle OK response.
	mediaType, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return err
	}

	switch mediaType {
	case "multipart/form-data":
		// Handle multipart response (split images).
		mr := multipart.NewReader(resp.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}

			if receiveFileFunc != nil {
				err = receiveFileFunc(part.FileName(), part)
				if err != nil {
					return err
				}
			}

			_ = part.Close()
		}

	case "application/octet-stream":
		// Handle octet-stream response (unified image).
		_, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
		if err != nil {
			return err
		}

		filename, ok := params["filename"]
		if !ok {
			return fmt.Errorf("Filename not found in Content-Disposition header")
		}

		if receiveFileFunc != nil {
			return receiveFileFunc(filename, resp.Body)
		}

	default:
		return fmt.Errorf("Response contains unsupported media type: %s", mediaType)
	}

	return nil
}
