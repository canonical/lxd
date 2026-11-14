package main

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/lxc/config"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

type utilsTestSuite struct {
	suite.Suite
}

func TestUtilsTestSuite(t *testing.T) {
	suite.Run(t, new(utilsTestSuite))
}

func aliasTestConfig(t *testing.T, handler http.HandlerFunc) *config.Config {
	t.Helper()

	t.Setenv("LXD_AUTH_BEARER_TOKEN", "")
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)

	conf := &config.Config{
		ConfigDir:     t.TempDir(),
		DefaultRemote: "test",
		Remotes: map[string]config.Remote{
			"test": {Addr: server.URL},
		},
	}

	cert := shared.TestingKeyPair()
	require.NoError(t, os.WriteFile(conf.ConfigPath("client.crt"), cert.PublicKey(), 0o600))
	require.NoError(t, os.WriteFile(conf.ConfigPath("client.key"), cert.PrivateKey(), 0o600))
	require.NoError(t, os.MkdirAll(conf.ConfigPath("servercerts"), 0o700))
	serverCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	require.NoError(t, os.WriteFile(conf.ServerCertPath("test"), serverCert, 0o600))
	return conf
}

func TestGuessImageSharedLocalAlias(t *testing.T) {
	for _, imageRef := range []string{"images", "images/edge"} {
		t.Run(imageRef, func(t *testing.T) {
			ref := url.PathEscape(imageRef)
			conf := aliasTestConfig(t, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				switch r.URL.EscapedPath() {
				case "/1.0":
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"api_extensions":["image_alias_types"]}}`)
				case "/1.0/images/container/aliases/" + ref:
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"type":"container","target":"container-image"}}`)
				case "/1.0/images/virtual-machine/aliases/" + ref:
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"type":"virtual-machine","target":"vm-image"}}`)
				case "/1.0/images/aliases/" + ref:
					w.WriteHeader(http.StatusConflict)
					_, _ = io.WriteString(w, `{"type":"error","error":"Alias exists for both image types"}`)
				default:
					w.WriteHeader(http.StatusNotFound)
					_, _ = io.WriteString(w, `{"type":"error","error":"Not found"}`)
				}
			})
			conf.Remotes["images"] = config.Remote{Public: true}

			server, err := conf.GetInstanceServer("test")
			require.NoError(t, err)
			defer server.Disconnect()

			remote, image := guessImage(conf, server, "test", "", imageRef)
			require.Empty(t, remote)
			require.Equal(t, imageRef, image)
		})
	}
}

func TestPublishSnapshotAliasLookup(t *testing.T) {
	for _, tt := range []struct {
		name       string
		extensions string
		aliases    []string
		requests   []string
	}{
		{
			name: "no aliases", extensions: `["instances","image_alias_types"]`,
			requests: []string{"GET /1.0", "POST /1.0/images"},
		},
		{
			name: "legacy aliases", extensions: `["instances"]`, aliases: []string{"shared"},
			requests: []string{"GET /1.0", "GET /1.0/images/aliases", "POST /1.0/images"},
		},
		{
			name: "typed aliases", extensions: `["instances","image_alias_types"]`, aliases: []string{"shared"},
			requests: []string{"GET /1.0", "POST /1.0/images"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := make(chan string, 8)
			conf := aliasTestConfig(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/1.0/events" {
					w.WriteHeader(http.StatusNotFound)
					_, _ = io.WriteString(w, `{"type":"error","error":"Events unavailable","error_code":404}`)
					return
				}

				request := r.Method + " " + r.URL.Path
				select {
				case requests <- request:
				default:
					t.Error("Too many requests")
				}

				switch request {
				case "GET /1.0":
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"api_extensions":`+tt.extensions+`}}`)
				case "GET /1.0/instances/source":
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"type":"container"}}`)
				case "GET /1.0/images/aliases":
					aliases := `[]`
					if tt.name == "typed aliases" {
						aliases = `[{"name":"shared","type":"virtual-machine","target":"vm-image"}]`
					}

					_, _ = io.WriteString(w, `{"type":"sync","metadata":`+aliases+`}`)
				case "POST /1.0/images":
					var image api.ImagesPost
					err := json.NewDecoder(r.Body).Decode(&image)
					if assert.NoError(t, err) && assert.NotNil(t, image.Source) {
						assert.Equal(t, api.SourceType("snapshot"), image.Source.Type)
						assert.Equal(t, "source/snap", image.Source.Name)
					}

					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"type":"error","error":"Test reached publish request"}`)
				default:
					t.Errorf("Unexpected request: %s", request)
					w.WriteHeader(http.StatusNotFound)
					_, _ = io.WriteString(w, `{"type":"error","error":"Not found"}`)
				}
			})

			publish := cmdPublish{global: &cmdGlobal{conf: conf}, flagAliases: tt.aliases}
			err := publish.run(nil, []string{"source/snap"})
			require.EqualError(t, err, "Test reached publish request")
			require.Len(t, requests, len(tt.requests))
			for _, request := range tt.requests {
				require.Equal(t, request, <-requests)
			}
		})
	}
}

