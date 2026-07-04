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

## Attaching binaries

Pass the `assets` input a newline- or comma-separated list of files or globs to
attach binaries to the release in a single step:

```yaml
- uses: ./release
  with:
    version: ${{ steps.bump.outputs.next-tag }}
    body: ${{ steps.changelog.outputs.unreleased-content }}
    token: ${{ secrets.GITHUB_TOKEN }}
    assets: |
      dist/*
      checksums.txt
```

When `assets` is set, the release is created as a draft, every asset is uploaded,
and only then is the release published. Consumers therefore never see a release
without its binaries, and the release is compatible with GitHub immutable
releases (which lock assets at publish time). A listed path or glob that matches
no files fails the action. When `assets` is omitted, the release is published
directly as before.

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
