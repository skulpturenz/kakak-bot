package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bradleyfalzon/ghinstallation/v2"
)

// gitHubIdentity is the author/committer used for git commits and tags.
type gitHubIdentity struct {
	Name  string
	Email string
}

// resolvedAuth holds the token and commit identity to use for a command.
type resolvedAuth struct {
	Token    string
	Identity gitHubIdentity
}

func defaultGitHubIdentity() gitHubIdentity {
	return gitHubIdentity{Name: changelogCommitName, Email: changelogCommitEmail}
}

// resolveGitHubAuth decides how to authenticate. When both appID and
// privateKeyPEM are provided, it mints a GitHub App installation access token
// and derives the app's bot identity. Otherwise it returns the supplied token
// with the default github-actions[bot] identity.
func resolveGitHubAuth(ctx context.Context, apiURL, repository, token, appID, privateKeyPEM string) (resolvedAuth, error) {
	appID = strings.TrimSpace(appID)
	privateKeyPEM = strings.TrimSpace(privateKeyPEM)

	if appID == "" || privateKeyPEM == "" {
		return resolvedAuth{Token: token, Identity: defaultGitHubIdentity()}, nil
	}

	if apiURL == "" {
		apiURL = defaultGitHubAPIURL
	}
	apiURL = strings.TrimRight(apiURL, "/")

	id, err := strconv.ParseInt(appID, 10, 64)
	if err != nil {
		return resolvedAuth{}, fmt.Errorf("invalid app id %q: %w", appID, err)
	}

	appsTransport, err := ghinstallation.NewAppsTransport(http.DefaultTransport, id, []byte(privateKeyPEM))
	if err != nil {
		return resolvedAuth{}, fmt.Errorf("failed to build GitHub App transport: %w", err)
	}
	appsTransport.BaseURL = apiURL

	installationID, err := appInstallationID(ctx, apiURL, repository, appsTransport)
	if err != nil {
		return resolvedAuth{}, err
	}

	installationTransport := ghinstallation.NewFromAppsTransport(appsTransport, installationID)
	installationTransport.BaseURL = apiURL

	installationToken, err := installationTransport.Token(ctx)
	if err != nil {
		return resolvedAuth{}, fmt.Errorf("failed to mint installation token: %w", err)
	}

	identity, err := appBotIdentity(ctx, apiURL, appsTransport, installationToken)
	if err != nil {
		return resolvedAuth{}, err
	}

	return resolvedAuth{Token: installationToken, Identity: identity}, nil
}

// appInstallationID looks up the installation of the app on the given repository.
func appInstallationID(ctx context.Context, apiURL, repository string, appsTransport *ghinstallation.AppsTransport) (int64, error) {
	owner, repo, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || repo == "" {
		return 0, fmt.Errorf("GITHUB_REPOSITORY must be in owner/repo format")
	}

	endpoint := fmt.Sprintf("%s/repos/%s/%s/installation", apiURL, url.PathEscape(owner), url.PathEscape(repo))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", gitHubAPIVersion)

	resp, err := (&http.Client{Transport: appsTransport}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, githubResponseError("look up app installation", resp)
	}

	var payload struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, err
	}
	if payload.ID == 0 {
		return 0, fmt.Errorf("GitHub did not return an installation id for %s", repository)
	}
	return payload.ID, nil
}

// appBotIdentity derives the <slug>[bot] identity for the authenticated app.
func appBotIdentity(ctx context.Context, apiURL string, appsTransport *ghinstallation.AppsTransport, installationToken string) (gitHubIdentity, error) {
	appReq, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/app", nil)
	if err != nil {
		return gitHubIdentity{}, err
	}
	appReq.Header.Set("Accept", "application/vnd.github+json")
	appReq.Header.Set("X-GitHub-Api-Version", gitHubAPIVersion)

	appResp, err := (&http.Client{Transport: appsTransport}).Do(appReq)
	if err != nil {
		return gitHubIdentity{}, err
	}
	defer appResp.Body.Close()
	if appResp.StatusCode != http.StatusOK {
		return gitHubIdentity{}, githubResponseError("look up GitHub App", appResp)
	}

	var app struct {
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(appResp.Body).Decode(&app); err != nil {
		return gitHubIdentity{}, err
	}
	if app.Slug == "" {
		return gitHubIdentity{}, fmt.Errorf("GitHub did not return an app slug")
	}

	login := app.Slug + "[bot]"

	userReq, err := newGitHubRequest(ctx, http.MethodGet, apiURL+"/users/"+url.PathEscape(login), installationToken, nil)
	if err != nil {
		return gitHubIdentity{}, err
	}
	userResp, err := http.DefaultClient.Do(userReq)
	if err != nil {
		return gitHubIdentity{}, err
	}
	defer userResp.Body.Close()
	if userResp.StatusCode != http.StatusOK {
		return gitHubIdentity{}, githubResponseError("look up app bot user", userResp)
	}

	var user struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&user); err != nil {
		return gitHubIdentity{}, err
	}
	if user.ID == 0 {
		return gitHubIdentity{}, fmt.Errorf("GitHub did not return a user id for %s", login)
	}

	return gitHubIdentity{
		Name:  login,
		Email: fmt.Sprintf("%d+%s@users.noreply.github.com", user.ID, login),
	}, nil
}
