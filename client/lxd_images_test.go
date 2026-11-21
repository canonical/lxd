package lxd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/api"
)

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
	tmpDir := t.TempDir()

	targetMeta := filepath.Join(tmpDir, "target.meta")
	targetRootfs := filepath.Join(tmpDir, "target.rootfs")

	dest, err := os.Create(targetMeta)
	require.NoError(t, err)

	defer func() { _ = dest.Close() }()

	destRootfs, err := os.Create(targetRootfs)
	require.NoError(t, err)

	defer func() { _ = destRootfs.Close() }()
	req := ImageFileRequest{MetaFile: io.WriteSeeker(dest), RootfsFile: io.WriteSeeker(destRootfs)}

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
			do := func(*http.Request) (*http.Response, error) {
				return tt.response(t), nil
			}

			resp, err := lxdDownloadImage(tt.fp, fpToAddr(tt.fp), "", do, req)
			require.NoError(t, err)

			assert.Equal(t, tt.wantMetaName, resp.MetaName)
			assert.Equal(t, tt.wantRootfsName, resp.RootfsName)
		})
	}
}

func TestGetImageAliasTypeRouting(t *testing.T) {
	const containerPath = "/1.0/images/container/aliases/shared"
	const vmPath = "/1.0/images/virtual-machine/aliases/shared"
	const legacyPath = "/1.0/images/aliases/shared"
	const containerAlias = `{"name":"shared","type":"container","target":"container-image"}`
	const vmAlias = `{"name":"shared","type":"virtual-machine","target":"vm-image"}`
	const legacyAlias = `{"name":"shared","target":"legacy-image"}`

	for _, tt := range []struct {
		name       string
		skipServer bool
		extensions string
		imageType  string
		aliasPath  string
		alias      string
		deniedPath string
		requests   []string
		wantType   string
		wantTarget string
		wantStatus int
		wantError  string
	}{
		{
			name: "known legacy server", extensions: `[]`, imageType: "container",
			aliasPath: legacyPath, alias: legacyAlias, requests: []string{"/1.0", legacyPath},
			wantType: "container", wantTarget: "legacy-image",
		},
		{
			name: "known typed server does not fall back for an untyped lookup", extensions: `["image_alias_types"]`,
			aliasPath: legacyPath, alias: legacyAlias, requests: []string{"/1.0", containerPath, vmPath},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "known typed server diagnoses a type mismatch", extensions: `["image_alias_types"]`, imageType: "virtual-machine",
			aliasPath: legacyPath, alias: containerAlias, requests: []string{"/1.0", vmPath, legacyPath},
			wantError: `It exists for image type "container"`,
		},
		{
			name: "unknown capabilities select typed container first", skipServer: true,
			aliasPath: containerPath, alias: containerAlias, requests: []string{containerPath},
			wantType: "container", wantTarget: "container-image",
		},
		{
			name: "unknown capabilities select typed VM after container miss", skipServer: true,
			aliasPath: vmPath, alias: vmAlias, requests: []string{containerPath, vmPath},
			wantType: "virtual-machine", wantTarget: "vm-image",
		},
		{
			name: "unknown capabilities fall back after both typed lookups miss", skipServer: true,
			aliasPath: legacyPath, alias: legacyAlias, requests: []string{containerPath, vmPath, legacyPath},
			wantType: "container", wantTarget: "legacy-image",
		},
		{
			name: "unknown capabilities treat microVM as container in legacy fallback", skipServer: true, imageType: "microvm",
			aliasPath: legacyPath, alias: legacyAlias, requests: []string{containerPath, legacyPath},
			wantType: "container", wantTarget: "legacy-image",
		},
		{
			name: "unknown capabilities check the legacy alias type", skipServer: true, imageType: "virtual-machine",
			aliasPath: legacyPath, alias: legacyAlias, requests: []string{vmPath, legacyPath},
			wantError: "Alias does not exist for the specified type",
		},
		{
			name: "unknown capabilities do not fall back on forbidden", skipServer: true, imageType: "virtual-machine",
			aliasPath: legacyPath, alias: vmAlias, deniedPath: vmPath, requests: []string{vmPath},
			wantStatus: http.StatusForbidden,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := make(chan string, 8)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				select {
				case requests <- r.URL.Path:
				default:
					t.Error("Too many requests")
				}

				switch r.URL.Path {
				case "/1.0":
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"api_extensions":`+tt.extensions+`}}`)
				case tt.aliasPath:
					w.Header().Set("ETag", "alias-tag")
					_, _ = io.WriteString(w, `{"type":"sync","metadata":`+tt.alias+`}`)
				case tt.deniedPath:
					w.WriteHeader(http.StatusForbidden)
					_, _ = io.WriteString(w, `{"type":"error","error":"Forbidden"}`)
				default:
					w.WriteHeader(http.StatusNotFound)
					_, _ = io.WriteString(w, `{"type":"error","error":"Not found"}`)
				}
			}))
			defer server.Close()

			client, err := ConnectLXD(server.URL, &ConnectionArgs{SkipGetServer: tt.skipServer})
			require.NoError(t, err)
			defer client.Disconnect()

			alias, etag, err := client.GetImageAliasType(tt.imageType, "shared")
			switch {
			case tt.wantStatus != 0:
				require.True(t, api.StatusErrorCheck(err, tt.wantStatus), "Unexpected error: %v", err)
			case tt.wantError != "":
				require.ErrorContains(t, err, tt.wantError)
			default:
				require.NoError(t, err)
				require.Equal(t, tt.wantType, alias.Type)
				require.Equal(t, tt.wantTarget, alias.Target)
				require.Equal(t, "alias-tag", etag)
			}

			require.Len(t, requests, len(tt.requests))
			for _, path := range tt.requests {
				require.Equal(t, path, <-requests)
			}
		})
	}
}

func TestImageAliasMutationRouting(t *testing.T) {
	for _, mode := range []string{"legacy", "typed", "unknown"} {
		for _, tt := range []struct {
			name   string
			method string
			path   string
			call   func(InstanceServer) error
		}{
			{"create", http.MethodPost, "/1.0/images/container/aliases", func(c InstanceServer) error {
				return c.CreateImageAlias(api.ImageAliasesPost{ImageAliasesEntry: api.ImageAliasesEntry{Name: "shared", Target: "image", Type: "container"}})
			}},
			{"update", http.MethodPut, "/1.0/images/container/aliases/shared", func(c InstanceServer) error {
				return c.UpdateImageAliasType("container", "shared", api.ImageAliasesEntryPut{Target: "image"}, "tag")
			}},
			{"rename", http.MethodPost, "/1.0/images/container/aliases/shared", func(c InstanceServer) error {
				return c.RenameImageAliasType("container", "shared", api.ImageAliasesEntryPost{Name: "new"})
			}},
			{"delete", http.MethodDelete, "/1.0/images/container/aliases/shared", func(c InstanceServer) error {
				return c.DeleteImageAliasType("container", "shared")
			}},
		} {
			t.Run(tt.name+"/"+mode, func(t *testing.T) {
				route := make(chan string, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/1.0" {
						assert.NotEqual(t, "unknown", mode, "SkipGetServer must not trigger capability discovery")
						body := `{"type":"sync","metadata":{"api_extensions":[]}}`
						if mode == "typed" {
							body = `{"type":"sync","metadata":{"api_extensions":["image_alias_types"]}}`
						}

						_, _ = io.WriteString(w, body)
						return
					}

					assert.Equal(t, tt.method, r.Method)
					assert.Equal(t, tt.path, r.URL.Path)
					select {
					case route <- r.URL.Path:
					default:
						t.Error("Unexpected duplicate mutation")
					}

					if mode == "unknown" {
						w.WriteHeader(http.StatusNotFound)
						_, _ = io.WriteString(w, `{"type":"error","error":"Not found"}`)
						return
					}

					_, _ = io.WriteString(w, `{"type":"sync","metadata":{}}`)
				}))
				defer server.Close()

				client, err := ConnectLXD(server.URL, &ConnectionArgs{SkipGetServer: mode == "unknown"})
				require.NoError(t, err)
				defer client.Disconnect()

				err = tt.call(client)
				switch mode {
				case "typed":
					require.NoError(t, err)
					require.Len(t, route, 1)
				case "unknown":
					require.True(t, api.StatusErrorCheck(err, http.StatusNotFound), "Unexpected error: %v", err)
					require.Len(t, route, 1, "only the typed mutation should be sent")
				default:
					require.ErrorContains(t, err, "image_alias_types")
					require.Empty(t, route, "no mutation should be sent")
				}
			})
		}
	}
}

func TestImageAliasInvalidTypes(t *testing.T) {
	for _, mode := range []string{"legacy", "typed", "unknown"} {
		for _, imageType := range []string{
			"unsupported",
			"CONTAINER",
			"container/../../instances",
			"container?project=other",
			"container%2Faliases",
			"container#fragment",
		} {
			for _, operation := range []struct {
				name string
				call func(InstanceServer, string) error
			}{
				{"lookup", func(c InstanceServer, imageType string) error {
					_, _, err := c.GetImageAliasType(imageType, "shared")
					return err
				}},
				{"create", func(c InstanceServer, imageType string) error {
					return c.CreateImageAlias(api.ImageAliasesPost{ImageAliasesEntry: api.ImageAliasesEntry{Name: "shared", Target: "image", Type: imageType}})
				}},
				{"update", func(c InstanceServer, imageType string) error {
					return c.UpdateImageAliasType(imageType, "shared", api.ImageAliasesEntryPut{Target: "image"}, "")
				}},
				{"delete", func(c InstanceServer, imageType string) error {
					return c.DeleteImageAliasType(imageType, "shared")
				}},
				{"rename", func(c InstanceServer, imageType string) error {
					return c.RenameImageAliasType(imageType, "shared", api.ImageAliasesEntryPost{Name: "new"})
				}},
			} {
				t.Run(mode+"/"+imageType+"/"+operation.name, func(t *testing.T) {
					var requests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method == http.MethodGet && r.URL.Path == "/1.0" {
							body := `{"type":"sync","metadata":{"api_extensions":[]}}`
							if mode == "typed" {
								body = `{"type":"sync","metadata":{"api_extensions":["image_alias_types"]}}`
							}

							_, _ = io.WriteString(w, body)
							return
						}

						requests.Add(1)
						_, _ = io.WriteString(w, `{"type":"sync","metadata":{}}`)
					}))
					defer server.Close()

					client, err := ConnectLXD(server.URL, &ConnectionArgs{SkipGetServer: mode == "unknown"})
					require.NoError(t, err)
					defer client.Disconnect()

					err = operation.call(client, imageType)
					require.ErrorContains(t, err, "Invalid image type")
					require.Zero(t, requests.Load(), "invalid types must not send resource requests")
				})
			}
		}
	}
}

func TestImageAliasMutationMicroVMType(t *testing.T) {
	route := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/1.0" {
			_, _ = io.WriteString(w, `{"type":"sync","metadata":{"api_extensions":["image_alias_types"]}}`)
			return
		}

		route <- r.Method + " " + r.URL.Path
		_, _ = io.WriteString(w, `{"type":"sync","metadata":{}}`)
	}))
	defer server.Close()
	client, err := ConnectLXD(server.URL, nil)
	require.NoError(t, err)
	defer client.Disconnect()

	require.NoError(t, client.DeleteImageAliasType("microvm", "shared"))
	require.Equal(t, "DELETE /1.0/images/container/aliases/shared", <-route)
}

func TestCreateImageAliasUntypedLegacy(t *testing.T) {
	for _, skipServer := range []bool{false, true} {
		t.Run(strconv.FormatBool(skipServer), func(t *testing.T) {
			var writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/1.0" {
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"api_extensions":[]}}`)
					return
				}

				if r.Method != http.MethodPost || r.URL.Path != "/1.0/images/aliases" {
					t.Errorf("Unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "Unexpected request", http.StatusNotFound)
					return
				}

				writes.Add(1)
				_, _ = io.WriteString(w, `{"type":"sync","metadata":{}}`)
			}))
			defer server.Close()
			client, err := ConnectLXD(server.URL, &ConnectionArgs{SkipGetServer: skipServer})
			require.NoError(t, err)
			defer client.Disconnect()

			require.NoError(t, client.CreateImageAlias(api.ImageAliasesPost{
				ImageAliasesEntry: api.ImageAliasesEntry{Name: "legacy", Target: "image"},
			}))
			require.Equal(t, int32(1), writes.Load())
		})
	}
}

