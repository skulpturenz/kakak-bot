package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/require"
)

func TestResolveChangelogConfigUsesProvidedPath(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "cliff.toml")
	require.NoError(t, os.WriteFile(configPath, []byte("[changelog]\n"), 0644))

	got, cleanup, err := resolveChangelogConfig(configPath)
	require.NoError(t, err)
	defer cleanup()

	require.Equal(t, configPath, got, "resolveChangelogConfig()")
}

func TestResolveChangelogConfigWritesEmbeddedDefault(t *testing.T) {
	got, cleanup, err := resolveChangelogConfig("")
	require.NoError(t, err)
	defer cleanup()

	content, err := os.ReadFile(got)
	require.NoError(t, err)

	require.Contains(t, string(content), "# Changelog", "default config does not contain changelog header")
}

func TestWriteGitHubOutput(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "github-output.txt")
	t.Setenv("GITHUB_OUTPUT", outputPath)

	require.NoError(t, writeGitHubOutput("content", "line one\nline two"))

	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)

	got := string(content)
	require.Contains(t, got, "content<<kakak_output_", "missing multiline output header")
	require.Contains(t, got, "line one\nline two\n", "missing multiline output content")
}

func TestChangelogFileChanged(t *testing.T) {
	dir := t.TempDir()
	repo, wt := initTestRepo(t, dir)

	writeTestFile(t, dir, "CHANGELOG.md", "initial\n")
	_, err := wt.Add("CHANGELOG.md")
	require.NoError(t, err)
	commitTestRepo(t, wt, "chore: initial changelog")

	repo, err = git.PlainOpen(dir)
	require.NoError(t, err)

	changed, err := changelogFileChanged(repo, "CHANGELOG.md")
	require.NoError(t, err)
	require.False(t, changed, "expected unchanged committed changelog")

	writeTestFile(t, dir, "CHANGELOG.md", "updated\n")
	changed, err = changelogFileChanged(repo, "CHANGELOG.md")
	require.NoError(t, err)
	require.True(t, changed, "expected modified changelog")
}

func TestChangelogCommitsToSkip(t *testing.T) {
	dir := t.TempDir()
	repo, wt := initTestRepo(t, dir)

	writeTestFile(t, dir, "CHANGELOG.md", "initial\n")
	_, err := wt.Add("CHANGELOG.md")
	require.NoError(t, err)
	commitTestRepo(t, wt, "chore: update changelog")

	writeTestFile(t, dir, "other.txt", "feature\n")
	_, err = wt.Add("other.txt")
	require.NoError(t, err)
	commitTestRepo(t, wt, "feat: add feature")

	commits, err := changelogCommitsToSkip(repo, "CHANGELOG.md", "chore: update changelog")
	require.NoError(t, err)

	require.Len(t, commits, 1)
}

func TestCommitSignedChangelogRequiresGitHubRepository(t *testing.T) {
	_, err := commitSignedChangelog(context.Background(), signedChangelogOptions{
		Token:      "token",
		APIURL:     "https://api.github.test",
		Branch:     "main",
		OutputPath: "CHANGELOG.md",
		Message:    "chore: update changelog",
		Content:    []byte("content\n"),
	})
	require.Error(t, err, "expected missing GITHUB_REPOSITORY error")
	require.ErrorContains(t, err, "GITHUB_REPOSITORY")
}

func TestCommitSignedChangelogNoopWhenRemoteContentMatches(t *testing.T) {
	changelogContent := []byte("existing changelog\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertGitHubRequest(t, r, http.MethodGet)
		assertGitHubContentPath(t, r, "/repos/skulpture/kakak-bot/contents/CHANGELOG.md", "main")
		writeJSON(t, w, http.StatusOK, githubContentResponse{
			Type:     "file",
			Encoding: "base64",
			SHA:      "old-sha",
			Content:  base64.StdEncoding.EncodeToString(changelogContent),
		})
	}))
	defer server.Close()

	commitSHA, err := commitSignedChangelog(context.Background(), signedChangelogOptions{
		Token:      "token",
		Repository: "skulpture/kakak-bot",
		APIURL:     server.URL,
		Branch:     "main",
		OutputPath: "CHANGELOG.md",
		Message:    "chore: update changelog",
		Content:    changelogContent,
	})
	require.NoError(t, err)
	require.Empty(t, commitSHA, "commit SHA; want empty no-op SHA")
}