func TestPublishRemoteSnapshotAliasFailureCleansSource(t *testing.T) {
	for _, failure := range []string{"alias conflict", "alias lookup", "parent lookup"} {
		t.Run(failure, func(t *testing.T) {
			const fingerprint = "published-image"
			deleted := make(chan string, 1)
			operation := `{"id":"publish","status":"Success","status_code":200,"metadata":{"fingerprint":"` + fingerprint + `","type":"container"}}`
			if failure == "parent lookup" {
				operation = `{"id":"publish","status":"Success","status_code":200,"metadata":{"fingerprint":"` + fingerprint + `"}}`
			}

			conf := aliasTestConfig(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /1.0":
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"api_extensions":["instances","image_alias_types"]}}`)
				case "GET /1.0/events":
					upgrader := websocket.Upgrader{}
					conn, err := upgrader.Upgrade(w, r, nil)
					if !assert.NoError(t, err) {
						return
					}

					defer conn.Close()
					for {
						_, _, err = conn.ReadMessage()
						if err != nil {
							return
						}
					}
				case "POST /1.0/images":
					_, _ = io.WriteString(w, `{"type":"async","operation":"/1.0/operations/publish","metadata":`+operation+`}`)
				case "GET /1.0/operations/publish", "GET /1.0/operations/publish/wait":
					_, _ = io.WriteString(w, `{"type":"sync","metadata":`+operation+`}`)
				case "GET /1.0/instances/source":
					w.WriteHeader(http.StatusForbidden)
					_, _ = io.WriteString(w, `{"type":"error","error":"Parent lookup denied","error_code":403}`)
				case "DELETE /1.0/images/" + fingerprint:
					deleted <- fingerprint
					_, _ = io.WriteString(w, `{"type":"async","operation":"/1.0/operations/delete","metadata":{"id":"delete","status":"Success","status_code":200}}`)
				default:
					t.Errorf("Unexpected source request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "Unexpected source request", http.StatusBadRequest)
				}
			})
			targetConf := aliasTestConfig(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /1.0":
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"api_extensions":["instances","image_alias_types"]}}`)
				case "GET /1.0/images/aliases":
					if failure == "alias lookup" {
						w.WriteHeader(http.StatusForbidden)
						_, _ = io.WriteString(w, `{"type":"error","error":"Alias lookup denied","error_code":403}`)
						return
					}

					_, _ = io.WriteString(w, `{"type":"sync","metadata":[{"name":"shared","type":"container","target":"existing-image"}]}`)
				default:
					t.Errorf("Unexpected target request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "Unexpected target request", http.StatusBadRequest)
				}
			})
			conf.Remotes["target"] = targetConf.Remotes["test"]
			serverCert, err := os.ReadFile(targetConf.ServerCertPath("test"))
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(conf.ServerCertPath("target"), serverCert, 0o600))

			publish := cmdPublish{global: &cmdGlobal{conf: conf, flagQuiet: true}, flagAliases: []string{"shared"}}
			err = publish.run(nil, []string{"source/snap", "target:"})
			switch failure {
			case "alias conflict":
				require.EqualError(t, err, "Aliases already exists: shared")
			case "alias lookup":
				require.ErrorContains(t, err, "Alias lookup denied")
			case "parent lookup":
				require.ErrorContains(t, err, "Parent lookup denied")
			}

			select {
			case got := <-deleted:
				require.Equal(t, fingerprint, got)
			default:
				t.Fatal("Failed remote publish left the source image behind")
			}
		})
	}
}

