# Shared Actions

Public reusable GitHub Actions workflows for Go CI, Docker and desktop releases,
Flutter, Helm, and dependency updates.

## Use a workflow

Call a workflow at the job level and pin it to a reviewed full commit SHA:

```yaml
name: CI
on: [push, pull_request]
permissions:
  contents: read
jobs:
  test:
    uses: blinklabs-io/shared-actions/.github/workflows/reuseable-go-test.yml@41843cdf39c98dbace0bf6821535c1e074657b67
    with:
      run-build: true
      run-vet: true
```

Browse [workflow definitions](.github/workflows) for inputs, defaults, secrets,
and permissions. The caller controls triggers and supplies required permissions
and secrets. Filenames retain the `reuseable-` spelling for compatibility.
See [release and Helm requirements](docs/workflow-contracts.md) before publishing
or deploying.

## Contribute

Read [AGENTS.md](AGENTS.md) for compatibility, security, review, and validation
rules. For workflow changes, run `go test -race ./...`, `go vet ./...`, and
`actionlint`; Windows compiler fixtures also run in CI.
