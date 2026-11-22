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