func TestPublishLocalSnapshotAliasRollback(t *testing.T) {
	for _, outcome := range []string{"alias lookup", "alias conflict", "parent lookup", "invalid type", "success"} {
		t.Run(outcome, func(t *testing.T) {
			const fingerprint = "published-image"
			deleted := make(chan string, 2)
			deleteWaited := make(chan struct{}, 2)
			imageType := `"type":"container",`
			switch outcome {
			case "parent lookup":
				imageType = ""
			case "invalid type":
				imageType = `"type":"invalid",`
			}

			operation := `{"id":"publish","status":"Success","status_code":200,"metadata":{` + imageType + `"fingerprint":"` + fingerprint + `"}}`
			conf := aliasTestConfig(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /1.0":
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"api_extensions":["instances","image_alias_types"]}}`)
				case "GET /1.0/events":
					upgrader := websocket.Upgrader{}
					conn, err := upgrader.Upgrade(w, r, nil)
					if !assert.NoError(t, err) {
						return
					}

					defer conn.Close()
					for {
						_, _, err = conn.ReadMessage()
						if err != nil {
							return
						}
					}
				case "POST /1.0/images":
					var image api.ImagesPost
					err := json.NewDecoder(r.Body).Decode(&image)
					assert.NoError(t, err)
					assert.True(t, image.Public)
					_, _ = io.WriteString(w, `{"type":"async","operation":"/1.0/operations/publish","metadata":`+operation+`}`)
				case "GET /1.0/operations/publish", "GET /1.0/operations/publish/wait":
					_, _ = io.WriteString(w, `{"type":"sync","metadata":`+operation+`}`)
				case "GET /1.0/instances/source":
					w.WriteHeader(http.StatusForbidden)
					_, _ = io.WriteString(w, `{"type":"error","error":"Parent lookup denied","error_code":403}`)
				case "GET /1.0/images/aliases":
					if outcome == "alias lookup" {
						w.WriteHeader(http.StatusForbidden)
						_, _ = io.WriteString(w, `{"type":"error","error":"Alias lookup denied","error_code":403}`)
						return
					}

					aliases := `[]`
					if outcome == "alias conflict" {
						aliases = `[{"name":"shared","type":"container","target":"existing-image"}]`
					}

					_, _ = io.WriteString(w, `{"type":"sync","metadata":`+aliases+`}`)
				case "POST /1.0/images/aliases", "POST /1.0/images/container/aliases":
					assert.Equal(t, "success", outcome)
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{}}`)
				case "DELETE /1.0/images/" + fingerprint:
					deleted <- fingerprint
					_, _ = io.WriteString(w, `{"type":"async","operation":"/1.0/operations/delete","metadata":{"id":"delete","status":"Running","status_code":103}}`)
				case "GET /1.0/operations/delete", "GET /1.0/operations/delete/wait":
					deleteWaited <- struct{}{}
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"id":"delete","status":"Success","status_code":200}}`)
				default:
					t.Errorf("Unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "Unexpected request", http.StatusBadRequest)
				}
			})

			publish := cmdPublish{
				global:         &cmdGlobal{conf: conf, flagQuiet: true},
				flagAliases:    []string{"shared"},
				flagMakePublic: true,
			}

			err := publish.run(nil, []string{"source/snap"})
			switch outcome {
			case "alias lookup":
				require.ErrorContains(t, err, "Alias lookup denied")
			case "alias conflict":
				require.EqualError(t, err, "Aliases already exists: shared")
			case "parent lookup":
				require.ErrorContains(t, err, "Parent lookup denied")
			case "invalid type":
				require.Error(t, err)
			case "success":
				require.NoError(t, err)
				require.Empty(t, deleted)
				require.Empty(t, deleteWaited)
				return
			}

			require.Len(t, deleted, 1, "Failed local publish must delete the public image exactly once")
			require.Equal(t, fingerprint, <-deleted)
			require.NotEmpty(t, deleteWaited, "Rollback must wait for image deletion to complete")
		})
	}
}

func TestImageAliasMutationErrors(t *testing.T) {
	const conflict = "Alias exists for both image types"
	for _, tt := range []struct {
		name       string
		extensions string
		imageType  string
		requests   int
		wantError  string
	}{
		{name: "invalid type", imageType: "invalid", wantError: `Invalid image type "invalid"`},
		{name: "explicit type on old server", extensions: `[]`, imageType: "container", requests: 1, wantError: "image_alias_types"},
		{name: "untyped name is ambiguous", extensions: `["image_alias_types"]`, requests: 2, wantError: conflict},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := make(chan string, 8)
			conf := aliasTestConfig(t, func(w http.ResponseWriter, r *http.Request) {
				requests <- r.Method + " " + r.URL.Path
				if r.URL.Path == "/1.0" {
					_, _ = io.WriteString(w, `{"type":"sync","metadata":{"api_extensions":`+tt.extensions+`}}`)
					return
				}

				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"type":"error","error":"`+conflict+`"}`)
			})

			global := &cmdGlobal{conf: conf}
			del := cmdImageAliasDelete{global: global, flagType: tt.imageType}
			rename := cmdImageAliasRename{global: global, flagType: tt.imageType}
			for _, operation := range []struct {
				call    func() error
				request string
			}{
				{func() error { return del.run(nil, []string{"shared"}) }, "DELETE /1.0/images/aliases/shared"},
				{func() error { return rename.run(nil, []string{"shared", "new"}) }, "POST /1.0/images/aliases/shared"},
			} {
				err := operation.call()
				require.ErrorContains(t, err, tt.wantError)
				require.Len(t, requests, tt.requests)
				if tt.requests > 0 {
					require.Equal(t, "GET /1.0", <-requests)
				}

				if tt.wantError == conflict {
					require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
					require.Equal(t, operation.request, <-requests)
				}
			}
		})
	}
}

