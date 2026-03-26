package lxd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/simplestreams"
)

// newTestSimpleStreamsServer creates a test HTTPS server that serves simplestreams metadata and
// image files. combinedHash is the fingerprint advertised in the simplestreams index, and
// metaContent and rootfsContent are the actual bytes served.
func newTestSimpleStreamsServer(t *testing.T, metaContent []byte, rootfsContent []byte, combinedHash string) *httptest.Server {
	t.Helper()

	metaHash := sha256.Sum256(metaContent)
	rootfsHash := sha256.Sum256(rootfsContent)

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
				Versions: map[string]simplestreams.ProductVersion{
					"20260101_0000": {
						Items: map[string]simplestreams.ProductVersionItem{
							"lxd.tar.xz": {
								FileType:              "lxd.tar.xz",
								HashSha256:            hex.EncodeToString(metaHash[:]),
								Size:                  int64(len(metaContent)),
								Path:                  "images/test/meta.tar.xz",
								LXDHashSha256SquashFs: combinedHash,
							},
							"root.squashfs": {
								FileType:   "squashfs",
								HashSha256: hex.EncodeToString(rootfsHash[:]),
								Size:       int64(len(rootfsContent)),
								Path:       "images/test/rootfs.squashfs",
							},
						},
					},
				},
			},
		},
	}

	productsJSON, err := json.Marshal(products)
	if err != nil {
		t.Fatal(err)
	}

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
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/streams/v1/index.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(indexJSON)
	})

	mux.HandleFunc("/streams/v1/images.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(productsJSON)
	})

	mux.HandleFunc("/images/test/meta.tar.xz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(metaContent)
	})

	mux.HandleFunc("/images/test/rootfs.squashfs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(rootfsContent)
	})

	return httptest.NewTLSServer(mux)
}

// newTestSimpleStream creates a ProtocolSimpleStreams backed by the given test server.
func newTestSimpleStream(server *httptest.Server) *ProtocolSimpleStreams {
	return &ProtocolSimpleStreams{
		ssClient:      simplestreams.NewClient(server.URL, *server.Client(), "test"),
		http:          server.Client(),
		httpHost:      server.URL,
		httpUserAgent: "test",
	}
}

// computeCombinedFingerprint returns SHA256(meta || rootfs) as a hex string.
func computeCombinedFingerprint(meta, rootfs []byte) string {
	h := sha256.New()
	h.Write(meta)
	h.Write(rootfs)
	return hex.EncodeToString(h.Sum(nil))
}

