package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/spf13/cobra"
)

var (
	releaseVersion    string
	releaseBody       string
	releaseAssets     string
	githubToken       string
	releaseAppID      string
	releasePrivateKey string

	releaseCmd = &cobra.Command{
		Use:   "release",
		Short: "Validate version and push git tag",
		RunE:  runRelease,
	}
)

func init() {
	releaseCmd.Flags().StringVar(&releaseVersion, "version", "", "Version to release")
	releaseCmd.Flags().StringVar(&releaseBody, "body", "", "Release body")
	releaseCmd.Flags().StringVar(&releaseAssets, "assets", os.Getenv("GITHUB_RELEASE_ASSETS"), "Newline/comma-separated files or globs to attach to the release")
	releaseCmd.Flags().StringVar(&githubToken, "token", os.Getenv("GITHUB_TOKEN"), "GitHub token")
	releaseCmd.Flags().StringVar(&releaseAppID, "app-id", os.Getenv("GITHUB_APP_ID"), "GitHub App id (mints an installation token when set with --private-key)")
	releaseCmd.Flags().StringVar(&releasePrivateKey, "private-key", os.Getenv("GITHUB_APP_PRIVATE_KEY"), "GitHub App private key (PEM)")
	rootCmd.AddCommand(releaseCmd)
}

func runRelease(cmd *cobra.Command, args []string) error {
	version := releaseVersion
	if version == "" {
		ref := os.Getenv("GITHUB_REF")
		if strings.HasPrefix(ref, "refs/tags/") {
			version = strings.TrimPrefix(ref, "refs/tags/")
		} else {
			latest, err := getLatestTagGoGit("v")
			if err != nil {
				return fmt.Errorf("version is required and no tags found")
			}
			version = latest
		}
	}

	v, err := semver.NewVersion(version)
	if err != nil {
		return fmt.Errorf("invalid semver version: %s", version)
	}

	fmt.Printf("Validating version: %s\n", v.String())

	assets, err := resolveAssetFiles(releaseAssets)
	if err != nil {
		return err
	}

	auth, err := resolveGitHubAuth(cmd.Context(), os.Getenv("GITHUB_API_URL"), os.Getenv("GITHUB_REPOSITORY"), githubToken, releaseAppID, releasePrivateKey)
	if err != nil {
		return err
	}

	tag := version
	fmt.Printf("Pushing tag: %s\n", tag)

	r, err := git.PlainOpen(".")
	if err != nil {
		return err
	}

	head, err := r.Head()
	if err != nil {
		return err
	}

	_, err = r.CreateTag(tag, head.Hash(), &git.CreateTagOptions{
		Tagger: &object.Signature{
			Name:  auth.Identity.Name,
			Email: auth.Identity.Email,
			When:  time.Now(),
		},
		Message: tag,
	})
	if err != nil && err != git.ErrTagExists {
		return fmt.Errorf("failed to create tag locally: %w", err)
	}

	pushOpts := &git.PushOptions{
		RemoteName: "origin",
		RefSpecs: []config.RefSpec{
			config.RefSpec(fmt.Sprintf("refs/tags/%s:refs/tags/%s", tag, tag)),
		},
	}

	if auth.Token != "" {
		pushOpts.Auth = &httpgit.BasicAuth{
			Username: "x-access-token",
			Password: auth.Token,
		}
	}

	err = r.Push(pushOpts)
	if err != nil {
		fmt.Printf("Warning: go-git push failed: %v.\n", err)
	}

	fmt.Printf("Successfully pushed tag %s\n", tag)

	if auth.Token != "" {
		repo := os.Getenv("GITHUB_REPOSITORY")
		if repo == "" {
			fmt.Println("Warning: GITHUB_REPOSITORY not set, skipping GitHub release creation")
			return nil
		}

		apiURL := os.Getenv("GITHUB_API_URL")
		if apiURL == "" {
			apiURL = defaultGitHubAPIURL
		}
		apiURL = strings.TrimRight(apiURL, "/")

		fmt.Printf("Creating GitHub release for %s in %s\n", tag, repo)
		err := createGitHubRelease(cmd.Context(), apiURL, repo, tag, releaseBody, auth.Token, assets)
		if err != nil {
			return fmt.Errorf("failed to create GitHub release: %w", err)
		}
		fmt.Println("Successfully created GitHub release")
	}

	return nil
}

// resolveAssetFiles expands a newline/comma-separated list of paths or globs
// into an ordered, de-duplicated list of files. An entry that matches no files
// is a hard error so a mistyped path never ships a release without its binaries.
func resolveAssetFiles(input string) ([]string, error) {
	if strings.TrimSpace(input) == "" {
		return nil, nil
	}

	entries := strings.FieldsFunc(input, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ','
	})

	var files []string
	seen := make(map[string]bool)
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		matches, err := filepath.Glob(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid asset pattern %q: %w", entry, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("asset %q matched no files", entry)
		}

		for _, m := range matches {
			if seen[m] {
				continue
			}
			seen[m] = true
			files = append(files, m)
		}
	}

	return files, nil
}

// createGitHubRelease creates the GitHub release and, when assets are provided,
// does so atomically: the release is created as a draft, every asset is
// uploaded, and only then is the release published. This guarantees consumers
// never observe a release without its binaries and is compatible with GitHub
// immutable releases, which lock assets at publish time.
func createGitHubRelease(ctx context.Context, apiURL, repo, tag, body, token string, assets []string) error {
	draft := len(assets) > 0

	reqBody := struct {
		TagName    string `json:"tag_name"`
		Name       string `json:"name"`
		Body       string `json:"body"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}{
		TagName:    tag,
		Name:       tag,
		Body:       body,
		Draft:      draft,
		Prerelease: strings.Contains(tag, "-"),
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("%s/repos/%s/releases", apiURL, repo)
	req, err := newGitHubRequest(ctx, http.MethodPost, endpoint, token, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return githubResponseError("create release", resp)
	}

	var release struct {
		ID        int64  `json:"id"`
		UploadURL string `json:"upload_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("failed to decode release response: %w", err)
	}

	if !draft {
		return nil
	}

	for _, asset := range assets {
		if err := uploadReleaseAsset(ctx, release.UploadURL, asset, token); err != nil {
			return err
		}
	}

	return publishRelease(ctx, apiURL, repo, release.ID, token)
}

// uploadReleaseAsset uploads a single file to the release's upload_url. The
// upload_url is a URI template (".../assets{?name,label}"); the template suffix
// is stripped and the file name is supplied via the name query parameter.
func uploadReleaseAsset(ctx context.Context, uploadURL, path, token string) error {
	if i := strings.Index(uploadURL, "{"); i != -1 {
		uploadURL = uploadURL[:i]
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	name := filepath.Base(path)
	endpoint := uploadURL + "?name=" + url.QueryEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, f)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", gitHubAPIVersion)
	req.Header.Set("Content-Type", "application/octet-stream")

	fmt.Printf("Uploading asset %s\n", name)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return githubResponseError(fmt.Sprintf("upload asset %s", name), resp)
	}

	return nil
}

// publishRelease flips a draft release to published once all assets are attached.
func publishRelease(ctx context.Context, apiURL, repo string, id int64, token string) error {
	payload, err := json.Marshal(struct {
		Draft bool `json:"draft"`
	}{Draft: false})
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("%s/repos/%s/releases/%d", apiURL, repo, id)
	req, err := newGitHubRequest(ctx, http.MethodPatch, endpoint, token, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return githubResponseError("publish release", resp)
	}

	return nil
}