func (s *utilsTestSuite) TestIsAliasesSubsetTrue() {
	a1 := []api.ImageAlias{
		{Name: "foo"},
	}

	a2 := []api.ImageAlias{
		{Name: "foo"},
		{Name: "bar"},
		{Name: "baz"},
	}

	s.True(IsAliasesSubset(a1, a2))
}

func (s *utilsTestSuite) TestIsAliasesSubsetFalse() {
	a1 := []api.ImageAlias{
		{Name: "foo"},
		{Name: "bar"},
	}

	a2 := []api.ImageAlias{
		{Name: "foo"},
		{Name: "baz"},
	}

	s.False(IsAliasesSubset(a1, a2))
}

func (s *utilsTestSuite) TestGetExistingAliases() {
	images := []api.ImageAliasesEntry{
		{Name: "foo"},
		{Name: "bar"},
		{Name: "baz"},
	}

	aliases := GetExistingAliases([]string{"bar", "foo", "other"}, "", images)
	s.Exactly([]api.ImageAliasesEntry{images[0], images[1]}, aliases)
}

func (s *utilsTestSuite) TestGetExistingAliasesEmpty() {
	images := []api.ImageAliasesEntry{
		{Name: "foo"},
		{Name: "bar"},
		{Name: "baz"},
	}

	aliases := GetExistingAliases([]string{"other1", "other2"}, "", images)
	s.Exactly([]api.ImageAliasesEntry{}, aliases)
}

