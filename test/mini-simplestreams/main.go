package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/canonical/lxd/shared/simplestreams"
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

	size, hash, err := testutils.GetSizeAndSHA256HashFromFile(busyBoxFilepath)
	if err != nil {
		return fmt.Errorf("Failed hashing image file: %w", err)
	}

	now := time.Now()
	updated := now.Format(time.RFC3339[:len(time.RFC3339)-5])
	version := now.Format("20060102_1504")
	result, err := servemock.API(context.Background(), servemock.Config{
		UseTLS:  true,
		Address: address,
		Handlers: []servemock.Handler{
			{
				Pattern: "GET /streams/v1/index.json",
				HTTPHandler: func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(200)
					_ = json.NewEncoder(w).Encode(simplestreams.Stream{
						Format:  "index:1.0",
						Updated: updated,
						Index: map[string]simplestreams.StreamIndex{
							"images": {
								DataType: "image-downloads",
								Path:     "streams/v1/images.json",
								Format:   "products:1.0",
								Updated:  updated,
								Products: []string{
									"busybox",
									"exploit",
								},
							},
						},
					})
				},
			},
			{
				Pattern: "GET /streams/v1/images.json",
				HTTPHandler: func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(200)
					_ = json.NewEncoder(w).Encode(simplestreams.Products{
						ContentID: "images",
						DataType:  "image-downloads",
						Format:    "products:1.0",
						Products: map[string]simplestreams.Product{
							"busybox": {
								Aliases:      "busybox",
								Architecture: "amd64",
								Versions: map[string]simplestreams.ProductVersion{
									version: {
										Items: map[string]simplestreams.ProductVersionItem{
											"lxd_combined.tar.gz": {
												FileType:   "lxd_combined.tar.gz",
												Path:       "images/busybox/lxd_combined.tar.gz",
												HashSha256: hash,
												Size:       size,
											},
										},
									},
								},
							},
							"exploit": {
								Aliases:      "exploit",
								Architecture: "amd64",
								Versions: map[string]simplestreams.ProductVersion{
									version: {
										Items: map[string]simplestreams.ProductVersionItem{
											"lxd_combined.tar.gz": {
												FileType:   "lxd_combined.tar.gz",
												Path:       "images/exploit/lxd_combined.tar.gz",
												HashSha256: "../../../../tmp/pwned.txt",
												Size:       int64(len(pwnedBytes)),
											},
										},
									},
								},
							},
						},
						Updated: updated,
					})
				},
			},
			{
				Pattern: "GET /images/busybox/lxd_combined.tar.gz",
				HTTPHandler: func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
					f, err := os.Open(busyBoxFilepath)
					if err != nil {
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = w.Write([]byte(err.Error()))
						return
					}

					w.WriteHeader(http.StatusOK)
					_, _ = io.Copy(w, f)
				},
			},
			{
				Pattern: "GET /images/exploit/lxd_combined.tar.gz",
				HTTPHandler: func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Length", strconv.Itoa(len(pwnedBytes)))
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write(pwnedBytes)
				},
			},
		},
		CACertPath: filepath.Join(workdir, "mini-simplestreams-ca.crt"),
	})
	if err != nil {
		return err
	}

	return <-result.Err
}
