package lxd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newUnifiedImageResponse(t *testing.T, filename string, content []byte) *http.Response {
	t.Helper()

	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":        []string{"application/octet-stream"},
			"Content-Disposition": []string{`attachment; filename="` + filename + `"`},
		},
		Body: io.NopCloser(bytes.NewReader(content)),
	}
}

func newSplitImageResponse(t *testing.T, metaName string, metaContent []byte, rootfsName string, rootfsContent []byte) *http.Response {
	t.Helper()

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)

	part, err := mw.CreateFormFile("metadata", metaName)
	require.NoError(t, err)
	_, err = part.Write(metaContent)
	require.NoError(t, err)

	part, err = mw.CreateFormFile("rootfs", rootfsName)
	require.NoError(t, err)
	_, err = part.Write(rootfsContent)
	require.NoError(t, err)

	require.NoError(t, mw.Close())

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{mw.FormDataContentType()}},
		Body:       io.NopCloser(body),
	}
}

func fingerprint(t *testing.T, contents ...[]byte) string {
	t.Helper()
	hash := sha256.New()
	for _, content := range contents {
		_, err := hash.Write(content)
		require.NoError(t, err)
	}

	return hex.EncodeToString(hash.Sum(nil))
}

func TestLXDDownloadImageSanitizesNames(t *testing.T) {
	metaContent := []byte("metadata")
	rootfsContent := []byte("rootfs")
	uFp := fingerprint(t, metaContent)
	sFp := fingerprint(t, metaContent, rootfsContent)
	fpToAddr := func(fp string) string { return "http://localhost/1.0/images/" + fp + "/export" }

	tests := []struct {
		name           string
		response       func(t *testing.T) *http.Response
		fp             string
		wantMetaName   string
		wantRootfsName string
	}{
		{
			name: "unified image with an absolute path",
			response: func(t *testing.T) *http.Response {
				return newUnifiedImageResponse(t, "/etc/cron.d/evil.tar.gz", metaContent)
			},
			fp:           uFp,
			wantMetaName: "evil.tar.gz",
		},
		{
			name: "unified image with a trailing slash",
			response: func(t *testing.T) *http.Response {
				return newUnifiedImageResponse(t, "/etc/cron.d/evil.tar.gz/", metaContent)
			},
			fp:           uFp,
			wantMetaName: "evil.tar.gz",
		},

		{
			name: "unified image with a relative path",
			response: func(t *testing.T) *http.Response {
				return newUnifiedImageResponse(t, "../../evil.tar.gz", metaContent)
			},
			fp:           uFp,
			wantMetaName: "evil.tar.gz",
		},
		{
			name: "split image with absolute and relative paths",
			response: func(t *testing.T) *http.Response {
				return newSplitImageResponse(t, "/etc/cron.d/evil.tar.gz", metaContent, "../../evil.squashfs", rootfsContent)
			},
			fp:             sFp,
			wantMetaName:   "evil.tar.gz",
			wantRootfsName: "evil.squashfs",
		},
		{
			name: "split image with trailing slashes",
			response: func(t *testing.T) *http.Response {
				return newSplitImageResponse(t, "/etc/cron.d/evil.tar.gz/", metaContent, "/evil.squashfs/", rootfsContent)
			},
			fp:             sFp,
			wantMetaName:   "evil.tar.gz",
			wantRootfsName: "evil.squashfs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dest, err := os.Create(filepath.Join(t.TempDir(), "target.meta"))
			require.NoError(t, err)
			defer func() { _ = dest.Close() }()

			destRootfs, err := os.Create(filepath.Join(t.TempDir(), "target.rootfs"))
			require.NoError(t, err)
			defer func() { _ = destRootfs.Close() }()

			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return tt.response(t), nil
			})}

			req := ImageFileRequest{MetaFile: io.WriteSeeker(dest), RootfsFile: io.WriteSeeker(destRootfs)}
			resp, err := lxdDownloadImage(tt.fp, fpToAddr(tt.fp), "", client, req)
			require.NoError(t, err)

			assert.Equal(t, tt.wantMetaName, resp.MetaName)
			assert.Equal(t, tt.wantRootfsName, resp.RootfsName)
		})
	}
}
