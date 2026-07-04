# Release Action

Validates a SemVer version, pushes the matching git tag, and creates a GitHub
release for it through the REST API. The version is resolved from the `version`
input, falling back to the tag in `GITHUB_REF`, and finally to the latest `v`
tag in the repository. A version containing a `-` (e.g. `v1.2.3-rc.0`) is
published as a pre-release.

```yaml
- uses: ./release
  with:
    version: ${{ steps.bump.outputs.next-tag }}
    body: ${{ steps.changelog.outputs.unreleased-content }}
    token: ${{ secrets.GITHUB_TOKEN }}
```

The workflow token must be allowed to push tags and create releases:

```yaml
permissions:
  contents: write
```

Instead of `token`, you can authenticate as a GitHub App by providing `app-id`
and `private-key`. When both are set, the action mints an installation access
token, tags and releases as the app, and attributes the tag to the app's bot
identity. This is useful when releases should be attributed to a dedicated app
rather than `github-actions[bot]`.
