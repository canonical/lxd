package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

func TestImageEndpointResolver(t *testing.T) {
	tests := []struct {
		name              string
		path              string
		rawPath           string // If set, simulates URL-encoded path
		wantEndpoint      *APIEndpoint
		wantPathValueKey  string
		wantPathValueVal  string
		wantPathValueKey2 string
		wantPathValueVal2 string
	}{
		// Alias routes
		{
			name:             "alias simple name",
			path:             "/1.0/images/aliases/myalias",
			wantEndpoint:     &imageAliasCmd,
			wantPathValueKey: "name",
			wantPathValueVal: "myalias",
		},
		{
			name:         "alias with unescaped slash in name (path is too long)",
			path:         "/1.0/images/aliases/my/alias",
			wantEndpoint: nil,
		},
		{
			name:             "alias with encoded slash",
			path:             "/1.0/images/aliases/my/alias",
			rawPath:          "/1.0/images/aliases/my%2Falias",
			wantEndpoint:     &imageAliasCmd,
			wantPathValueKey: "name",
			wantPathValueVal: "my/alias",
		},
		{
			name:             "alias with spaces",
			path:             "/1.0/images/aliases/my alias",
			rawPath:          "/1.0/images/aliases/my%20alias",
			wantEndpoint:     &imageAliasCmd,
			wantPathValueKey: "name",
			wantPathValueVal: "my alias",
		},
		{
			name:             "alias ubuntu/noble",
			path:             "/1.0/images/aliases/ubuntu/noble",
			rawPath:          "/1.0/images/aliases/ubuntu%2Fnoble",
			wantEndpoint:     &imageAliasCmd,
			wantPathValueKey: "name",
			wantPathValueVal: "ubuntu/noble",
		},

		// Fingerprint routes
		{
			name:             "fingerprint only",
			path:             "/1.0/images/abc123def456",
			wantEndpoint:     &imageCmd,
			wantPathValueKey: "fingerprint",
			wantPathValueVal: "abc123def456",
		},
		{
			name:             "fingerprint export",
			path:             "/1.0/images/abc123def456/export",
			wantEndpoint:     &imageExportCmd,
			wantPathValueKey: "fingerprint",
			wantPathValueVal: "abc123def456",
		},
		{
			name:             "fingerprint secret",
			path:             "/1.0/images/abc123def456/secret",
			wantEndpoint:     &imageSecretCmd,
			wantPathValueKey: "fingerprint",
			wantPathValueVal: "abc123def456",
		},
		{
			name:             "fingerprint refresh",
			path:             "/1.0/images/abc123def456/refresh",
			wantEndpoint:     &imageRefreshCmd,
			wantPathValueKey: "fingerprint",
			wantPathValueVal: "abc123def456",
		},

		// Unknown action returns nil
		{
			name:             "fingerprint unknown action",
			path:             "/1.0/images/abc123def456/unknown",
			wantEndpoint:     nil,
			wantPathValueKey: "fingerprint",
			wantPathValueVal: "abc123def456",
		},

		// Base path returns nil (404)
		{
			name:         "base images path",
			path:         "/1.0/images/",
			wantEndpoint: nil,
		},
		{
			name:         "base aliases path",
			path:         "/1.0/images/aliases/",
			wantEndpoint: nil,
		},

		// Invalid URL escape sequences are treated as literals
		{
			name:             "typed alias collection",
			path:             "/1.0/images/container/aliases",
			wantEndpoint:     &imageTypeAliasesCmd,
			wantPathValueKey: "imageType",
			wantPathValueVal: "container",
		},
		{
			name:              "typed alias name",
			path:              "/1.0/images/container/aliases/jammy",
			wantEndpoint:      &imageTypeAliasCmd,
			wantPathValueKey:  "imageType",
			wantPathValueVal:  "container",
			wantPathValueKey2: "name",
			wantPathValueVal2: "jammy",
		},
		{
			name:              "typed alias with encoded slash",
			path:              "/1.0/images/virtual-machine/aliases/ubuntu/noble",
			rawPath:           "/1.0/images/virtual-machine/aliases/ubuntu%2Fnoble",
			wantEndpoint:      &imageTypeAliasCmd,
			wantPathValueKey:  "imageType",
			wantPathValueVal:  "virtual-machine",
			wantPathValueKey2: "name",
			wantPathValueVal2: "ubuntu/noble",
		},
		{
			name:             "untyped alias whose name starts with container",
			path:             "/1.0/images/aliases/container/jammy",
			rawPath:          "/1.0/images/aliases/container%2Fjammy",
			wantEndpoint:     &imageAliasCmd,
			wantPathValueKey: "name",
			wantPathValueVal: "container/jammy",
		},
		{
			name:         "image type without aliases",
			path:         "/1.0/images/container",
			wantEndpoint: nil,
		},

		{
			name:             "invalid escape in alias",
			path:             "/1.0/images/aliases/bad%2",
			rawPath:          "/1.0/images/aliases/bad%2",
			wantEndpoint:     &imageAliasCmd,
			wantPathValueKey: "name",
			wantPathValueVal: "bad%2",
		},
		{
			name:             "invalid escape in fingerprint",
			path:             "/1.0/images/bad%2/export",
			rawPath:          "/1.0/images/bad%2/export",
			wantEndpoint:     &imageExportCmd,
			wantPathValueKey: "fingerprint",
			wantPathValueVal: "bad%2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &url.URL{Path: tt.path, RawPath: tt.rawPath}
			r := &http.Request{URL: u}

			got := imageEndpointResolver(r)

			if got != tt.wantEndpoint {
				var gotPath, wantPath string
				if got != nil {
					gotPath = got.Path
				}

				if tt.wantEndpoint != nil {
					wantPath = tt.wantEndpoint.Path
				}

				t.Errorf("imageEndpointResolver() = %s: %v (Path: %q), want %v (Path: %q)", tt.name, got, gotPath, tt.wantEndpoint, wantPath)
			}

			if tt.wantPathValueKey != "" && tt.wantEndpoint != nil {
				gotVal := r.PathValue(tt.wantPathValueKey)
				if gotVal != tt.wantPathValueVal {
					t.Errorf("PathValue(%q) = %q, want %q", tt.wantPathValueKey, gotVal, tt.wantPathValueVal)
				}
			}

			if tt.wantPathValueKey2 != "" && tt.wantEndpoint != nil {
				gotVal := r.PathValue(tt.wantPathValueKey2)
				if gotVal != tt.wantPathValueVal2 {
					t.Errorf("PathValue(%q) = %q, want %q", tt.wantPathValueKey2, gotVal, tt.wantPathValueVal2)
				}
			}
		})
	}
}

