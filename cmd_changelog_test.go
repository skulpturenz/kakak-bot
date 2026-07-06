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

func TestExtractPRNumber(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    int
		ok      bool
	}{
		{"merge form", "v0.0.1-1 (#30)", 30, true},
		{"github merge form", "Merge pull request #42 from feature/x", 42, true},
		{"uses subject only", "chore: release (#7)\n\nbody mentions #999", 7, true},
		{"no reference", "chore: no pr here", 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			number, ok := extractPRNumber(tc.message)
			require.Equal(t, tc.ok, ok, "ok")
			require.Equal(t, tc.want, number, "number")
		})
	}
}

func TestEnrichChangelogContextInjectsReleaseCommitPRBody(t *testing.T) {
	// The release commit (id == commit_id) is deliberately not a merge commit,
	// proving squash/rebase-merged releases are handled.
	contextJSON := []byte(`[
		{
			"version": "v0.0.1-1",
			"commit_id": "aaa",
			"commits": [
				{"id": "ccc", "message": "feat: a thing (#27)", "merge_commit": true},
				{"id": "aaa", "message": "v0.0.1-1 (#30)", "merge_commit": false}
			]
		}
	]`)

	var fetched []int
	fetch := func(number int) (string, error) {
		fetched = append(fetched, number)
		return "## Highlights\n\n- shipped a thing", nil
	}

	enriched, err := enrichChangelogContext(contextJSON, fetch)
	require.NoError(t, err)

	var releases []map[string]any
	require.NoError(t, json.Unmarshal(enriched, &releases))
	commits := releases[0]["commits"].([]any)

	earlier := commits[0].(map[string]any)
	require.NotContains(t, earlier, "extra", "a non-release PR in the range must not be enriched")

	release := commits[1].(map[string]any)
	extra, ok := release["extra"].(map[string]any)
	require.True(t, ok, "the release commit should have extra")
	require.Equal(t, "## Highlights\n\n- shipped a thing", extra["pr_body"])

	require.Equal(t, []int{30}, fetched, "only the release commit's PR is fetched")
}

func TestEnrichChangelogContextSkipsReleaseCommitWithoutPR(t *testing.T) {
	contextJSON := []byte(`[{"commit_id": "aaa", "commits": [{"id": "aaa", "message": "direct release"}]}]`)

	var called bool
	enriched, err := enrichChangelogContext(contextJSON, func(int) (string, error) {
		called = true
		return "x", nil
	})
	require.NoError(t, err)
	require.False(t, called, "a release tip with no PR reference must not trigger a fetch")

	var releases []map[string]any
	require.NoError(t, json.Unmarshal(enriched, &releases))
	require.NotContains(t, releases[0]["commits"].([]any)[0].(map[string]any), "extra")
}

func TestEnrichChangelogContextSkipsEmptyDescription(t *testing.T) {
	contextJSON := []byte(`[{"commit_id": "aaa", "commits": [{"id": "aaa", "message": "v1 (#5)"}]}]`)

	enriched, err := enrichChangelogContext(contextJSON, func(int) (string, error) {
		return "   \n  ", nil
	})
	require.NoError(t, err)

	var releases []map[string]any
	require.NoError(t, json.Unmarshal(enriched, &releases))
	commit := releases[0]["commits"].([]any)[0].(map[string]any)
	require.NotContains(t, commit, "extra", "empty PR description must not inject extra")
}

func TestEnrichChangelogContextSkipsOnFetchError(t *testing.T) {
	contextJSON := []byte(`[{"commit_id": "aaa", "commits": [{"id": "aaa", "message": "v1 (#5)"}]}]`)

	enriched, err := enrichChangelogContext(contextJSON, func(int) (string, error) {
		return "", fmt.Errorf("boom")
	})
	require.NoError(t, err, "a fetch error must not fail the whole render")

	var releases []map[string]any
	require.NoError(t, json.Unmarshal(enriched, &releases))
	commit := releases[0]["commits"].([]any)[0].(map[string]any)
	require.NotContains(t, commit, "extra")
}

func TestPullRequestBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertGitHubRequest(t, r, http.MethodGet)
		require.Equal(t, "/repos/skulpture/kakak-bot/pulls/30", r.URL.Path)
		writeJSON(t, w, http.StatusOK, map[string]any{"body": "## Notes\n\n- did stuff"})
	}))
	defer server.Close()

	body, err := pullRequestBody(context.Background(), server.Client(), server.URL, "skulpture/kakak-bot", "token", 30)
	require.NoError(t, err)
	require.Equal(t, "## Notes\n\n- did stuff", body)
}

func TestPullRequestBodyHandlesNullBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{"body": nil})
	}))
	defer server.Close()

	body, err := pullRequestBody(context.Background(), server.Client(), server.URL, "skulpture/kakak-bot", "token", 30)
	require.NoError(t, err)
	require.Empty(t, body)
}

func TestPRBodyFetcherCaches(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeJSON(t, w, http.StatusOK, map[string]any{"body": "cached"})
	}))
	defer server.Close()

	fetch := prBodyFetcher(context.Background(), server.Client(), server.URL, "skulpture/kakak-bot", "token")

	for i := 0; i < 3; i++ {
		body, err := fetch(30)
		require.NoError(t, err)
		require.Equal(t, "cached", body)
	}
	require.Equal(t, 1, calls, "repeated fetches for the same PR should hit the cache")
}