func TestRemoteAliasHandoffToLegacyDestination(t *testing.T) {
	for _, tt := range []struct {
		name            string
		sharedAlias     bool
		typedTarget     bool
		skipServer      bool
		wantAlias       string
		wantFingerprint string
		imageType       string
		args            *ImageCopyArgs
	}{
		{"shared alias on legacy target", true, false, false, "", "selected-image", "container", nil},
		{"unique alias on legacy target", false, false, false, "release", "", "container", nil},
		{"shared alias on typed target", true, true, false, "release", "", "container", nil},
		{"shared alias on unknown target", true, false, true, "", "selected-image", "container", nil},
		{"selected VM on typed target", true, true, false, "release", "", "virtual-machine", nil},
		{"selected VM with empty options", true, true, false, "release", "", "virtual-machine", &ImageCopyArgs{}},
		{"explicit type overrides selected VM", true, true, false, "release", "", "virtual-machine", &ImageCopyArgs{Type: "container"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			copies := make(chan api.ImagesPostSource, 1)
			sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/1.0" {
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"api_extensions":["image_alias_types"],"environment":{"server_name":"source","addresses":["images.example:8443"]}}}`)
					return
				}

				if r.URL.Path == "/1.0/images/release" {
					w.WriteHeader(http.StatusNotFound)
					_, _ = io.WriteString(w, `{"type":"error","error":"Not found","error_code":404}`)
					return
				}

				if r.URL.Path == "/1.0/images/container/aliases/release" {
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"name":"release","type":"container","target":"selected-image"}}`)
					return
				}

				assert.Equal(t, "/1.0/images/aliases/release", r.URL.Path)
				if tt.sharedAlias {
					w.WriteHeader(http.StatusConflict)
					_, _ = io.WriteString(w, `{"type":"error","error":"Alias exists for both types","error_code":409}`)
				} else {
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"name":"release","type":"container","target":"selected-image"}}`)
				}
			}))
			defer sourceServer.Close()
			source, err := ConnectLXD(sourceServer.URL, nil)
			require.NoError(t, err)
			defer source.Disconnect()

			targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/1.0/events" {
					w.WriteHeader(http.StatusNotFound)
					_, _ = io.WriteString(w, `{"type":"error","error":"Events unavailable","error_code":404}`)
					return
				}

				if r.Method == http.MethodPost && r.URL.Path == "/1.0/images" {
					var request api.ImagesPost
					err := json.NewDecoder(r.Body).Decode(&request)
					if assert.NoError(t, err) && assert.NotNil(t, request.Source) {
						select {
						case copies <- *request.Source:
						default:
							// Copy retries the selected address; earlier attempts carry the same value.
						}
					}

					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"type":"error","error":"Copy request inspected","error_code":400}`)
					return
				}

				assert.Equal(t, "/1.0", r.URL.Path)
				extensions := `[]`
				if tt.typedTarget {
					extensions = `["image_alias_types"]`
				}

				_, _ = io.WriteString(w, `{"type":"sync","metadata":{"api_extensions":`+extensions+`,"environment":{"server_name":"target"}}}`)
			}))
			defer targetServer.Close()
			target, err := ConnectLXD(targetServer.URL, &ConnectionArgs{SkipGetServer: tt.skipServer})
			require.NoError(t, err)
			defer target.Disconnect()
			targetLXD, ok := target.(*ProtocolLXD)
			require.True(t, ok)

			requestSource := api.InstanceSource{Alias: "release"}
			_, err = targetLXD.getSourceImageConnectionInfo(source,
				api.Image{Fingerprint: "selected-image", Public: true, Type: "container"}, &requestSource)
			require.NoError(t, err)
			require.Equal(t, tt.wantAlias, requestSource.Alias)
			require.Equal(t, tt.wantFingerprint, requestSource.Fingerprint)

			op, err := target.CopyImage(source, api.Image{Fingerprint: "release", Public: true, Type: tt.imageType}, tt.args)
			require.NoError(t, err)
			require.ErrorContains(t, op.Wait(), "Copy request inspected")
			require.Len(t, copies, 1)
			wantCopy := "release"
			if tt.wantFingerprint != "" {
				wantCopy = tt.wantFingerprint
			}

			copied := <-copies
			require.Equal(t, wantCopy, copied.Fingerprint)
			wantType := tt.imageType
			if tt.args != nil && tt.args.Type != "" {
				wantType = tt.args.Type
			}

			require.Equal(t, wantType, copied.ImageType)
		})
	}
}

