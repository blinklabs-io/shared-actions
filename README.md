# Shared Actions

Reusable GitHub Actions workflows for Blink Labs repositories.

Call workflows from `.github/workflows` using an immutable commit SHA. The
caller supplies workflow inputs, token permissions, and required secrets.
This repository contains reusable implementations and their validation.

Run `go test -race ./...`, `go vet ./...`, and `actionlint` to validate changes.
The Windows desktop compiler fixture runs in the Windows CI matrix.

The release-dispatch workflow sends notifications to the governance receiver
in `blinklabs-io/actions`. Moving the reusable implementation here does not move
that receiver. Callers must supply a token authorized to dispatch to that target.

Release retries reuse only a draft marked for the same workflow run and binary.
An existing asset must match its state, size, and SHA-256 before a retry skips
uploading it; mismatched assets require review. Custom binary builds receive
`BUILD_DATE` as the checked-out commit timestamp in UTC for reproducible retries.

Chart publication accepts checked-out source files, including tracked links to
shared files inside the repository. Git metadata, untracked files, links outside
the checkout, and symlink cycles are rejected before Helm loads chart content.
