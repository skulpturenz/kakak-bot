# Match Label Action

Locates one of a given list of labels among the labels active on the workflow
PR. This is useful for driving conditional workflow behaviour from labels, such
as selecting a release type from `release/major`, `release/minor`, and so on.

Provide exactly one of `allowed` or `allowed-multiple`:

- `allowed` matches **exactly one** of the listed labels. The action fails if no
  label or more than one matches.
- `allowed-multiple` matches **any number** of the listed labels. The action
  fails only if none match.

Both accept comma- or newline-separated names. When no label matches and
`default-match` is set, that value is returned instead of failing. The matched
label (or comma-separated labels) is exposed as the `match` output.

```yaml
- id: release-label
  uses: ./match-label
  with:
    allowed: release/major, release/minor, release/patch
    default-match: release/patch
```

By default `labels` is read from the current PR event
(`github.event.pull_request.labels`), so no explicit value is needed inside a
pull-request-triggered workflow. Pass `labels` (or the `--labels` flag when
running `kakak match-label` directly) to match against an arbitrary list.
