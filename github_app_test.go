package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err, "generate key")
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	return string(pem.EncodeToMemory(block))
}

func TestResolveGitHubAuthWithoutApp(t *testing.T) {
	auth, err := resolveGitHubAuth(context.Background(), "", "owner/repo", "ghp_token", "", "")
	require.NoError(t, err)
	assert.Equal(t, "ghp_token", auth.Token)
	assert.Equal(t, changelogCommitName, auth.Identity.Name, "Identity.Name; want default github-actions[bot]")
	assert.Equal(t, changelogCommitEmail, auth.Identity.Email, "Identity.Email; want default github-actions[bot]")
}

func TestResolveGitHubAuthWithApp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/installation":
			_, _ = w.Write([]byte(`{"id": 12345}`))
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/12345/access_tokens":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token": "ghs_minted", "expires_at": "2999-01-01T00:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/app":
			_, _ = w.Write([]byte(`{"slug": "my-app"}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/users/"):
			_, _ = w.Write([]byte(`{"id": 999}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	auth, err := resolveGitHubAuth(context.Background(), server.URL, "owner/repo", "ghp_token", "42", testPrivateKeyPEM(t))
	require.NoError(t, err)

	assert.Equal(t, "ghs_minted", auth.Token)
	assert.Equal(t, "my-app[bot]", auth.Identity.Name)
	assert.Equal(t, "999+my-app[bot]@users.noreply.github.com", auth.Identity.Email)
}