func TestCommitSignedChangelogCreatesMissingRemoteFile(t *testing.T) {
	changelogContent := []byte("new changelog\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertGitHubContentPath(t, r, "/repos/skulpture/kakak-bot/contents/docs/CHANGELOG.md", "main")
		switch r.Method {
		case http.MethodGet:
			assertGitHubRequest(t, r, http.MethodGet)
			writeJSON(t, w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		case http.MethodPut:
			assertGitHubRequest(t, r, http.MethodPut)
			var payload githubContentUpdateRequest
			decodeJSON(t, r, &payload)
			assertGitHubContentPayload(t, payload, "chore: update changelog", "main", "", changelogContent)
			writeGitHubCommitResponse(t, w, http.StatusCreated, "created-sha", true, "valid")
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	commitSHA, err := commitSignedChangelog(context.Background(), signedChangelogOptions{
		Token:      "token",
		Repository: "skulpture/kakak-bot",
		APIURL:     server.URL,
		Branch:     "main",
		OutputPath: "docs/CHANGELOG.md",
		Message:    "chore: update changelog",
		Content:    changelogContent,
	})
	require.NoError(t, err)
	require.Equal(t, "created-sha", commitSHA, "commit SHA")
}

func TestCommitSignedChangelogUpdatesRemoteFile(t *testing.T) {
	changelogContent := []byte("updated changelog\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertGitHubContentPath(t, r, "/repos/skulpture/kakak-bot/contents/CHANGELOG.md", "main")
		switch r.Method {
		case http.MethodGet:
			assertGitHubRequest(t, r, http.MethodGet)
			writeJSON(t, w, http.StatusOK, githubContentResponse{
				Type:     "file",
				Encoding: "base64",
				SHA:      "old-sha",
				Content:  base64.StdEncoding.EncodeToString([]byte("old changelog\n")),
			})
		case http.MethodPut:
			assertGitHubRequest(t, r, http.MethodPut)
			var payload githubContentUpdateRequest
			decodeJSON(t, r, &payload)
			assertGitHubContentPayload(t, payload, "chore: update changelog", "main", "old-sha", changelogContent)
			writeGitHubCommitResponse(t, w, http.StatusOK, "updated-sha", true, "valid")
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	commitSHA, err := commitSignedChangelog(context.Background(), signedChangelogOptions{
		Token:      "token",
		Repository: "skulpture/kakak-bot",
		APIURL:     server.URL,
		Branch:     "main",
		OutputPath: "CHANGELOG.md",
		Message:    "chore: update changelog",
		Content:    changelogContent,
	})
	require.NoError(t, err)
	require.Equal(t, "updated-sha", commitSHA, "commit SHA")
}

func TestCommitSignedChangelogFailsUnverifiedCommit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(t, w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		case http.MethodPut:
			writeGitHubCommitResponse(t, w, http.StatusCreated, "unsigned-sha", false, "unsigned")
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	_, err := commitSignedChangelog(context.Background(), signedChangelogOptions{
		Token:      "token",
		Repository: "skulpture/kakak-bot",
		APIURL:     server.URL,
		Branch:     "main",
		OutputPath: "CHANGELOG.md",
		Message:    "chore: update changelog",
		Content:    []byte("content\n"),
	})
	require.Error(t, err, "expected unsigned commit error")
	require.ErrorContains(t, err, "unsigned")
}

func initTestRepo(t *testing.T, dir string) (*git.Repository, *git.Worktree) {
	t.Helper()

	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)

	wt, err := repo.Worktree()
	require.NoError(t, err)

	return repo, wt
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
}

func commitTestRepo(t *testing.T, wt *git.Worktree, message string) {
	t.Helper()

	_, err := wt.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test User",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	require.NoError(t, err)
}

func assertGitHubRequest(t *testing.T, r *http.Request, method string) {
	t.Helper()

	require.Equal(t, method, r.Method)
	require.Equal(t, "Bearer token", r.Header.Get("Authorization"), "Authorization header")
	require.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"), "Accept header")
	require.Equal(t, gitHubAPIVersion, r.Header.Get("X-GitHub-Api-Version"), "X-GitHub-Api-Version header")
	if method == http.MethodPut {
		require.Equal(t, "application/json", r.Header.Get("Content-Type"), "Content-Type header")
	}
}

func assertGitHubContentPath(t *testing.T, r *http.Request, path, branch string) {
	t.Helper()

	require.Equal(t, path, r.URL.Path, "path")
	if r.Method == http.MethodGet {
		require.Equal(t, branch, r.URL.Query().Get("ref"), "ref query")
	}
}

func assertGitHubContentPayload(t *testing.T, payload githubContentUpdateRequest, message, branch, sha string, content []byte) {
	t.Helper()

	require.Equal(t, message, payload.Message, "message")
	require.Equal(t, branch, payload.Branch, "branch")
	require.Equal(t, sha, payload.SHA, "sha")
	decoded, err := base64.StdEncoding.DecodeString(payload.Content)
	require.NoError(t, err)
	require.Equal(t, string(content), string(decoded), "content")
}

func decodeJSON(t *testing.T, r *http.Request, target any) {
	t.Helper()

	require.NoError(t, json.NewDecoder(r.Body).Decode(target))
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, payload any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	require.NoError(t, json.NewEncoder(w).Encode(payload))
}

func writeGitHubCommitResponse(t *testing.T, w http.ResponseWriter, status int, sha string, verified bool, reason string) {
	t.Helper()

	writeJSON(t, w, status, map[string]any{
		"commit": map[string]any{
			"sha": sha,
			"verification": map[string]any{
				"verified": verified,
				"reason":   reason,
			},
		},
	})
}

func Example_escapeGitHubContentPath() {
	fmt.Println(escapeGitHubContentPath("docs/release notes/CHANGELOG.md"))
	// Output: docs/release%20notes/CHANGELOG.md
}