func TestCopyImagePreservesFingerprintWithSharedAlias(t *testing.T) {
	const fingerprint = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, tt := range []struct {
		name        string
		imageStatus int
		wantError   string
	}{
		{name: "existing fingerprint", imageStatus: http.StatusOK, wantError: "Export requested image"},
		{name: "lookup denied", imageStatus: http.StatusForbidden, wantError: "Image lookup denied"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/1.0":
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"api_extensions":["image_alias_types"],"environment":{"server_name":"source"}}}`)
				case "/1.0/images/aliases/" + fingerprint:
					w.WriteHeader(http.StatusConflict)
					_, _ = io.WriteString(w, `{"type":"error","error":"Alias exists for both types","error_code":409}`)
				case "/1.0/images/" + fingerprint:
					if tt.imageStatus == http.StatusForbidden {
						w.WriteHeader(http.StatusForbidden)
						_, _ = io.WriteString(w, `{"type":"error","error":"Image lookup denied","error_code":403}`)
						return
					}

					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"fingerprint":"`+fingerprint+`","type":"container","public":true}}`)
				case "/1.0/images/container/aliases/" + fingerprint:
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"type":"container","target":"unrelated-image"}}`)
				case "/1.0/images/" + fingerprint + "/export":
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"type":"error","error":"Export requested image","error_code":400}`)
				default:
					t.Errorf("Unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "Unexpected image selected", http.StatusBadRequest)
				}
			}))
			defer sourceServer.Close()
			source, err := ConnectLXD(sourceServer.URL, nil)
			require.NoError(t, err)
			defer source.Disconnect()

			targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/1.0", r.URL.Path)
				_, _ = io.WriteString(w, `{"type":"sync","metadata":{"api_extensions":[],"environment":{"server_name":"target"}}}`)
			}))
			defer targetServer.Close()
			target, err := ConnectLXD(targetServer.URL, nil)
			require.NoError(t, err)
			defer target.Disconnect()

			_, err = target.CopyImage(source, api.Image{Fingerprint: fingerprint, Public: true, Type: "container"}, &ImageCopyArgs{Mode: "relay"})
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}