func TestLocalImageCopyAliasSelection(t *testing.T) {
	server, container, vm, legacy := newImageAliasTestServer(t)
	for _, fingerprint := range []string{container, vm} {
		err := server.CreateImageAlias(api.ImageAliasesPost{
			ImageAliasesEntry: api.ImageAliasesEntry{Name: "release", Target: fingerprint},
		})
		require.NoError(t, err)
	}

	for _, tt := range []struct {
		name        string
		alias       string
		imageType   string
		fingerprint string
	}{
		{name: "container-first", alias: "release", fingerprint: container},
		{name: "explicit-vm", alias: "release", imageType: "virtual-machine", fingerprint: vm},
		{name: "missing", alias: "missing"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			op, err := server.CreateImage(api.ImagesPost{Source: &api.ImagesPostSource{
				Type:        api.SourceTypeImage,
				ImageSource: api.ImageSource{Alias: tt.alias, ImageType: tt.imageType},
			}}, nil)
			if tt.fingerprint == "" {
				require.True(t, api.StatusErrorCheck(err, http.StatusNotFound), "Expected missing source, got %v", err)
				return
			}

			require.NoError(t, err)
			require.NoError(t, op.Wait())
			require.Equal(t, tt.fingerprint, op.Get().Metadata["fingerprint"])
		})
	}

	t.Run("destination-alias-fallback", func(t *testing.T) {
		err := server.CreateProject(api.ProjectsPost{
			Name:       "empty-source",
			ProjectPut: api.ProjectPut{Config: map[string]string{"features.images": "true", "features.profiles": "false"}},
		})
		require.NoError(t, err)
		op, err := server.CreateImage(api.ImagesPost{Source: &api.ImagesPostSource{
			Type: api.SourceTypeImage, Project: "empty-source", ImageSource: api.ImageSource{Alias: "release"},
		}}, nil)
		require.NoError(t, err)
		require.NoError(t, op.Wait())
		require.Equal(t, container, op.Get().Metadata["fingerprint"])
	})

	t.Run("legacy-image-replaces-container-alias", func(t *testing.T) {
		op, err := server.CreateImage(api.ImagesPost{
			Source:  &api.ImagesPostSource{Type: api.SourceTypeImage, Fingerprint: legacy},
			Aliases: []api.ImageAlias{{Name: "release"}},
		}, nil)
		require.NoError(t, err)
		require.NoError(t, op.Wait())
		for imageType, fingerprint := range map[string]string{"container": legacy, "virtual-machine": vm} {
			alias, _, err := server.GetImageAliasType(imageType, "release")
			require.NoError(t, err)
			require.Equal(t, fingerprint, alias.Target)
		}
	})
}

