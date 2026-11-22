# Agent instructions

## Repository scope

This is the public home of reusable GitHub Actions workflows. Implementations
live in `.github/workflows/`; Go fixtures in `tests/` execute workflow scripts
and verify their contracts. Read the relevant `workflow_call` declaration,
implementation, tests, and [workflow requirements](docs/workflow-contracts.md)
before editing.

Governance configuration, repository administration, and the dispatch receiver
belong to `blinklabs-io/actions`. Keep private repository names, configuration,
history, credentials, and local session material out of this repository and its
public issues, pull requests, logs, and artifacts. Do not store governance
credentials here; callers supply declared workflow-specific secrets.

## Compatibility

- Treat workflow filenames, input names/types/defaults, secret names, outputs,
  job/check names, matrix coverage, and build/release gates as public contracts.
  Preserve the existing `reuseable-` filenames despite their spelling.
- Inspect callers before changing a contract. Keep reusable entry points on
  `workflow_call`; callers own events, filters, permissions, and secret mapping.
  Include maintained branches, release tags, merge queues, and reruns in impact
  assessment. Default-branch success alone does not prove compatibility.
- Use full reviewed commit SHAs for cross-repository workflow references.
  Preserve referenced commits and paths. Do not delete or rewrite published
  branches/tags to clean up a migration.
- Generated caller wrappers belong to their source configuration in actions.
  Change that source deliberately; do not hand-edit generated files as a fix.
  Keep caller migrations separate from tool upgrades and formatter enablement.
- Before retiring workflow paths or changing a host repository's visibility,
  verify access for every affected public caller, supported ref, and rerun.
  Moving an implementation does not update references in existing caller commits.

## Workflow security

- Public PR checks use `pull_request`. Preserve caller `merge_group` triggers
  where needed. Audit caller events as well as the reusable implementation.
- A `pull_request_target` exception needs a specific documented justification
  and explicit authorization. Never check out or execute untrusted PR code
  with write permissions, secrets, privileged caches, or deployment credentials.
- Declare minimal token permissions at workflow and job scope. A caller must
  grant the scopes a reusable job needs; a called workflow cannot elevate them.
  Keep test/build jobs separate from trusted publishing and deployment jobs.
- Pass untrusted refs, titles, paths, and inputs through environment variables
  or structured arguments. Quote shell expansions and validate paths, JSON,
  versions, and digests before using them. Avoid expression interpolation into
  executable shell source, `eval`, or command construction from input strings.
- Pin third-party actions to full commit SHAs with readable version comments.
  Preserve intentional toolchain policies; do not change versions merely to
  silence a failing check.
- Merge and scan images using validated digests from the same run. Preserve
  independent architecture scans and prerelease/stable alias separation.
  Propagate native-command failures immediately, including in PowerShell.
- Preserve run-owned release drafts, asset integrity checks, chart containment,
  archive digest checks, and environment-only deployment credentials. Read the
  complete producer/consumer path before changing retry or cleanup behavior.
- Do not create, edit, delete, or repair releases or published packages by hand.
  Do not push images, deploy charts, or invoke credentialed publishing as a test.
  A tag or deployment needs explicit authorization and its own verification.

## Validation

Choose checks for the changed contract. Documentation-only changes need source,
example, Markdown/link, and diff validation; do not add tests that merely repeat
documentation. Workflow or test changes require formatting, `actionlint`, and
the relevant Go checks:

```sh
test -z "$(gofmt -l tests)"
go test -race ./... -count=1
go vet ./...
go build ./...
golangci-lint run
actionlint
git diff --check
```

Fixtures require the tools their extracted scripts invoke; read the fixture
before running it. Use Apple Container for isolated security fixtures when
available, with read-only source/public dependencies, no host credentials or
network, an unprivileged user, and bounded disposable storage. Report any check
that could not run and its concrete reason.

Test workflow behavior through the actual extracted script or entry point.
Cover valid and invalid inputs, and prove regression tests fail against the old
behavior at the intended assertion. A parse/build/setup failure is not regression
evidence. Run the whole affected package after a focused test.

For Windows desktop build changes, also verify both native architectures through
[test-desktop-windows.yml](.github/workflows/test-desktop-windows.yml). The fixture runs
`go test -tags desktopintegration ./tests -run '^TestDesktopWindowsCGO$'` and
requires PowerShell, make, Go, and the matching MSYS2 C/C++ compiler. Direct runs
must set `MSYS2_AMD64_LOC` or `MSYS2_ARM64_LOC` to the matching installation,
as the CI setup does. Local cross-compilation does not establish native execution
success.

## Delivery and review

- Use a separate repository-owned worktree from the current remote default
  branch. Check open PRs, worktrees, and ownership before changing a branch.
  Preserve unrelated work and never duplicate another session's mutations.
- Link each PR to its issue. Use Conventional Commits and `git commit -s`,
  including amendments; never add `Co-authored-by` trailers. Do not rewrite
  published history or push changes directly to the default branch.
- Validate and obtain independent review before pushing. Verify human and bot
  findings against current source; fix valid findings, reply with the fixing
  commit or concise disposition, and request follow-up human review after a fix.
  Do not repeatedly request a review while one is pending.
- Merge only with human approval for the current head, green checks, reviewed
  disposition of actionable findings, and normal repository policy satisfied.
  Bot approval or silence is not human approval. Use a normal head-pinned squash
  merge, preserve the sign-off, and never bypass protections.
- Verify the merged tree and sign-off, postmerge CI, linked issue closure, and
  project status. Report evidence honestly; do not claim unavailable publishing,
  registry, deployment, or platform checks ran.

Comments explain an invariant, non-obvious algorithm, or gotcha. Delete comments
that narrate history, label sections, or restate code. Explain system behavior
in documentation; keep exported Go doc comments accurate and conventional.