func (s *utilsTestSuite) TestGetExistingAliasesType() {
	images := []api.ImageAliasesEntry{
		{Name: "foo", Type: "container"},
		{Name: "foo", Type: "virtual-machine"},
		{Name: "bar", Type: "container"},
	}

	aliases := GetExistingAliases([]string{"bar", "foo"}, "container", images)
	s.Exactly([]api.ImageAliasesEntry{images[0], images[2]}, aliases)
}

func (s *utilsTestSuite) TestStructHasFields() {
	s.True(structHasField(reflect.TypeFor[api.Image](), "type"))
	s.True(structHasField(reflect.TypeFor[api.Image](), "public"))
	s.False(structHasField(reflect.TypeFor[api.Image](), "foo"))
}

func (s *utilsTestSuite) TestGetServerSupportedFilters() {
	filters := []string{
		"foo", "type=container", "user.blah=a", "status=running,stopped",
	}

	supportedFilters, unsupportedFilters := getServerSupportedFilters(filters, api.InstanceFull{})
	s.Equal([]string{"type=container"}, supportedFilters)
	s.Equal([]string{"foo", "user.blah=a", "status=running,stopped"}, unsupportedFilters)
}

func (s *utilsTestSuite) TestResolveRegistryImageSource() {
	confRemotes := map[string]config.Remote{
		"local": {
			Addr:    "https://127.0.0.1:8443",
			Project: "my-project",
		},
		"remote1": {
			Addr:     "https://images.example.com",
			Protocol: "simplestreams",
			Project:  "default",
		},
		"no-project": {
			Addr: "https://127.0.0.1:8443",
		},
	}

	tests := []struct {
		name            string
		imgRemote       string
		imgRef          string
		instRemote      string
		projectOverride string
		wantFingerprint string
		wantProject     string
		wantRegistry    string
	}{
		{
			name:            "Local image with project override",
			imgRemote:       "local",
			imgRef:          "abc123",
			instRemote:      "local",
			projectOverride: "custom-project",
			wantFingerprint: "abc123",
			wantProject:     "custom-project",
			wantRegistry:    "",
		},
		{
			name:            "Local image with remote project",
			imgRemote:       "local",
			imgRef:          "abc123",
			instRemote:      "local",
			projectOverride: "",
			wantFingerprint: "abc123",
			wantProject:     "my-project",
			wantRegistry:    "",
		},
		{
			name:            "Local image falls back to default project",
			imgRemote:       "no-project",
			imgRef:          "abc123",
			instRemote:      "no-project",
			projectOverride: "",
			wantFingerprint: "abc123",
			wantProject:     api.ProjectDefaultName,
			wantRegistry:    "",
		},
		{
			name:            "Empty image remote defaults to instance remote",
			imgRemote:       "",
			imgRef:          "abc123",
			instRemote:      "local",
			projectOverride: "",
			wantFingerprint: "abc123",
			wantProject:     "my-project",
			wantRegistry:    "",
		},
		{
			name:            "Remote image from image registry",
			imgRemote:       "remote1",
			imgRef:          "noble",
			instRemote:      "local",
			projectOverride: "",
			wantFingerprint: "noble",
			wantProject:     "",
			wantRegistry:    "remote1",
		},
		{
			name:            "Remote image from image registry ignores project override",
			imgRemote:       "remote1",
			imgRef:          "noble",
			instRemote:      "local",
			projectOverride: "custom-project",
			wantFingerprint: "noble",
			wantProject:     "",
			wantRegistry:    "remote1",
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			imgInfo, registryName := resolveRegistryImageSource(confRemotes, tc.imgRemote, tc.imgRef, tc.instRemote, tc.projectOverride)

			s.Equal(tc.wantFingerprint, imgInfo.Fingerprint)
			s.Equal(tc.wantProject, imgInfo.Project)
			s.Equal(tc.wantRegistry, registryName)
		})
	}
}

