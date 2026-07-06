package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/spf13/cobra"
)

const (
	defaultChangelogOutput = "CHANGELOG.md"
	changelogCommitName    = "github-actions[bot]"
	changelogCommitEmail   = "41898282+github-actions[bot]@users.noreply.github.com"
	defaultGitHubAPIURL    = "https://api.github.com"
	gitHubAPIVersion       = "2026-03-10"
)

//go:embed changelog/default-cliff.toml
var defaultCliffConfig string

var (
	changelogMessage    string
	changelogBranch     string
	changelogToken      string
	changelogConfig     string
	changelogOutput     string
	changelogAppID      string
	changelogPrivateKey string

	changelogCmd = &cobra.Command{
		Use:   "changelog",
		Short: "Generate and commit a changelog",
		RunE:  runChangelog,
	}
)

func init() {
	changelogCmd.Flags().StringVar(&changelogMessage, "message", "chore: update changelog", "Commit message")
	changelogCmd.Flags().StringVar(&changelogBranch, "branch", "", "Branch to commit to")
	changelogCmd.Flags().StringVar(&changelogToken, "token", os.Getenv("GITHUB_TOKEN"), "GitHub token")
	changelogCmd.Flags().StringVar(&changelogConfig, "config", "", "Path to a git-cliff configuration file")
	changelogCmd.Flags().StringVar(&changelogOutput, "output", defaultChangelogOutput, "Path to the changelog file")
	changelogCmd.Flags().StringVar(&changelogAppID, "app-id", os.Getenv("GITHUB_APP_ID"), "GitHub App id (mints an installation token when set with --private-key)")
	changelogCmd.Flags().StringVar(&changelogPrivateKey, "private-key", os.Getenv("GITHUB_APP_PRIVATE_KEY"), "GitHub App private key (PEM)")
	rootCmd.AddCommand(changelogCmd)
}

func runChangelog(cmd *cobra.Command, args []string) error {
	if changelogMessage == "" {
		return fmt.Errorf("--message is required")
	}

	if changelogBranch == "" {
		return fmt.Errorf("--branch is required")
	}

	if changelogOutput == "" {
		changelogOutput = defaultChangelogOutput
	}

	auth, err := resolveGitHubAuth(cmd.Context(), os.Getenv("GITHUB_API_URL"), os.Getenv("GITHUB_REPOSITORY"), changelogToken, changelogAppID, changelogPrivateKey)
	if err != nil {
		return err
	}

	configPath, cleanup, err := resolveChangelogConfig(changelogConfig)
	if err != nil {
		return err
	}
	defer cleanup()

	repo, err := git.PlainOpenWithOptions(".", &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return err
	}

	skipCommits, err := changelogCommitsToSkip(repo, changelogOutput, changelogMessage)
	if err != nil {
		return err
	}

	repository := os.Getenv("GITHUB_REPOSITORY")
	enrich := auth.Token != "" && repository != ""

	if enrich {
		fetch := prBodyFetcher(cmd.Context(), http.DefaultClient, os.Getenv("GITHUB_API_URL"), repository, auth.Token)

		if _, err := renderEnrichedChangelog(configPath, skipCommits, fetch, nil, []string{"--output", changelogOutput}); err != nil {
			return err
		}

		unreleasedContent, err := renderEnrichedChangelog(configPath, skipCommits, fetch, []string{"--unreleased"}, nil)
		if err != nil {
			return err
		}

		return finalizeChangelog(cmd.Context(), repo, auth, unreleasedContent)
	}

	if _, err := runGitCliff(configPath, skipCommits, "--output", changelogOutput); err != nil {
		return err
	}

	unreleasedContent, err := runGitCliff(configPath, skipCommits, "--unreleased")
	if err != nil {
		return err
	}

	return finalizeChangelog(cmd.Context(), repo, auth, unreleasedContent)
}

func finalizeChangelog(ctx context.Context, repo *git.Repository, auth resolvedAuth, unreleasedContent string) error {
	content, err := os.ReadFile(changelogOutput)
	if err != nil {
		return err
	}

	if err := writeGitHubOutput("content", string(content)); err != nil {
		return err
	}

	if err := writeGitHubOutput("unreleased-content", unreleasedContent); err != nil {
		return err
	}

	if auth.Token != "" {
		commitSHA, err := commitSignedChangelog(ctx, signedChangelogOptions{
			Token:      auth.Token,
			Repository: os.Getenv("GITHUB_REPOSITORY"),
			APIURL:     os.Getenv("GITHUB_API_URL"),
			Branch:     changelogBranch,
			OutputPath: changelogOutput,
			Message:    changelogMessage,
			Content:    content,
		})
		if err != nil {
			return err
		}

		if commitSHA == "" {
			fmt.Println("No remote changelog changes to commit.")
		}
		return writeGitHubOutput("commit-sha", commitSHA)
	}

	changed, err := changelogFileChanged(repo, changelogOutput)
	if err != nil {
		return err
	}

	if !changed {
		if err := writeGitHubOutput("commit-sha", ""); err != nil {
			return err
		}
		fmt.Println("No changelog changes to commit.")
		return nil
	}

	commitHash, err := commitChangelog(repo, changelogOutput, changelogMessage, auth.Identity)
	if err != nil {
		return err
	}

	if err := pushChangelog(repo, changelogBranch, auth.Token, commitHash); err != nil {
		return err
	}

	return writeGitHubOutput("commit-sha", commitHash.String())
}

