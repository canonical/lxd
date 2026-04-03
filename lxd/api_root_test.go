package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUIGet tests the uiGet handler.
func TestUIGet(t *testing.T) {
	t.Run("Unavailable", func(t *testing.T) {
		t.Setenv("LXD_UI", "")

		req := httptest.NewRequest(http.MethodGet, "/ui/", nil)
		w := httptest.NewRecorder()

		// Simulate the Content-Type pre-set by createCmd, to guard against a regression
		// where the file server would inherit this header and serve all UI files as application/json.
		w.Header().Set("Content-Type", "application/json")

		resp := uiGet(nil, req)
		require.NoError(t, resp.Render(w, req))

		result := w.Result()
		assert.Equal(t, http.StatusServiceUnavailable, result.StatusCode)
		assert.Contains(t, result.Header.Get("Content-Type"), "text/html")
		assert.NotContains(t, result.Header.Get("Content-Type"), "application/json")
	})

	t.Run("ServesFiles", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "app.html"), []byte("<html><body>UI</body></html>"), 0o644))
		t.Setenv("LXD_UI", dir)

		req := httptest.NewRequest(http.MethodGet, "/ui/app.html", nil)
		w := httptest.NewRecorder()

		// Simulate the Content-Type pre-set by createCmd, to guard against a regression
		// where the file server would inherit this header and serve all UI files as application/json.
		w.Header().Set("Content-Type", "application/json")

		resp := uiGet(nil, req)
		require.NoError(t, resp.Render(w, req))

		result := w.Result()
		assert.Equal(t, http.StatusOK, result.StatusCode)
		assert.Contains(t, result.Header.Get("Content-Type"), "text/html")
		assert.NotContains(t, result.Header.Get("Content-Type"), "application/json")
	})
}

// TestDocumentationGet tests the documentationGet handler.
func TestDocumentationGet(t *testing.T) {
	t.Run("Unavailable", func(t *testing.T) {
		t.Setenv("LXD_DOCUMENTATION", "")

		req := httptest.NewRequest(http.MethodGet, "/documentation/", nil)
		w := httptest.NewRecorder()

		// Simulate the Content-Type pre-set by createCmd.
		w.Header().Set("Content-Type", "application/json")

		resp := documentationGet(nil, req)
		require.NoError(t, resp.Render(w, req))

		result := w.Result()
		assert.Equal(t, http.StatusNotFound, result.StatusCode)
	})

	t.Run("ServesFiles", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "guide.html"), []byte("<html><body>Docs</body></html>"), 0o644))
		t.Setenv("LXD_DOCUMENTATION", dir)

		req := httptest.NewRequest(http.MethodGet, "/documentation/guide.html", nil)
		w := httptest.NewRecorder()

		// Simulate the Content-Type pre-set by createCmd, to guard against a regression
		// where the file server would inherit this header and serve all documentation files as application/json.
		w.Header().Set("Content-Type", "application/json")

		resp := documentationGet(nil, req)
		require.NoError(t, resp.Render(w, req))

		result := w.Result()
		assert.Equal(t, http.StatusOK, result.StatusCode)
		assert.Contains(t, result.Header.Get("Content-Type"), "text/html")
		assert.NotContains(t, result.Header.Get("Content-Type"), "application/json")
	})
}

// TestUIRedirectGet tests the uiRedirectGet handler.
func TestUIRedirectGet(t *testing.T) {
	t.Run("Unavailable", func(t *testing.T) {
		t.Setenv("LXD_UI", "")

		req := httptest.NewRequest(http.MethodGet, "/ui", nil)
		w := httptest.NewRecorder()

		resp := uiRedirectGet(nil, req)
		require.NoError(t, resp.Render(w, req))

		result := w.Result()
		assert.Equal(t, http.StatusServiceUnavailable, result.StatusCode)
		assert.Contains(t, result.Header.Get("Content-Type"), "text/html")
	})

	t.Run("Redirects", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("LXD_UI", dir)

		req := httptest.NewRequest(http.MethodGet, "/ui", nil)
		w := httptest.NewRecorder()

		resp := uiRedirectGet(nil, req)
		require.NoError(t, resp.Render(w, req))

		result := w.Result()
		assert.Equal(t, http.StatusMovedPermanently, result.StatusCode)
		assert.Equal(t, "/ui/", result.Header.Get("Location"))
	})
}

// TestDocumentationRedirectGet tests the documentationRedirectGet handler.
func TestDocumentationRedirectGet(t *testing.T) {
	t.Run("Unavailable", func(t *testing.T) {
		t.Setenv("LXD_DOCUMENTATION", "")

		req := httptest.NewRequest(http.MethodGet, "/documentation", nil)
		w := httptest.NewRecorder()

		resp := documentationRedirectGet(nil, req)
		require.NoError(t, resp.Render(w, req))

		result := w.Result()
		assert.Equal(t, http.StatusNotFound, result.StatusCode)
	})

	t.Run("Redirects", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("LXD_DOCUMENTATION", dir)

		req := httptest.NewRequest(http.MethodGet, "/documentation", nil)
		w := httptest.NewRecorder()

		resp := documentationRedirectGet(nil, req)
		require.NoError(t, resp.Render(w, req))

		result := w.Result()
		assert.Equal(t, http.StatusMovedPermanently, result.StatusCode)
		assert.Equal(t, "/documentation/", result.Header.Get("Location"))
	})
}