func TestImageAliasLocationsAndLifecycle(t *testing.T) {
	server, container, vm, _ := newImageAliasTestServer(t)
	for _, name := range []string{"release", "renamed"} {
		err := server.CreateImageAlias(api.ImageAliasesPost{
			ImageAliasesEntry: api.ImageAliasesEntry{Name: name, Target: container},
		})
		require.NoError(t, err)
	}

	listener, err := server.GetEvents()
	require.NoError(t, err)
	defer listener.Disconnect()
	events := make(chan api.Event, 16)
	_, err = listener.AddHandler([]string{api.EventTypeLifecycle}, func(event api.Event) { events <- event })
	require.NoError(t, err)

	post := func(path string, body any, action string, name string) {
		t.Helper()
		data, err := json.Marshal(body)
		require.NoError(t, err)
		req, err := http.NewRequest(http.MethodPost, "http://unix.socket"+path, bytes.NewReader(data))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := server.DoHTTP(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		wantURL := entity.ImageAliasURL(api.ProjectDefaultName, "virtual-machine", name).String()
		require.Equal(t, wantURL, resp.Header.Get("Location"))
		require.Equal(t, wantURL, waitImageAliasLifecycleEvent(t, events, action).Source)
	}

	post("/1.0/images/aliases", api.ImageAliasesPost{
		ImageAliasesEntry: api.ImageAliasesEntry{Name: "release", Target: vm},
	}, api.EventLifecycleImageAliasCreated, "release")
	post("/1.0/images/virtual-machine/aliases/release", api.ImageAliasesEntryPost{Name: "renamed"}, api.EventLifecycleImageAliasRenamed, "renamed")

	// The untyped endpoint may retarget to another type, and its event must point to the new type.
	err = server.CreateImageAlias(api.ImageAliasesPost{ImageAliasesEntry: api.ImageAliasesEntry{Name: "retarget", Target: container}})
	require.NoError(t, err)
	err = server.UpdateImageAlias("retarget", api.ImageAliasesEntryPut{Target: vm}, "")
	require.NoError(t, err)
	event := waitImageAliasLifecycleEvent(t, events, api.EventLifecycleImageAliasUpdated)
	require.Equal(t, entity.ImageAliasURL(api.ProjectDefaultName, "virtual-machine", "retarget").String(), event.Source)

	err = server.DeleteImageAliasType("virtual-machine", "renamed")
	require.NoError(t, err)
	event = waitImageAliasLifecycleEvent(t, events, api.EventLifecycleImageAliasDeleted)
	require.Equal(t, entity.ImageAliasURL(api.ProjectDefaultName, "virtual-machine", "renamed").String(), event.Source)
	alias, _, err := server.GetImageAliasType("container", "renamed")
	require.NoError(t, err)
	require.Equal(t, container, alias.Target)
}

func TestAliasMutationRejectsRetargetedIdentity(t *testing.T) {
	t.Setenv("LXD_DIR", t.TempDir())
	daemon, cleanup := newTestDaemon(t)
	defer cleanup()

	container := strings.Repeat("d", 64)
	vm := strings.Repeat("e", 64)
	err := daemon.State().DB.Cluster.Transaction(t.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, `INSERT INTO images (fingerprint, filename, size, architecture, upload_date, project_id, type)
VALUES (?, '', 1, 1, datetime('now'), 1, 0),
       (?, '', 1, 1, datetime('now'), 1, 1)`, container, vm)
		if err != nil {
			return err
		}

		projectName := api.ProjectDefaultName
		containerID, _, err := tx.GetImage(ctx, container, dbCluster.ImageFilter{Project: &projectName})
		if err != nil {
			return err
		}

		return tx.CreateImageAlias(ctx, "default", "shared", containerID, "")
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodDelete, "/1.0/images/aliases/shared", nil)
	req.SetPathValue("name", "shared")
	err = daemon.State().DB.Cluster.Transaction(t.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		aliasID, _, err := tx.GetImageAlias(ctx, "default", "shared", "container", true)
		if err != nil {
			return err
		}

		projectName := api.ProjectDefaultName
		vmID, _, err := tx.GetImage(ctx, vm, dbCluster.ImageFilter{Project: &projectName})
		if err != nil {
			return err
		}

		err = tx.UpdateImageAlias(ctx, aliasID, vmID, "", true)
		if err != nil {
			return err
		}

		request.SetContextValue(req, ctxImageAliasDetails, imageAliasDetails{id: aliasID, imageType: "container"})
		_, _, err = getAuthorizedImageAlias(ctx, tx, "default", req)
		require.True(t, api.StatusErrorCheck(err, http.StatusNotFound), "Expected stale authorized alias rejection, got %v", err)
		return nil
	})
	require.NoError(t, err)
}