func TestGetImageFile_CombinedFingerprintValid(t *testing.T) {
	metaContent := []byte("fake-metadata-content")
	rootfsContent := []byte("fake-rootfs-content")
	combinedFP := computeCombinedFingerprint(metaContent, rootfsContent)

	server := newTestSimpleStreamsServer(t, metaContent, rootfsContent, combinedFP)
	defer server.Close()

	images := newTestSimpleStream(server)

	metaFile, err := os.CreateTemp(t.TempDir(), "meta")
	require.NoError(t, err)
	defer metaFile.Close()

	rootfsFile, err := os.CreateTemp(t.TempDir(), "rootfs")
	require.NoError(t, err)
	defer rootfsFile.Close()

	resp, err := images.GetImageFile(combinedFP, ImageFileRequest{
		MetaFile:   metaFile,
		RootfsFile: rootfsFile,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(len(metaContent)), resp.MetaSize)
	assert.Equal(t, int64(len(rootfsContent)), resp.RootfsSize)
}

func TestGetImageFile_CombinedFingerprintMismatch(t *testing.T) {
	metaContent := []byte("fake-metadata-content")
	rootfsContent := []byte("fake-rootfs-content")

	// Use the correct combined fingerprint in the simplestreams index so the
	// image can be looked up, but serve tampered rootfs content so the actual
	// combined hash won't match.
	combinedFP := computeCombinedFingerprint(metaContent, rootfsContent)
	tamperedRootfs := []byte("tampered-rootfs-content")

	// Server advertises combinedFP but serves tampered rootfs with correct
	// individual file hashes for the tampered content.
	server := newTestSimpleStreamsServer(t, metaContent, tamperedRootfs, combinedFP)
	defer server.Close()

	images := newTestSimpleStream(server)

	metaFile, err := os.CreateTemp(t.TempDir(), "meta")
	require.NoError(t, err)
	defer metaFile.Close()

	rootfsFile, err := os.CreateTemp(t.TempDir(), "rootfs")
	require.NoError(t, err)
	defer rootfsFile.Close()

	_, err = images.GetImageFile(combinedFP, ImageFileRequest{
		MetaFile:   metaFile,
		RootfsFile: rootfsFile,
	})

	actualFP := computeCombinedFingerprint(metaContent, tamperedRootfs)
	expectedMsg := fmt.Sprintf("Image fingerprint mismatch. Got %s expected %s", actualFP, combinedFP)
	assert.EqualError(t, err, expectedMsg)
}

// newTestSimpleStreamsServerWithDelta creates a test server that serves a simplestreams index with
// two image versions, a source version and a current version with a delta. The delta allows
// upgrading from srcRootfs to newRootfs.
func newTestSimpleStreamsServerWithDelta(t *testing.T, newMeta []byte, srcRootfs []byte, newRootfs []byte, deltaContent []byte, combinedHash string) *httptest.Server {
	t.Helper()

	newMetaHash := sha256.Sum256(newMeta)
	newRootfsHash := sha256.Sum256(newRootfs)
	srcRootfsHash := sha256.Sum256(srcRootfs)
	deltaHash := sha256.Sum256(deltaContent)

	// The source version's combined fingerprint is used as the key in "root.delta-<srcFP>".
	srcMetaContent := []byte("old-metadata")
	srcMetaHash := sha256.Sum256(srcMetaContent)
	srcCombinedFP := computeCombinedFingerprint(srcMetaContent, srcRootfs)

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
				Versions: map[string]simplestreams.ProductVersion{
					"20260101_0000": {
						Items: map[string]simplestreams.ProductVersionItem{
							"lxd.tar.xz": {
								FileType:              "lxd.tar.xz",
								HashSha256:            hex.EncodeToString(srcMetaHash[:]),
								Size:                  int64(len(srcMetaContent)),
								Path:                  "images/test/old-meta.tar.xz",
								LXDHashSha256SquashFs: srcCombinedFP,
							},
							"root.squashfs": {
								FileType:   "squashfs",
								HashSha256: hex.EncodeToString(srcRootfsHash[:]),
								Size:       int64(len(srcRootfs)),
								Path:       "images/test/old-rootfs.squashfs",
							},
						},
					},
					"20260102_0000": {
						Items: map[string]simplestreams.ProductVersionItem{
							"lxd.tar.xz": {
								FileType:              "lxd.tar.xz",
								HashSha256:            hex.EncodeToString(newMetaHash[:]),
								Size:                  int64(len(newMeta)),
								Path:                  "images/test/meta.tar.xz",
								LXDHashSha256SquashFs: combinedHash,
							},
							"root.squashfs": {
								FileType:   "squashfs",
								HashSha256: hex.EncodeToString(newRootfsHash[:]),
								Size:       int64(len(newRootfs)),
								Path:       "images/test/rootfs.squashfs",
							},
							"root.squashfs.vcdiff": {
								FileType:   "squashfs.vcdiff",
								HashSha256: hex.EncodeToString(deltaHash[:]),
								Size:       int64(len(deltaContent)),
								Path:       "images/test/rootfs.vcdiff",
								DeltaBase:  "20260101_0000",
							},
						},
					},
				},
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

	mux.HandleFunc("/images/test/meta.tar.xz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(newMeta)
	})

	mux.HandleFunc("/images/test/rootfs.squashfs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(newRootfs)
	})

	mux.HandleFunc("/images/test/rootfs.vcdiff", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(deltaContent)
	})

	return httptest.NewTLSServer(mux)
}