func TestAuthGroupImageAliasRepresentation(t *testing.T) {
	for _, typed := range []bool{false, true} {
		t.Run(strconv.FormatBool(typed), func(t *testing.T) {
			reference := "/1.0/images/aliases/release?project=default"
			if typed {
				reference = "/1.0/images/container/aliases/release?project=default"
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/1.0":
					extensions := `["access_management"]`
					if typed {
						extensions = `["access_management","image_alias_types"]`
					}

					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"api_extensions":`+extensions+`}}`)
				case "/1.0/auth/groups/review", "/1.0/auth/groups":
					wantPreference := ""
					if typed {
						wantPreference = "1"
					}

					assert.Equal(t, wantPreference, r.URL.Query().Get("image_alias_types"))
					group := api.AuthGroup{Name: "review"}
					group.Permissions = []api.Permission{{
						EntityType: "image_alias", EntityReference: reference, Entitlement: "can_view",
					}}
					var metadata any = group
					if r.URL.Path == "/1.0/auth/groups" {
						assert.Equal(t, "1", r.URL.Query().Get("recursion"))
						metadata = []api.AuthGroup{group}
					}

					w.Header().Set("ETag", `"group-tag"`)
					assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"type": "sync", "metadata": metadata}))
				default:
					t.Errorf("Unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "Unexpected request", http.StatusNotFound)
				}
			}))
			defer server.Close()
			client, err := ConnectLXD(server.URL, nil)
			require.NoError(t, err)
			defer client.Disconnect()

			group, etag, err := client.GetAuthGroup("review")
			require.NoError(t, err)
			require.Equal(t, reference, group.Permissions[0].EntityReference)
			require.Equal(t, `"group-tag"`, etag)
			groups, err := client.GetAuthGroups()
			require.NoError(t, err)
			require.Equal(t, reference, groups[0].Permissions[0].EntityReference)
		})
	}
}