type signedChangelogOptions struct {
	Token      string
	Repository string
	APIURL     string
	Branch     string
	OutputPath string
	Message    string
	Content    []byte
	HTTPClient *http.Client
}

type githubContentFile struct {
	SHA     string
	Content []byte
}

type githubContentResponse struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	SHA      string `json:"sha"`
	Content  string `json:"content"`
}

type githubContentUpdateRequest struct {
	Message string `json:"message"`
	Content string `json:"content"`
	Branch  string `json:"branch"`
	SHA     string `json:"sha,omitempty"`
}

type githubContentUpdateResponse struct {
	Commit struct {
		SHA          string `json:"sha"`
		Verification struct {
			Verified bool   `json:"verified"`
			Reason   string `json:"reason"`
		} `json:"verification"`
	} `json:"commit"`
}

func commitSignedChangelog(ctx context.Context, opts signedChangelogOptions) (string, error) {
	if opts.Token == "" {
		return "", fmt.Errorf("--token is required for signed changelog commits")
	}
	if opts.Repository == "" {
		return "", fmt.Errorf("GITHUB_REPOSITORY is required for signed changelog commits")
	}
	if opts.APIURL == "" {
		opts.APIURL = defaultGitHubAPIURL
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}

	existing, err := getGitHubContent(ctx, opts)
	if err != nil {
		return "", err
	}

	if existing != nil && bytes.Equal(existing.Content, opts.Content) {
		return "", nil
	}

	commitSHA, err := putGitHubContent(ctx, opts, existing)
	if err != nil {
		return "", err
	}
	return commitSHA, nil
}

func getGitHubContent(ctx context.Context, opts signedChangelogOptions) (*githubContentFile, error) {
	endpoint, err := githubContentsURL(opts.APIURL, opts.Repository, opts.OutputPath)
	if err != nil {
		return nil, err
	}

	query := endpoint.Query()
	query.Set("ref", opts.Branch)
	endpoint.RawQuery = query.Encode()

	req, err := newGitHubRequest(ctx, http.MethodGet, endpoint.String(), opts.Token, nil)
	if err != nil {
		return nil, err
	}

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, githubResponseError("get changelog content", resp)
	}

	var content githubContentResponse
	if err := json.NewDecoder(resp.Body).Decode(&content); err != nil {
		return nil, err
	}
	if content.Type != "file" {
		return nil, fmt.Errorf("remote changelog path is not a file: %s", opts.OutputPath)
	}
	if content.Encoding != "base64" {
		return nil, fmt.Errorf("remote changelog content uses unsupported encoding: %s", content.Encoding)
	}

	decoded, err := base64.StdEncoding.DecodeString(compactBase64(content.Content))
	if err != nil {
		return nil, err
	}

	return &githubContentFile{SHA: content.SHA, Content: decoded}, nil
}

