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
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestResolveChangelogConfigUsesProvidedPath(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "cliff.toml")
	if err := os.WriteFile(configPath, []byte("[changelog]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, cleanup, err := resolveChangelogConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if got != configPath {
		t.Fatalf("resolveChangelogConfig() = %q; want %q", got, configPath)
	}
}

func TestResolveChangelogConfigWritesEmbeddedDefault(t *testing.T) {
	got, cleanup, err := resolveChangelogConfig("")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	content, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(content), "# Changelog") {
		t.Fatalf("default config does not contain changelog header: %s", content)
	}
}

func TestWriteGitHubOutput(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "github-output.txt")
	t.Setenv("GITHUB_OUTPUT", outputPath)

	if err := writeGitHubOutput("content", "line one\nline two"); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}

	got := string(content)
	if !strings.Contains(got, "content<<kakak_output_") {
		t.Fatalf("missing multiline output header: %q", got)
	}
	if !strings.Contains(got, "line one\nline two\n") {
		t.Fatalf("missing multiline output content: %q", got)
	}
}

func TestChangelogFileChanged(t *testing.T) {
	dir := t.TempDir()
	repo, wt := initTestRepo(t, dir)

	writeTestFile(t, dir, "CHANGELOG.md", "initial\n")
	if _, err := wt.Add("CHANGELOG.md"); err != nil {
		t.Fatal(err)
	}
	commitTestRepo(t, wt, "chore: initial changelog")

	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}

	changed, err := changelogFileChanged(repo, "CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		wt, err := repo.Worktree()
		if err != nil {
			t.Fatal(err)
		}
		status, err := wt.Status()
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("expected unchanged committed changelog, status: %s", status)
	}

	writeTestFile(t, dir, "CHANGELOG.md", "updated\n")
	changed, err = changelogFileChanged(repo, "CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected modified changelog")
	}
}

func TestChangelogCommitsToSkip(t *testing.T) {
	dir := t.TempDir()
	repo, wt := initTestRepo(t, dir)

	writeTestFile(t, dir, "CHANGELOG.md", "initial\n")
	if _, err := wt.Add("CHANGELOG.md"); err != nil {
		t.Fatal(err)
	}
	commitTestRepo(t, wt, "chore: update changelog")

	writeTestFile(t, dir, "other.txt", "feature\n")
	if _, err := wt.Add("other.txt"); err != nil {
		t.Fatal(err)
	}
	commitTestRepo(t, wt, "feat: add feature")

	commits, err := changelogCommitsToSkip(repo, "CHANGELOG.md", "chore: update changelog")
	if err != nil {
		t.Fatal(err)
	}

	if len(commits) != 1 {
		t.Fatalf("len(changelogCommitsToSkip()) = %d; want 1", len(commits))
	}
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
	if err == nil {
		t.Fatal("expected missing GITHUB_REPOSITORY error")
	}
	if !strings.Contains(err.Error(), "GITHUB_REPOSITORY") {
		t.Fatalf("error = %q; want GITHUB_REPOSITORY", err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if commitSHA != "" {
		t.Fatalf("commit SHA = %q; want empty no-op SHA", commitSHA)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if commitSHA != "created-sha" {
		t.Fatalf("commit SHA = %q; want created-sha", commitSHA)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if commitSHA != "updated-sha" {
		t.Fatalf("commit SHA = %q; want updated-sha", commitSHA)
	}
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
	if err == nil {
		t.Fatal("expected unsigned commit error")
	}
	if !strings.Contains(err.Error(), "unsigned") {
		t.Fatalf("error = %q; want unsigned reason", err)
	}
}

func initTestRepo(t *testing.T, dir string) (*git.Repository, *git.Worktree) {
	t.Helper()

	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}

	return repo, wt
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
}

func assertGitHubRequest(t *testing.T, r *http.Request, method string) {
	t.Helper()

	if r.Method != method {
		t.Fatalf("method = %s; want %s", r.Method, method)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer token" {
		t.Fatalf("Authorization header = %q; want bearer token", got)
	}
	if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
		t.Fatalf("Accept header = %q; want application/vnd.github+json", got)
	}
	if got := r.Header.Get("X-GitHub-Api-Version"); got != gitHubAPIVersion {
		t.Fatalf("X-GitHub-Api-Version header = %q; want %s", got, gitHubAPIVersion)
	}
	if method == http.MethodPut && r.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type header = %q; want application/json", r.Header.Get("Content-Type"))
	}
}

func assertGitHubContentPath(t *testing.T, r *http.Request, path, branch string) {
	t.Helper()

	if r.URL.Path != path {
		t.Fatalf("path = %q; want %q", r.URL.Path, path)
	}
	if r.Method == http.MethodGet && r.URL.Query().Get("ref") != branch {
		t.Fatalf("ref query = %q; want %q", r.URL.Query().Get("ref"), branch)
	}
}

func assertGitHubContentPayload(t *testing.T, payload githubContentUpdateRequest, message, branch, sha string, content []byte) {
	t.Helper()

	if payload.Message != message {
		t.Fatalf("message = %q; want %q", payload.Message, message)
	}
	if payload.Branch != branch {
		t.Fatalf("branch = %q; want %q", payload.Branch, branch)
	}
	if payload.SHA != sha {
		t.Fatalf("sha = %q; want %q", payload.SHA, sha)
	}
	decoded, err := base64.StdEncoding.DecodeString(payload.Content)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(content) {
		t.Fatalf("content = %q; want %q", decoded, content)
	}
}

func decodeJSON(t *testing.T, r *http.Request, target any) {
	t.Helper()

	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, payload any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatal(err)
	}
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