func (s *utilsTestSuite) TestNBDRemoveStaleSocket() {
	// A directory named after the test can push the socket paths past the 104 byte sun_path limit of macOS.
	dir, err := os.MkdirTemp("", "lxc")
	s.Require().NoError(err)
	defer func() { _ = os.RemoveAll(dir) }()

	s.Run("Missing path", func() {
		s.NoError(nbdRemoveStaleSocket(filepath.Join(dir, "missing.sock")))
	})

	s.Run("Stale socket is deleted", func() {
		path := filepath.Join(dir, "stale.sock")
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		s.Require().NoError(err)
		listener.SetUnlinkOnClose(false)
		_ = listener.Close()

		s.NoError(nbdRemoveStaleSocket(path))
		s.NoFileExists(path)
	})

	s.Run("Live socket is kept", func() {
		path := filepath.Join(dir, "live.sock")
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		s.Require().NoError(err)
		defer func() { _ = listener.Close() }()

		s.Error(nbdRemoveStaleSocket(path))
		s.FileExists(path)
	})

	s.Run("Regular file is kept", func() {
		path := filepath.Join(dir, "file")
		s.Require().NoError(os.WriteFile(path, nil, 0o600))

		s.NoError(nbdRemoveStaleSocket(path))
		s.FileExists(path)
	})
}

func (s *utilsTestSuite) TestFormatTime() {
	// Create a fixed time for testing: 2026-08-10 12:30:45 UTC.
	fixedTime := time.Date(2026, 8, 10, 12, 30, 45, 0, time.UTC)

	// Create timezone locations.
	estLocation, err := time.LoadLocation("America/New_York")
	s.NoError(err)

	pstLocation, err := time.LoadLocation("America/Los_Angeles")
	s.NoError(err)

	// Create a zero time (Unix timestamp 0).
	zeroTime := time.Unix(0, 0)

	// Create an unset time (negative Unix timestamp).
	unsetTime := time.Unix(-1, 0)

	tests := []struct {
		name     string
		t        *time.Time
		location *time.Location
		want     string
	}{
		{
			name: "Nil time pointer",
			t:    nil,
			want: "",
		},
		{
			name: "Zero time",
			t:    &zeroTime,
			want: "",
		},
		{
			name: "Unset time (negative Unix timestamp)",
			t:    &unsetTime,
			want: "",
		},
		{
			name:     "Valid time in UTC",
			t:        &fixedTime,
			location: time.UTC,
			want:     "2026/08/10 12:30 UTC",
		},
		{
			name:     "Valid time in EST",
			t:        &fixedTime,
			location: estLocation,
			want:     "2026/08/10 08:30 EDT",
		},
		{
			name:     "Valid time in PST",
			t:        &fixedTime,
			location: pstLocation,
			want:     "2026/08/10 05:30 PDT",
		},
	}

	localTimeLocation := time.Local
	for _, tc := range tests {
		s.Run(tc.name, func() {
			if tc.location != nil {
				time.Local = tc.location
				defer func() {
					time.Local = localTimeLocation
				}()
			}

			got := formatTime(tc.t)
			s.Equal(tc.want, got)
		})
	}
}