func putGitHubContent(ctx context.Context, opts signedChangelogOptions, existing *githubContentFile) (string, error) {
	endpoint, err := githubContentsURL(opts.APIURL, opts.Repository, opts.OutputPath)
	if err != nil {
		return "", err
	}

	payload := githubContentUpdateRequest{
		Message: opts.Message,
		Content: base64.StdEncoding.EncodeToString(opts.Content),
		Branch:  opts.Branch,
	}
	if existing != nil {
		payload.SHA = existing.SHA
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := newGitHubRequest(ctx, http.MethodPut, endpoint.String(), opts.Token, bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", githubResponseError("commit changelog content", resp)
	}

	var result githubContentUpdateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Commit.SHA == "" {
		return "", fmt.Errorf("GitHub did not return a commit SHA")
	}

	if !result.Commit.Verification.Verified {
		reason := result.Commit.Verification.Reason
		if reason == "" {
			reason = "missing verification"
		}
		return "", fmt.Errorf("GitHub created an unsigned changelog commit: %s", reason)
	}

	return result.Commit.SHA, nil
}

func newGitHubRequest(ctx context.Context, method, endpoint, token string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", gitHubAPIVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func githubContentsURL(apiURL, repository, outputPath string) (*url.URL, error) {
	owner, repo, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || repo == "" {
		return nil, fmt.Errorf("GITHUB_REPOSITORY must be in owner/repo format")
	}

	base, err := url.Parse(apiURL)
	if err != nil {
		return nil, err
	}

	base.Path = strings.TrimRight(base.Path, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/contents/" + escapeGitHubContentPath(outputPath)
	return base, nil
}

func escapeGitHubContentPath(outputPath string) string {
	normalized := strings.TrimPrefix(normalizeGitPath(outputPath), "/")
	parts := strings.Split(normalized, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func compactBase64(content string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t', ' ':
			return -1
		default:
			return r
		}
	}, content)
}

func githubResponseError(action string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Message != "" {
		return fmt.Errorf("GitHub API failed to %s: %s (%s)", action, payload.Message, resp.Status)
	}

	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("GitHub API failed to %s: %s", action, msg)
}

func resolveChangelogConfig(configPath string) (string, func(), error) {
	if configPath != "" {
		if _, err := os.Stat(configPath); err != nil {
			return "", func() {}, fmt.Errorf("git-cliff config not found: %s", configPath)
		}
		return configPath, func() {}, nil
	}

	f, err := os.CreateTemp("", "kakak-cliff-*.toml")
	if err != nil {
		return "", func() {}, err
	}

	cleanup := func() {
		_ = os.Remove(f.Name())
	}

	if _, err := f.WriteString(defaultCliffConfig); err != nil {
		_ = f.Close()
		cleanup()
		return "", func() {}, err
	}

	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}

	return f.Name(), cleanup, nil
}

func runGitCliff(configPath string, skipCommits []string, args ...string) (string, error) {
	cmdArgs := []string{"--config", configPath}
	for _, commit := range skipCommits {
		cmdArgs = append(cmdArgs, "--skip-commit", commit)
	}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command("git-cliff", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			return "", err
		}
		return "", fmt.Errorf("%w: %s", err, msg)
	}

	if stderr.Len() > 0 {
		_, _ = os.Stderr.Write(stderr.Bytes())
	}

	return stdout.String(), nil
}

// renderEnrichedChangelog renders the changelog in three steps: dump the
// processed git-cliff context, inject merged PR descriptions into each merge
// commit's `extra` field, then render from that enriched context. filterArgs are
// applied to the context pass (e.g. --unreleased); outputArgs to the render pass
// (e.g. --output CHANGELOG.md). It returns the render pass' stdout.
func renderEnrichedChangelog(configPath string, skipCommits []string, fetch func(int) (string, error), filterArgs, outputArgs []string) (string, error) {
	contextArgs := append(append([]string{}, filterArgs...), "--context")
	contextJSON, err := runGitCliff(configPath, skipCommits, contextArgs...)
	if err != nil {
		return "", err
	}

	enriched, err := enrichChangelogContext([]byte(contextJSON), fetch)
	if err != nil {
		return "", err
	}

	f, err := os.CreateTemp("", "kakak-context-*.json")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())

	if _, err := f.Write(enriched); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}

	renderArgs := append([]string{"--from-context", f.Name()}, outputArgs...)
	return runGitCliff(configPath, nil, renderArgs...)
}

// enrichChangelogContext sets commit.extra.pr_body to the merged PR's
// description (fetched via fetch) for the single commit that the release was cut
// at — the one whose id matches the release's commit_id. That is the
// release/promotion PR; the descriptions of other PRs merged into the range are
// intentionally left out. The release commit need not be a merge commit, so
// squash- and rebase-merged releases are handled too. Releases whose tip has no
// PR reference, an empty description, or a fetch error are left untouched.
func enrichChangelogContext(contextJSON []byte, fetch func(int) (string, error)) ([]byte, error) {
	var releases []map[string]any
	if err := json.Unmarshal(contextJSON, &releases); err != nil {
		return nil, fmt.Errorf("failed to parse git-cliff context: %w", err)
	}

	for _, release := range releases {
		commitID, _ := release["commit_id"].(string)
		if commitID == "" {
			continue
		}

		commits, ok := release["commits"].([]any)
		if !ok {
			continue
		}

		for _, raw := range commits {
			commit, ok := raw.(map[string]any)
			if !ok {
				continue
			}

			if id, _ := commit["id"].(string); id != commitID {
				continue
			}

			message, _ := commit["message"].(string)
			number, ok := extractPRNumber(message)
			if !ok {
				break
			}

			body, err := fetch(number)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to fetch PR #%d description: %v\n", number, err)
				break
			}
			if strings.TrimSpace(body) != "" {
				commit["extra"] = map[string]any{"pr_body": body}
			}
			break
		}
	}

	return json.Marshal(releases)
}