func TestGetImageFile_DeltaCombinedFingerprintValid(t *testing.T) {
	srcRootfs := []byte("source-rootfs-content-for-delta-test")
	newRootfs := []byte("new-rootfs-content-for-delta-test")
	newMeta := []byte("new-metadata-content")

	// Generate delta using xdelta3.
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "src.squashfs")
	newPath := filepath.Join(tmpDir, "new.squashfs")
	deltaPath := filepath.Join(tmpDir, "delta.vcdiff")

	require.NoError(t, os.WriteFile(srcPath, srcRootfs, 0644))
	require.NoError(t, os.WriteFile(newPath, newRootfs, 0644))

	out, err := exec.Command("xdelta3", "-f", "-e", "-s", srcPath, newPath, deltaPath).CombinedOutput()
	require.NoError(t, err, "xdelta3 encode failed: %s", string(out))

	deltaContent, err := os.ReadFile(deltaPath)
	require.NoError(t, err)

	// The combined fingerprint must be SHA256 of new meta and rootfs, not involving the delta.
	combinedFP := computeCombinedFingerprint(newMeta, newRootfs)

	server := newTestSimpleStreamsServerWithDelta(t, newMeta, srcRootfs, newRootfs, deltaContent, combinedFP)
	defer server.Close()

	images := newTestSimpleStream(server)

	// Write the source rootfs to a file that DeltaSourceRetriever can find.
	srcMetaContent := []byte("old-metadata")
	srcCombinedFP := computeCombinedFingerprint(srcMetaContent, srcRootfs)
	srcRootfsPath := filepath.Join(tmpDir, "cached-rootfs.squashfs")
	require.NoError(t, os.WriteFile(srcRootfsPath, srcRootfs, 0644))

	metaFile, err := os.CreateTemp(t.TempDir(), "meta")
	require.NoError(t, err)
	defer metaFile.Close()

	rootfsFile, err := os.CreateTemp(t.TempDir(), "rootfs")
	require.NoError(t, err)
	defer rootfsFile.Close()

	resp, err := images.GetImageFile(combinedFP, ImageFileRequest{
		MetaFile:   metaFile,
		RootfsFile: rootfsFile,
		DeltaSourceRetriever: func(fingerprint string, fname string) string {
			if fingerprint == srcCombinedFP && fname == "rootfs" {
				return srcRootfsPath
			}

			return ""
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(len(newMeta)), resp.MetaSize)
	assert.Equal(t, int64(len(newRootfs)), resp.RootfsSize)
}

func TestGetImageFile_DeltaPerFileHashMismatch(t *testing.T) {
	srcRootfs := []byte("source-rootfs-content-for-delta-test")
	newRootfs := []byte("new-rootfs-content-for-delta-test")
	newMeta := []byte("new-metadata-content")

	// Generate delta using xdelta3.
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "src.squashfs")

	require.NoError(t, os.WriteFile(srcPath, srcRootfs, 0644))

	// Use correct combined FP for the index lookup, but tamper the delta so
	// the patched rootfs won't match.
	combinedFP := computeCombinedFingerprint(newMeta, newRootfs)

	// Create a tampered delta from srcRootfs -> tamperedRootfs.
	tamperedRootfs := []byte("tampered-rootfs-content-for-delta-test")
	tamperedPath := filepath.Join(tmpDir, "tampered.squashfs")
	tamperedDeltaPath := filepath.Join(tmpDir, "tampered-delta.vcdiff")

	require.NoError(t, os.WriteFile(tamperedPath, tamperedRootfs, 0644))

	out, err := exec.Command("xdelta3", "-f", "-e", "-s", srcPath, tamperedPath, tamperedDeltaPath).CombinedOutput()
	require.NoError(t, err, "xdelta3 encode failed: %s", string(out))

	tamperedDeltaContent, err := os.ReadFile(tamperedDeltaPath)
	require.NoError(t, err)

	// Server advertises combinedFP but serves the tampered delta.
	server := newTestSimpleStreamsServerWithDelta(t, newMeta, srcRootfs, newRootfs, tamperedDeltaContent, combinedFP)
	defer server.Close()

	images := newTestSimpleStream(server)

	srcMetaContent := []byte("old-metadata")
	srcCombinedFP := computeCombinedFingerprint(srcMetaContent, srcRootfs)
	srcRootfsPath := filepath.Join(tmpDir, "cached-rootfs.squashfs")
	require.NoError(t, os.WriteFile(srcRootfsPath, srcRootfs, 0644))

	metaFile, err := os.CreateTemp(t.TempDir(), "meta")
	require.NoError(t, err)
	defer metaFile.Close()

	rootfsFile, err := os.CreateTemp(t.TempDir(), "rootfs")
	require.NoError(t, err)
	defer rootfsFile.Close()

	_, err = images.GetImageFile(combinedFP, ImageFileRequest{
		MetaFile:   metaFile,
		RootfsFile: rootfsFile,
		DeltaSourceRetriever: func(fingerprint string, fname string) string {
			if fingerprint == srcCombinedFP && fname == "rootfs" {
				return srcRootfsPath
			}

			return ""
		},
	})

	tamperedHash := sha256.Sum256(tamperedRootfs)
	newRootfsHash := sha256.Sum256(newRootfs)
	expectedMsg := fmt.Sprintf("Patched rootfs hash mismatch after applying delta. Got %s expected %s", hex.EncodeToString(tamperedHash[:]), hex.EncodeToString(newRootfsHash[:]))
	assert.EqualError(t, err, expectedMsg)
}