func newImageAliasTestServer(t *testing.T) (client lxd.InstanceServer, containerFingerprint string, vmFingerprint string, legacyFingerprint string) {
	t.Helper()
	t.Setenv("LXD_DIR", t.TempDir())
	daemon, cleanup := newTestDaemon(t)
	t.Cleanup(cleanup)
	err := os.MkdirAll(daemon.State().ImagesStoragePath(api.ProjectDefaultName), 0o700)
	require.NoError(t, err)

	container := strings.Repeat("a", 64)
	vm := strings.Repeat("b", 64)
	legacy := strings.Repeat("c", 64)
	err = daemon.State().DB.Cluster.Transaction(t.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, `INSERT INTO images (fingerprint, filename, size, architecture, upload_date, project_id, type)
VALUES (?, '', 1, 1, datetime('now'), 1, 0),
       (?, '', 1, 1, datetime('now'), 1, 1),
       (?, '', 1, 1, datetime('now'), 1, 2)`, container, vm, legacy)
		if err != nil {
			return err
		}

		for _, fingerprint := range []string{container, vm, legacy} {
			err = tx.AddImageToLocalNode(ctx, api.ProjectDefaultName, fingerprint)
			if err != nil {
				return err
			}
		}

		return nil
	})
	require.NoError(t, err)
	server, err := lxd.ConnectLXDUnix(daemon.os.GetUnixSocket(), nil)
	require.NoError(t, err)
	return server, container, vm, legacy
}

func waitImageAliasLifecycleEvent(t *testing.T, events <-chan api.Event, action string) api.EventLifecycle {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case event := <-events:
			var lifecycleEvent api.EventLifecycle
			err := json.Unmarshal(event.Metadata, &lifecycleEvent)
			require.NoError(t, err)
			if lifecycleEvent.Action == action {
				return lifecycleEvent
			}

		case <-timeout.C:
			t.Fatalf("Timed out waiting for lifecycle action %q", action)
		}
	}
}

