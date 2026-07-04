package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateVersion(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"1.0.0", true},
		{"v1.2.3", true},
		{"1.2.3-alpha.0", true},
		{"invalid", false},
		{"1.0", true},
		{"v1.0", true},
	}

	for _, tc := range tests {
		_, err := semver.NewVersion(tc.input)
		assert.Equal(t, tc.valid, err == nil, "ValidateVersion(%s) valid", tc.input)
	}
}

func TestResolveAssetFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.bin", "two.bin", "notes.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600))
	}

	t.Run("empty input", func(t *testing.T) {
		files, err := resolveAssetFiles("   \n  ")
		require.NoError(t, err)
		assert.Empty(t, files)
	})

	t.Run("globs and explicit paths, comma and newline separated, de-duplicated", func(t *testing.T) {
		input := fmt.Sprintf("%s\n%s , %s",
			filepath.Join(dir, "*.bin"),
			filepath.Join(dir, "notes.txt"),
			filepath.Join(dir, "one.bin"), // already matched by the glob
		)
		files, err := resolveAssetFiles(input)
		require.NoError(t, err)

		sort.Strings(files)
		assert.Equal(t, []string{
			filepath.Join(dir, "notes.txt"),
			filepath.Join(dir, "one.bin"),
			filepath.Join(dir, "two.bin"),
		}, files)
	})

	t.Run("no match fails", func(t *testing.T) {
		_, err := resolveAssetFiles(filepath.Join(dir, "missing-*.zip"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "matched no files")
	})
}

func TestCreateGitHubReleaseWithoutAssets(t *testing.T) {
	var created map[string]any
	patched := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/owner/repo/releases":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&created))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 1, "upload_url": ""}`))
		case r.Method == http.MethodPatch:
			patched = true
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	err := createGitHubRelease(context.Background(), server.URL, "owner/repo", "v1.0.0", "notes", "tok", nil)
	require.NoError(t, err)

	assert.Equal(t, false, created["draft"], "release should be published directly when no assets")
	assert.Equal(t, false, created["prerelease"])
	assert.False(t, patched, "no publish PATCH expected without assets")
}

func TestCreateGitHubReleaseWithAssets(t *testing.T) {
	dir := t.TempDir()
	assetA := filepath.Join(dir, "kakak-linux-amd64")
	assetB := filepath.Join(dir, "kakak-darwin-arm64")
	require.NoError(t, os.WriteFile(assetA, []byte("binary-a"), 0o600))
	require.NoError(t, os.WriteFile(assetB, []byte("binary-b"), 0o600))

	var (
		created     map[string]any
		uploaded    = map[string]string{}
		publishBody map[string]any
	)

	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/owner/repo/releases":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&created))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"id": 42, "upload_url": "%s/uploads/repos/owner/repo/releases/42/assets{?name,label}"}`, serverURL)
		case r.Method == http.MethodPost && r.URL.Path == "/uploads/repos/owner/repo/releases/42/assets":
			assert.Equal(t, "application/octet-stream", r.Header.Get("Content-Type"))
			body, _ := io.ReadAll(r.Body)
			uploaded[r.URL.Query().Get("name")] = string(body)
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/owner/repo/releases/42":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&publishBody))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	err := createGitHubRelease(context.Background(), server.URL, "owner/repo", "v1.2.3", "notes", "tok", []string{assetA, assetB})
	require.NoError(t, err)

	assert.Equal(t, true, created["draft"], "release should be created as a draft when assets are present")
	assert.Equal(t, map[string]string{
		"kakak-linux-amd64":  "binary-a",
		"kakak-darwin-arm64": "binary-b",
	}, uploaded)
	require.NotNil(t, publishBody, "draft release should be published after uploads")
	assert.Equal(t, false, publishBody["draft"])
}