// TestGetImageFile_DeltaCombinedFingerprintMismatch verifies that when a delta applies successfully
// and the patched rootfs passes its per-file SHA256 check, the final combined fingerprint
// validation still catches a mismatch. This is done by advertising a wrong combined fingerprint
// in the simplestreams index while keeping all per-file hashes correct.
func TestGetImageFile_DeltaCombinedFingerprintMismatch(t *testing.T) {
	srcRootfs := []byte("source-rootfs-for-valid-hash-test")
	newRootfs := []byte("new-rootfs-for-valid-hash-test")
	newMeta := []byte("new-metadata-for-valid-hash-test")

	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "src.squashfs")
	newPath := filepath.Join(tmpDir, "new.squashfs")
	deltaPath := filepath.Join(tmpDir, "delta.vcdiff")

	require.NoError(t, os.WriteFile(srcPath, srcRootfs, 0644))
	require.NoError(t, os.WriteFile(newPath, newRootfs, 0644))

	// Generate a correct delta from srcRootfs -> newRootfs.
	out, err := exec.Command("xdelta3", "-f", "-e", "-s", srcPath, newPath, deltaPath).CombinedOutput()
	require.NoError(t, err, "xdelta3 encode failed: %s", string(out))

	deltaContent, err := os.ReadFile(deltaPath)
	require.NoError(t, err)

	// Compute the real combined fingerprint, then advertise a bogus one.
	// The per-file hashes for newRootfs are correct so the delta will apply and
	// the per-file check will pass, but the final combined fingerprint won't match.
	realCombinedFP := computeCombinedFingerprint(newMeta, newRootfs)
	bogusCombinedFP := "aaaa" + realCombinedFP[4:]
	if bogusCombinedFP == realCombinedFP {
		bogusCombinedFP = "bbbb" + realCombinedFP[4:]
	}

	server := newTestSimpleStreamsServerWithDelta(t, newMeta, srcRootfs, newRootfs, deltaContent, bogusCombinedFP)
	defer server.Close()

	images := newTestSimpleStream(server)

	srcMetaContent := []byte("old-metadata")
	srcCombinedFP := computeCombinedFingerprint(srcMetaContent, srcRootfs)
	srcRootfsPath := filepath.Join(tmpDir, "cached-rootfs.squashfs")
	require.NoError(t, os.WriteFile(srcRootfsPath, srcRootfs, 0644))

	metaFile, err := os.CreateTemp(t.TempDir(), "meta")
	require.NoError(t, err)
	defer metaFile.Close()

	rootfsFile, err := os.CreateTemp(t.TempDir(), "rootfs")
	require.NoError(t, err)
	defer rootfsFile.Close()

	_, err = images.GetImageFile(bogusCombinedFP, ImageFileRequest{
		MetaFile:   metaFile,
		RootfsFile: rootfsFile,
		DeltaSourceRetriever: func(fingerprint string, fname string) string {
			if fingerprint == srcCombinedFP && fname == "rootfs" {
				return srcRootfsPath
			}

			return ""
		},
	})

	// The delta applied cleanly and per-file hashes matched, but the combined
	// fingerprint (SHA256 of meta || rootfs) doesn't match the advertised one.
	expectedMsg := fmt.Sprintf("Image fingerprint mismatch. Got %s expected %s", realCombinedFP, bogusCombinedFP)
	assert.EqualError(t, err, expectedMsg)
}
