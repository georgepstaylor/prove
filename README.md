# prove

A GitHub App that approves a pull request without a human reviewer when every file it touches meets the rules!

> Some PRs prove on their own - no knead for a human reviewer. 🍞

Rules live in each repo's `.github/prove.yml`. The App submits the review itself, so it counts toward branch-protection required reviews — low-risk PRs can merge on their own while everything else still waits for a person.

## Configuration

Config is a list of rules. Each says: a PR confined to these `paths` may be self-approved by anyone in `allow`. Paths are globs (a trailing `/` means the whole subtree); `allow` entries are `@user` or `@org/team` handles.

```yaml
# .github/prove.yml
mode: enforce # enforce (act) | dry_run (comment only, take no action)

rules:
  - paths: ["docs/", "*.md"]
    allow: ["@org/writers", "@alice"]
  - paths: ["playground/bob/"]
    allow: ["@bob"]

protect:
  dot_github: true # always require a human for .github/
  config_file: true # ...and for this file, so rules can't self-rewrite
```

A PR is approved only if every changed path is covered by a rule allowing its author, and nothing it touches is guarded. One uncovered or guarded file blocks the whole PR.

A rule can also carry a `semver: [patch, minor]` or `sha: true` constraint to permit dependency bumps or pinned-SHA updates (e.g. from Dependabot or Renovate) — see the comments in [`.github/prove.yml`](.github/prove.yml) for the full set of options.

## Notes

- **Start in `dry_run`.** prove comments the decision it would make without acting, so you can tune rules before it approves anything.
- **Safe by default.** Config is read from the base branch (never the PR head), unlisted paths always need review, and any error during evaluation results in no approval.

## Status

Work in progress - actual docs to come soon

## License

See [LICENSE](LICENSE).
