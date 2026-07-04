# Bump Action

Determines the next SemVer release from the current one. The `from` input is
either a bump label or an explicit version:

- A label — `major`, `minor`, `patch`, `prerelease`, `premajor`, `preminor`, or
  `prepatch` — bumps the latest matching git tag. Supply `preid` to make the
  bump a pre-release (e.g. `alpha`), and `label-prefix` if labels are namespaced
  (e.g. `release/minor` with `label-prefix: release/`).
- An explicit version such as `v1.2.3` or `1.2.3` is validated and used as-is.

The base version comes from the latest tag matching `release-prefix` (default
`v`); when no tags exist it starts from `0.0.0`. The action outputs
`next-version` (`major.minor.patch[-preid.N]`) and `next-tag` (with the release
prefix applied).

```yaml
- id: bump
  uses: ./bump
  with:
    from: ${{ steps.release-label.outputs.match }}
    label-prefix: release/
```

This action is read-only and needs no token, but it must be able to see the
repository's tags, so check out with full history:

```yaml
- uses: actions/checkout@v7
  with:
    fetch-depth: 0
```