func TestRemoteAliasCacheImageType(t *testing.T) {
	daemon, cleanup := newTestDaemon(t)
	defer cleanup()
	s := daemon.State()
	container := strings.Repeat("a", 64)
	vm := strings.Repeat("b", 64)
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var metadata any
		switch r.URL.Path {
		case "/1.0":
			metadata = api.Server{ServerUntrusted: api.ServerUntrusted{APIExtensions: []string{"image_alias_types"}}}
		case "/1.0/images/container/aliases/shared":
			metadata = api.ImageAliasesEntry{Target: container, Type: "container"}
		case "/1.0/images/virtual-machine/aliases/shared":
			metadata = api.ImageAliasesEntry{Target: vm, Type: "virtual-machine"}
		case "/1.0/images/" + container:
			metadata = api.Image{Fingerprint: container, Type: "container"}
		case "/1.0/images/" + vm:
			metadata = api.Image{Fingerprint: vm, Type: "virtual-machine"}
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"type":"error","error":"Image not found","error_code":404}`)
			return
		}

		err := json.NewEncoder(w).Encode(map[string]any{"type": "sync", "metadata": metadata})
		if err != nil {
			t.Errorf("Failed encoding remote image response: %v", err)
		}
	}))
	defer remote.Close()

	err := s.DB.Cluster.Transaction(t.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		linkID, err := dbCluster.CreateClusterLink(ctx, tx.Tx(), dbCluster.ClusterLinkRow{
			Name: "alias-cache", Type: dbCluster.ClusterLinkType(api.ClusterLinkTypePublic),
		})
		if err != nil {
			return err
		}

		cert := remote.Certificate()
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
		err = dbCluster.SetClusterLinkCertificate(ctx, tx.Tx(), linkID, shared.CertFingerprint(cert), string(certPEM))
		if err != nil {
			return err
		}

		err = dbCluster.ClusterLinksConfigStore().Set(ctx, tx.Tx(), linkID, map[string]string{
			"volatile.addresses": strings.TrimPrefix(remote.URL, "https://"),
		})
		if err != nil {
			return err
		}

		registryID, err := dbCluster.CreateImageRegistry(ctx, tx.Tx(), dbCluster.ImageRegistriesRow{
			Name: "alias-cache", Protocol: dbCluster.ImageRegistryProtocol(api.ImageRegistryProtocolLXD),
		})
		if err != nil {
			return err
		}

		err = dbCluster.CreateImageRegistryConfig(ctx, tx.Tx(), registryID, map[string]string{
			"cluster": "alias-cache", "source_project": "default",
		})
		if err != nil {
			return err
		}

		// The newer VM cache entry must not override the container selected by the remote alias.
		for imageType, fingerprint := range []string{container, vm} {
			result, err := tx.Tx().ExecContext(ctx, `INSERT INTO images
(fingerprint, filename, size, architecture, creation_date, upload_date, project_id, type, auto_update)
VALUES (?, '', 1, ?, ?, datetime('now'), 1, ?, 1)`, fingerprint, s.OS.Architectures[0], imageType+1, imageType)
			if err != nil {
				return err
			}

			imageID, err := result.LastInsertId()
			if err != nil {
				return err
			}

			err = tx.CreateImageSource(ctx, int(imageID), "alias-cache", "shared")
			if err != nil {
				return err
			}

			err = tx.AddImageToLocalNode(ctx, "default", fingerprint)
			if err != nil {
				return err
			}
		}

		return nil
	})
	require.NoError(t, err)

	for _, tt := range []struct {
		name      string
		imageType string
		alias     string
		want      string
	}{
		{"untyped prefers remote container", "", "shared", container},
		{"microvm uses container", "microvm", "shared", container},
		{"VM keeps its namespace", "virtual-machine", "shared", vm},
		{"wrong fingerprint type is rejected", "container", vm, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			img, err := ensureDownloadedImageFitWithinBudget(t.Context(), s, nil,
				api.Project{Name: "default", Config: map[string]string{"features.images": "true"}},
				tt.alias, api.InstanceSource{ImageRegistry: "alias-cache"}, tt.imageType)
			if tt.want == "" {
				require.True(t, api.StatusErrorCheck(err, http.StatusBadRequest), "Expected type mismatch, got %v", err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, img.Fingerprint)
		})
	}
}