func (s *utilsTestSuite) TestPrintTimeIfSet() {
	// Create a fixed time for testing: 2026-08-10 12:30:45 UTC.
	fixedTime := time.Date(2026, 8, 10, 12, 30, 45, 0, time.UTC)

	// Create a zero time (Unix timestamp 0).
	zeroTime := time.Unix(0, 0)

	// Create timezone locations.
	estLocation, err := time.LoadLocation("America/New_York")
	s.NoError(err)

	// Helper to create string pointers.
	strPtr := func(s string) *string {
		return &s
	}

	tests := []struct {
		name     string
		prefix   string
		t        *time.Time
		fallback *string
		location *time.Location
		want     string
	}{
		{
			name:     "Valid time with prefix",
			prefix:   "Created:",
			t:        &fixedTime,
			fallback: nil,
			location: time.UTC,
			want:     "Created: 2026/08/10 12:30 UTC\n",
		},
		{
			name:     "Valid time in EST timezone",
			prefix:   "Modified:",
			t:        &fixedTime,
			fallback: nil,
			location: estLocation,
			want:     "Modified: 2026/08/10 08:30 EDT\n",
		},
		{
			name:     "Nil time with no fallback returns empty",
			prefix:   "Created:",
			t:        nil,
			fallback: nil,
			location: time.UTC,
			want:     "",
		},
		{
			name:     "Zero time with no fallback returns empty",
			prefix:   "Created:",
			t:        &zeroTime,
			fallback: nil,
			location: time.UTC,
			want:     "",
		},
		{
			name:     "Nil time with fallback uses fallback",
			prefix:   "Created:",
			t:        nil,
			fallback: strPtr("MISSING"),
			location: time.UTC,
			want:     "Created: MISSING\n",
		},
		{
			name:     "Zero time with fallback uses fallback",
			prefix:   "Created:",
			t:        &zeroTime,
			fallback: strPtr("never"),
			location: time.UTC,
			want:     "Created: never\n",
		},
		{
			name:     "Valid time ignores fallback",
			prefix:   "Updated:",
			t:        &fixedTime,
			fallback: strPtr("FALLBACK_IGNORED"),
			location: time.UTC,
			want:     "Updated: 2026/08/10 12:30 UTC\n",
		},
		{
			name:     "Empty prefix with valid time",
			prefix:   "",
			t:        &fixedTime,
			fallback: nil,
			location: time.UTC,
			want:     "2026/08/10 12:30 UTC\n",
		},
	}

	localTimeLocation := time.Local
	for _, tc := range tests {
		s.Run(tc.name, func() {
			if tc.location != nil {
				time.Local = tc.location
				defer func() {
					time.Local = localTimeLocation
				}()
			}

			buf := &bytes.Buffer{}
			printTimeIfSet(buf, tc.prefix, tc.t, tc.fallback)
			s.Equal(tc.want, buf.String())
		})
	}
}

func TestNormalizeImageAliasPermissionEntitlement(t *testing.T) {
	conf := aliasTestConfig(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/1.0", r.URL.Path)
		_, _ = io.WriteString(w, `{"type":"sync","metadata":{"api_extensions":["image_alias_types"]}}`)
	})
	server, err := conf.GetInstanceServer("test")
	require.NoError(t, err)
	defer server.Disconnect()

	const container = "/1.0/images/container/aliases/shared?project=default"
	const vm = "/1.0/images/virtual-machine/aliases/shared?project=default"
	permissions := []api.Permission{
		{EntityType: "image_alias", EntityReference: container, Entitlement: "can_view"},
		{EntityType: "image_alias", EntityReference: vm, Entitlement: "can_view"},
		{EntityType: "image_alias", EntityReference: vm, Entitlement: "can_edit"},
	}

	for _, tt := range []struct {
		entitlement string
		ambiguous   bool
	}{
		{"can_edit", false},
		{"can_view", true},
	} {
		t.Run(tt.entitlement, func(t *testing.T) {
			permission := &api.Permission{
				EntityType: "image_alias", EntityReference: "/1.0/images/aliases/shared?project=default", Entitlement: tt.entitlement,
			}

			normalized, err := normalizeImageAliasPermission(server, permissions, permission)
			if tt.ambiguous {
				require.ErrorContains(t, err, "both image types")
				return
			}

			require.NoError(t, err)
			require.Equal(t, vm, normalized.EntityReference)
		})
	}
}