var prNumberPattern = regexp.MustCompile(`#(\d+)`)

// extractPRNumber pulls the pull request number from a commit subject, matching
// both the merge-commit form ("... (#30)") and GitHub's "Merge pull request #30
// from ..." form.
func extractPRNumber(message string) (int, bool) {
	subject := strings.SplitN(message, "\n", 2)[0]
	match := prNumberPattern.FindStringSubmatch(subject)
	if match == nil {
		return 0, false
	}

	number, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return number, true
}

// prBodyFetcher returns a closure that fetches (and caches) pull request
// descriptions from GitHub.
func prBodyFetcher(ctx context.Context, client *http.Client, apiURL, repository, token string) func(int) (string, error) {
	cache := make(map[int]string)
	return func(number int) (string, error) {
		if body, ok := cache[number]; ok {
			return body, nil
		}

		body, err := pullRequestBody(ctx, client, apiURL, repository, token, number)
		if err != nil {
			return "", err
		}

		cache[number] = body
		return body, nil
	}
}

// pullRequestBody fetches the description of a single pull request.
func pullRequestBody(ctx context.Context, client *http.Client, apiURL, repository, token string, number int) (string, error) {
	if apiURL == "" {
		apiURL = defaultGitHubAPIURL
	}
	if client == nil {
		client = http.DefaultClient
	}

	owner, repo, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || repo == "" {
		return "", fmt.Errorf("GITHUB_REPOSITORY must be in owner/repo format")
	}

	endpoint := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", strings.TrimRight(apiURL, "/"), url.PathEscape(owner), url.PathEscape(repo), number)
	req, err := newGitHubRequest(ctx, http.MethodGet, endpoint, token, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", githubResponseError(fmt.Sprintf("fetch pull request #%d", number), resp)
	}

	var payload struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	return payload.Body, nil
}

func changelogFileChanged(repo *git.Repository, outputPath string) (bool, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return false, err
	}

	status, err := wt.Status()
	if err != nil {
		return false, err
	}

	fileStatus, ok := status[normalizeGitPath(outputPath)]
	if !ok {
		return false, nil
	}

	return fileStatus.Worktree != git.Unmodified, nil
}

func changelogCommitsToSkip(repo *git.Repository, outputPath, message string) ([]string, error) {
	normalizedOutput := normalizeGitPath(outputPath)
	iter, err := repo.Log(&git.LogOptions{FileName: &normalizedOutput})
	if err != nil {
		if err == plumbing.ErrObjectNotFound {
			return nil, nil
		}
		return nil, err
	}
	defer iter.Close()

	var commits []string
	err = iter.ForEach(func(commit *object.Commit) error {
		subject := strings.SplitN(commit.Message, "\n", 2)[0]
		if subject == message {
			commits = append(commits, commit.Hash.String())
		}
		return nil
	})
	return commits, err
}

func commitChangelog(repo *git.Repository, outputPath, message string, identity gitHubIdentity) (plumbing.Hash, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return plumbing.Hash{}, err
	}

	if _, err := wt.Add(normalizeGitPath(outputPath)); err != nil {
		return plumbing.Hash{}, err
	}

	return wt.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  identity.Name,
			Email: identity.Email,
			When:  time.Now(),
		},
	})
}

func pushChangelog(repo *git.Repository, branch, token string, commitHash plumbing.Hash) error {
	localRef := plumbing.NewBranchReferenceName(branch)
	if err := repo.Storer.SetReference(plumbing.NewHashReference(localRef, commitHash)); err != nil {
		return err
	}

	pushOpts := &git.PushOptions{
		RemoteName: "origin",
		RefSpecs: []config.RefSpec{
			config.RefSpec(fmt.Sprintf("%s:refs/heads/%s", localRef, branch)),
		},
	}

	if os.Getenv("GITHUB_REPOSITORY") != "" {
		if token == "" {
			return fmt.Errorf("--token is required when GITHUB_REPOSITORY is set")
		}
		pushOpts.Auth = &httpgit.BasicAuth{
			Username: "x-access-token",
			Password: token,
		}
	}

	err := repo.Push(pushOpts)
	if err == git.NoErrAlreadyUpToDate {
		return nil
	}
	return err
}

func writeGitHubOutput(name, value string) error {
	githubOutput := os.Getenv("GITHUB_OUTPUT")
	if githubOutput == "" {
		return nil
	}

	f, err := os.OpenFile(githubOutput, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	delimiter := fmt.Sprintf("kakak_output_%d", time.Now().UnixNano())
	_, err = fmt.Fprintf(f, "%s<<%s\n%s\n%s\n", name, delimiter, value, delimiter)
	return err
}

func normalizeGitPath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}
