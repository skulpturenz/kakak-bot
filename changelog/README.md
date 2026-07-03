# Changelog Action

Generates a changelog with `git-cliff` and commits the result to the target
branch.

When `token` is set, the action commits through the GitHub REST API and requires
GitHub to return a verified signed commit. Workflows using the default
`github.token` must grant write access to repository contents:

```yaml
permissions:
  contents: write
```

Without a token, the action falls back to local git commit and push behavior,
which is mainly useful for local integration tests.
