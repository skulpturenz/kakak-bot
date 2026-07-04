package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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

		fmt.Printf("Creating GitHub release for %s in %s\n", tag, repo)
		err := createGitHubRelease(repo, tag, releaseBody, auth.Token)
		if err != nil {
			return fmt.Errorf("failed to create GitHub release: %w", err)
		}
		fmt.Println("Successfully created GitHub release")
	}

	return nil
}

func createGitHubRelease(repo, tag, body, token string) error {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases", repo)

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
		Draft:      false,
		Prerelease: strings.Contains(tag, "-"),
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	return nil
}
