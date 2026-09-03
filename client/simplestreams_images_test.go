package lxd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/simplestreams"
)

func computeCombinedFingerprint(metaContent []byte, rootfsContent []byte) string {
	hash := sha256.New()
	_, _ = hash.Write(metaContent)
	_, _ = hash.Write(rootfsContent)
	return hex.EncodeToString(hash.Sum(nil))
}

func newTestSimpleStreamsServer(t *testing.T, metaPath string, metaContent []byte, rootfsPath string, rootfsContent []byte, combinedFingerprint string, sourceRootfs []byte, deltaContent []byte) *httptest.Server {
	t.Helper()

	metaHash := sha256.Sum256(metaContent)
	rootfsHash := sha256.Sum256(rootfsContent)
	currentItems := map[string]simplestreams.ProductVersionItem{
		"lxd.tar.xz": {
			FileType:              "lxd.tar.xz",
			HashSha256:            hex.EncodeToString(metaHash[:]),
			Size:                  int64(len(metaContent)),
			Path:                  metaPath,
			LXDHashSha256SquashFs: combinedFingerprint,
		},
		"root.squashfs": {
			FileType:   "squashfs",
			HashSha256: hex.EncodeToString(rootfsHash[:]),
			Size:       int64(len(rootfsContent)),
			Path:       rootfsPath,
		},
	}

	versions := map[string]simplestreams.ProductVersion{
		"20260102_0000": {
			Items: currentItems,
		},
	}

	if deltaContent != nil {
		sourceMeta := []byte("old-metadata")
		sourceMetaHash := sha256.Sum256(sourceMeta)
		sourceRootfsHash := sha256.Sum256(sourceRootfs)
		sourceFingerprint := computeCombinedFingerprint(sourceMeta, sourceRootfs)
		deltaHash := sha256.Sum256(deltaContent)

		versions["20260101_0000"] = simplestreams.ProductVersion{
			Items: map[string]simplestreams.ProductVersionItem{
				"lxd.tar.xz": {
					FileType:              "lxd.tar.xz",
					HashSha256:            hex.EncodeToString(sourceMetaHash[:]),
					Size:                  int64(len(sourceMeta)),
					Path:                  "images/test/old-meta.tar.xz",
					LXDHashSha256SquashFs: sourceFingerprint,
				},
				"root.squashfs": {
					FileType:   "squashfs",
					HashSha256: hex.EncodeToString(sourceRootfsHash[:]),
					Size:       int64(len(sourceRootfs)),
					Path:       "images/test/old-rootfs.squashfs",
				},
			},
		}

		currentItems["root.squashfs.vcdiff"] = simplestreams.ProductVersionItem{
			FileType:   "squashfs.vcdiff",
			HashSha256: hex.EncodeToString(deltaHash[:]),
			Size:       int64(len(deltaContent)),
			Path:       "images/test/rootfs.vcdiff",
			DeltaBase:  "20260101_0000",
		}
	}

	products := simplestreams.Products{
		ContentID: "images",
		DataType:  "image-downloads",
		Format:    "products:1.0",
		Products: map[string]simplestreams.Product{
			"test:amd64:default": {
				Aliases:         "test",
				Architecture:    "amd64",
				OperatingSystem: "Test",
				Release:         "test",
				ReleaseTitle:    "Test",
				Versions:        versions,
			},
		},
	}

	productsJSON, err := json.Marshal(products)
	require.NoError(t, err)

	index := simplestreams.Stream{
		Index: map[string]simplestreams.StreamIndex{
			"images": {
				DataType: "image-downloads",
				Path:     "streams/v1/images.json",
				Products: []string{"test:amd64:default"},
			},
		},
		Format: "index:1.0",
	}

	indexJSON, err := json.Marshal(index)
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc("/streams/v1/index.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(indexJSON)
	})

	mux.HandleFunc("/streams/v1/images.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(productsJSON)
	})

	mux.HandleFunc(path.Clean("/"+metaPath), func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(metaContent)
	})

	mux.HandleFunc(path.Clean("/"+rootfsPath), func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(rootfsContent)
	})

	if deltaContent != nil {
		mux.HandleFunc("/images/test/rootfs.vcdiff", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(deltaContent)
		})
	}

	return httptest.NewTLSServer(mux)
}

func newTestSimpleStream(server *httptest.Server) *ProtocolSimpleStreams {
	return &ProtocolSimpleStreams{
		ssClient:      simplestreams.NewClient(server.URL, *server.Client(), "test"),
		http:          server.Client(),
		httpHost:      server.URL,
		httpUserAgent: "test",
	}
}

