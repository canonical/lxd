package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/version"
	"github.com/canonical/lxd/test/testutils"
	"github.com/canonical/lxd/test/testutils/servemock"
)

func main() {
	if len(os.Args) < 4 {
		_, _ = fmt.Fprintln(os.Stderr, "Usage: mini-simplestreams <workdir> <address> <busybox image filepath>")
		os.Exit(1)
	}

	err := run()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		os.Exit(1)
	}
}

var pwnedBytes = []byte("You've been pwned!")

func run() error {
	workdir := os.Args[1]
	address := os.Args[2]
	busyBoxFilepath := filepath.Join(workdir, os.Args[3])

	busyBoxImage, exploitImage, err := getAPIImages(busyBoxFilepath)
	if err != nil {
		return err
	}

	result, err := servemock.API(context.Background(), servemock.Config{
		UseTLS:  true,
		Address: address,
		NotFound: func(w http.ResponseWriter, r *http.Request) {
			_ = response.NotFound(nil).Render(w, r)
		},
		Handlers: []servemock.Handler{
			{
				Pattern: "GET /1.0",
				HTTPHandler: func(w http.ResponseWriter, r *http.Request) {
					srv := api.ServerUntrusted{
						APIExtensions: version.APIExtensions,
						APIStatus:     "stable",
						APIVersion:    version.APIVersion,
						Public:        false,
						Auth:          api.AuthUntrusted,
						AuthMethods:   []string{api.AuthenticationMethodTLS},
					}

					_ = response.SyncResponse(true, srv).Render(w, r)
				},
			},
			{
				Pattern:     "GET /1.0/images/aliases/{name}",
				HTTPHandler: getImageAlias(*busyBoxImage, *exploitImage),
			},
			{
				Pattern:     "GET /1.0/images",
				HTTPHandler: getImages(*busyBoxImage, *exploitImage),
			},
			{
				Pattern:     "GET /1.0/images/{fingerprint}",
				HTTPHandler: getImage(*busyBoxImage, *exploitImage),
			},
			{
				Pattern:     "GET /images/{fingerprint}/export",
				HTTPHandler: exportImage(*busyBoxImage, busyBoxFilepath, *exploitImage),
			},
		},
		CACertPath: filepath.Join(workdir, "mini-lxd-ca.crt"),
	})
	if err != nil {
		return err
	}

	return <-result.Err
}

func getAPIImages(busyBoxFilepath string) (busybox *api.Image, exploit *api.Image, err error) {
	busyBoxSize, busyBoxHash, err := testutils.GetSizeAndSHA256HashFromFile(busyBoxFilepath)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed hashing image file: %w", err)
	}

	exploitSize, exploitHash, err := testutils.GetSizeAndSHA256Hash(bytes.NewBuffer(pwnedBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("Failed hashing image file: %w", err)
	}

	now := time.Now()
	busyBoxImage := api.Image{
		Aliases: []api.ImageAlias{
			{
				Name: "busybox",
			},
		},
		Architecture: "x86_64",
		Public:       true,
		Filename:     "lxd_combined.tar.gz",
		Fingerprint:  busyBoxHash,
		Size:         busyBoxSize,
		Type:         "container",
		CreatedAt:    now,
		ExpiresAt:    now.Add(time.Hour),
		LastUsedAt:   now,
		UploadedAt:   now,
		Properties: map[string]string{
			"architecture": "amd64",
			"serial":       "20260716_1502",
			"type":         "tar.gz",
		},
		Profiles: []string{"default"},
		Project:  "default",
	}

	exploitImage := api.Image{
		Aliases: []api.ImageAlias{
			{
				Name: "exploit",
			},
		},
		Architecture: "x86_64",
		Public:       true,
		Filename:     "lxd_combined.tar.gz",
		Fingerprint:  exploitHash,
		Size:         exploitSize,
		Type:         "container",
		CreatedAt:    now,
		ExpiresAt:    now.Add(time.Hour),
		LastUsedAt:   now,
		UploadedAt:   now,
		Properties: map[string]string{
			"architecture": "amd64",
			"serial":       "20260716_1502",
			"type":         "tar.gz",
		},
		Profiles: []string{"default"},
		Project:  "default",
	}

	return &busyBoxImage, &exploitImage, nil
}

func getImageAlias(busyBoxImage api.Image, exploitImage api.Image) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		switch name {
		case "busybox":
			_ = response.SyncResponse(true, api.ImageAliasesEntry{
				Name:   "busybox",
				Type:   "container",
				Target: busyBoxImage.Fingerprint,
			}).Render(w, r)
			return
		case "exploit":
			_ = response.SyncResponse(true, api.ImageAliasesEntry{
				Name:   "exploit",
				Type:   "container",
				Target: exploitImage.Fingerprint,
			}).Render(w, r)
			return
		}

		_ = response.NotFound(nil).Render(w, r)
	}
}

func getImages(busyBoxImage api.Image, exploitImage api.Image) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var recursion int
		if r.URL.Query().Get("recursion") != "" {
			var err error
			recursion, err = strconv.Atoi(r.URL.Query().Get("recursion"))
			if err != nil {
				_ = response.SmartError(err).Render(w, r)
				return
			}
		}

		if recursion == 0 {
			_ = response.SyncResponse(true, []string{
				busyBoxImage.Fingerprint,
				exploitImage.Fingerprint,
			}).Render(w, r)
			return
		}

		_ = response.SyncResponse(true, []api.Image{busyBoxImage, exploitImage}).Render(w, r)
	}
}

func getImage(busyBoxImage api.Image, exploitImage api.Image) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		fingerprint := r.PathValue("fingerprint")
		switch fingerprint {
		case busyBoxImage.Fingerprint:
			_ = response.SyncResponse(true, busyBoxImage).Render(w, r)
			return
		case exploitImage.Fingerprint:
			exploitImageCopy := exploitImage
			exploitImageCopy.Fingerprint = "../../../../tmp/pwned.txt"
			_ = response.SyncResponse(true, exploitImageCopy).Render(w, r)
			return
		default:
			_ = response.NotFound(nil).Render(w, r)
		}
	}
}

func exportImage(busyBoxImage api.Image, busyBoxFilepath string, exploitImage api.Image) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		fingerprint := r.PathValue("fingerprint")
		switch fingerprint {
		case busyBoxImage.Fingerprint:
			_, ext, _, err := shared.DetectCompression(busyBoxFilepath)
			if err != nil {
				ext = ""
			}

			filename := fingerprint + ext

			files := make([]response.FileResponseEntry, 1)
			files[0].Identifier = filename
			files[0].Path = busyBoxFilepath
			files[0].Filename = filename

			_ = response.FileResponse(files, nil).Render(w, r)
		case exploitImage.Fingerprint:
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", strconv.Itoa(len(pwnedBytes)))
			w.Header().Set("Content-Disposition", "inline;filename=pwned.txt")
			w.WriteHeader(http.StatusOK)
			_, _ = io.Copy(w, bytes.NewBuffer(pwnedBytes))
		}
	}
}