func newImageTargets(t *testing.T) (*os.File, *os.File) {
	t.Helper()

	metaFile, err := os.Create(filepath.Join(t.TempDir(), "meta"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = metaFile.Close() })

	rootfsFile, err := os.Create(filepath.Join(t.TempDir(), "rootfs"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rootfsFile.Close() })

	return metaFile, rootfsFile
}

func TestGetImageFileSanitizesFilePaths(t *testing.T) {
	metaContent := []byte("fake-metadata-content")
	rootfsContent := []byte("fake-rootfs-content")
	combinedFingerprint := computeCombinedFingerprint(metaContent, rootfsContent)

	tests := []struct {
		name           string
		metaPath       string
		rootfsPath     string
		wantMetaName   string
		wantRootfsName string
	}{
		{
			name:           "absolute paths",
			metaPath:       "/etc/cron.d/evil.tar.xz",
			rootfsPath:     "/etc/cron.d/evil.squashfs",
			wantMetaName:   "evil.tar.xz",
			wantRootfsName: "evil.squashfs",
		},
		{
			name:           "relative traversal paths",
			metaPath:       "images/../../evil.tar.xz",
			rootfsPath:     "images/../../../evil.squashfs",
			wantMetaName:   "evil.tar.xz",
			wantRootfsName: "evil.squashfs",
		},
		{
			name:           "trailing slashes",
			metaPath:       "images/test/evil.tar.xz/",
			rootfsPath:     "images/test/evil.squashfs/",
			wantMetaName:   "evil.tar.xz",
			wantRootfsName: "evil.squashfs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newTestSimpleStreamsServer(t, tt.metaPath, metaContent, tt.rootfsPath, rootfsContent, combinedFingerprint, nil, nil)
			defer server.Close()

			metaFile, rootfsFile := newImageTargets(t)
			resp, err := newTestSimpleStream(server).GetImageFile(combinedFingerprint, ImageFileRequest{
				MetaFile:   metaFile,
				RootfsFile: rootfsFile,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.wantMetaName, resp.MetaName)
			assert.Equal(t, tt.wantRootfsName, resp.RootfsName)
		})
	}
}

func TestGetImageFileDeltaSanitizesRootfsPath(t *testing.T) {
	if _, err := exec.LookPath("xdelta3"); err != nil {
		t.Skip("Missing xdelta3")
	}

	sourceRootfs := []byte("source-rootfs-content-for-delta-test")
	newRootfs := []byte("new-rootfs-content-for-delta-test")
	newMeta := []byte("new-metadata-content")

	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.squashfs")
	newPath := filepath.Join(tmpDir, "new.squashfs")
	deltaPath := filepath.Join(tmpDir, "delta.vcdiff")
	require.NoError(t, os.WriteFile(sourcePath, sourceRootfs, 0600))
	require.NoError(t, os.WriteFile(newPath, newRootfs, 0600))

	output, err := exec.Command("xdelta3", "-f", "-e", "-s", sourcePath, newPath, deltaPath).CombinedOutput()
	require.NoError(t, err, "xdelta3 encode failed: %s", string(output))

	deltaContent, err := os.ReadFile(deltaPath)
	require.NoError(t, err)

	combinedFingerprint := computeCombinedFingerprint(newMeta, newRootfs)
	server := newTestSimpleStreamsServer(t, "images/test/meta.tar.xz", newMeta, "/etc/cron.d/evil.squashfs/", newRootfs, combinedFingerprint, sourceRootfs, deltaContent)
	defer server.Close()

	sourceMeta := []byte("old-metadata")
	sourceFingerprint := computeCombinedFingerprint(sourceMeta, sourceRootfs)
	cachedRootfsPath := filepath.Join(tmpDir, "cached-rootfs.squashfs")
	require.NoError(t, os.WriteFile(cachedRootfsPath, sourceRootfs, 0600))

	metaFile, rootfsFile := newImageTargets(t)
	resp, err := newTestSimpleStream(server).GetImageFile(combinedFingerprint, ImageFileRequest{
		MetaFile:   metaFile,
		RootfsFile: rootfsFile,
		DeltaSourceRetriever: func(fingerprint string, filename string) string {
			if fingerprint == sourceFingerprint && filename == "rootfs" {
				return cachedRootfsPath
			}

			return ""
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "meta.tar.xz", resp.MetaName)
	assert.Equal(t, "evil.squashfs", resp.RootfsName)
	assert.Equal(t, int64(len(newRootfs)), resp.RootfsSize)
}
