# Release and Helm requirements

## Flutter APK builds

The [Flutter workflow](../.github/workflows/reuseable-flutter.yml) accepts an
optional `java-version` major version for APK builds, such as `"17"`. It selects
that JDK from the runner's installed `JAVA_HOME_<major>_X64` runtime and configures
Flutter to use it before building. An invalid or unavailable runtime fails the
job. Leaving the input empty preserves Flutter's existing JDK selection;
non-APK jobs do not select Java. The workflow does not upgrade Flutter or Gradle.

## Binary releases

The [binary publisher](../.github/workflows/reuseable-publish.yml) reuses only a
draft marked for the same workflow run and binary. A retry skips an existing
asset only when its state, size, and SHA-256 match; mismatches fail for review.
These guarantees do not extend to every other publishing workflow.

Custom binary builds receive `BUILD_DATE` as the checked-out commit timestamp in
UTC. This stabilizes the embedded date only; identical retry binaries also
require a stable toolchain and other build inputs.

The [release-dispatch workflow](../.github/workflows/reuseable-release-dispatch.yml)
notifies the governance receiver in `blinklabs-io/actions`. Its `token` secret
must authorize dispatch to that repository. Calling a public workflow does not
grant access to the receiver.

## Helm charts

[Chart publication](../.github/workflows/reuseable-helm-publish.yml) accepts
checked-out source files, including tracked links to shared files inside the
repository. Git metadata, untracked files, links outside the checkout, and
symlink cycles are rejected before Helm loads chart content.

[Chart deployment](../.github/workflows/reuseable-helm-deploy.yml) requires
`chart-sha256` for the complete archive. For a local chart, use Helm 4.3.1 and
compute the digest with the same packaging settings as deployment:

```sh
# For charts with dependencies, use the committed Chart.lock.
helm dependency build ./charts/app
SOURCE_DATE_EPOCH=0 helm package ./charts/app --destination ./dist/charts
sha256sum ./dist/charts/app-1.2.3.tgz
```

The dependency build is needed only for charts with dependencies. The deployment
job does not configure Helm repositories; dependencies requiring `helm repo add`
cannot rely on repository configuration from a developer machine. Such charts
need repository setup that this workflow does not provide.

Timestamp normalization prevents checkout times from changing archive bytes. For OCI
charts, hash the archive pulled from the registry. Deployment obtains
`kubeconfig` from the selected caller environment; it rejects a caller-supplied
`kubeconfig` secret.

## Windows signing

Call the `sign-windows` composite action on a trusted Windows runner with Java
and an authenticated Google Cloud SDK. Pin the action to a reviewed commit.
Pass one workspace-relative artifact path per line in `files`, the base64 PEM
`certificate-chain`, the Google Cloud KMS `keystore` and `key-name`, and the
`application-name`. The product URL defaults to the caller's repository URL. The `store-type` input
defaults to `GOOGLECLOUD`; changing the jsign store type does not change the
authentication method.

The action accepts unsigned inputs and verifies the embedded signature it adds.
It signs in place using SHA-256 and an RFC 3161 timestamp. It fails if
Windows rejects the signature or cannot find its timestamp certificate. Sign
the executable payloads before building an installer, then invoke the same
action on the finished MSI before uploading it.

## macOS notarization

The `macos-notarize` action submits a signed `pkg` or `zip` using an existing
notarytool keychain profile and its `keychain` path. It requires an accepted notarization result. For a
package, it staples and validates the ticket, retrying transient stapler
failures. ZIP archives are submitted without stapling. The caller must upload
and attest the same submitted artifact.

The `macos-signing-setup` action imports a Developer ID Application identity
and, for package releases, a Developer ID Installer identity. It creates a
private temporary keychain and notarytool profile. A caller must run
`macos-signing-cleanup` after successful setup, including when a later build,
notarization, verification or upload step fails. Pass the setup action's
`keychain` and `original-keychain` outputs to cleanup and guard that step with
`always()` and the setup step's success outcome. The cleanup restores the
original default keychain and search list, then deletes the temporary keychain.
If restoration fails, the helper keeps recovery data in its temporary directory. Application and
installer certificate passwords and the Apple credentials are supplied by the
caller as secrets.

The `macos-package` action assembles an installer from prebuilt binaries inside
the checked-out workspace. Its JSON `config` contains a `metadata` object,
a `binaries` mapping from each bundled executable name to a workspace-relative
source path, and an `icon` path. Metadata includes `application_name`,
`bundle_id`, `executable`, `icon_name`, `version`, `architecture`, and
`binary_names`. Optional settings include `cli_symlink`, `minimum_macos`,
`description`, `organization`, and `notification_alert_style`.

The default `signed` mode requires both Developer ID identities, signs the
binaries, bundle and package, and verifies their signatures. The `unsigned`
and `adhoc` modes support development builds. Assembly publishes the completed
package without overwriting an existing output. A signed release must pass the
result through notarization and package verification before upload and
attestation. The action emits the resulting absolute path as `package`.

The `macos-package/verify` action checks the package's Developer ID Installer
signature, Gatekeeper assessment, and stapled ticket. Its default installation
check is for disposable runners. It refuses to replace a pre-existing app or
receipt, checks each installed binary's signature and architecture, and removes
the installation, its receipt and any CLI link created by the test. Set
`install` to `false` when only signature and notarization checks are desired.

The `macos-package/sign-zip` action signs and verifies a workspace-contained CLI
executable, then creates a ZIP containing the signed copy under the configured
binary's name. It leaves the input unchanged and publishes the archive without
overwriting an existing file. Submit the resulting `archive` output to
`macos-notarize` with `format: zip`, and upload and attest that same ZIP.

The `macos-package/build` action builds configured Go executables and returns a
workspace-relative output directory for the package and CLI ZIP actions. Each
binary setting supplies its name, module directory, package, CGO requirement and
build tags. Version strings default to `git describe` and normally drop a
leading `v`; `version_prefix: preserve` keeps it when the product contract
requires it. An optional web setting builds the web project in a temporary copy
or reuses an existing bundle, rejects placeholder content, and temporarily stages
it for Go embedding. The original embed directory is restored after the build,
and concurrent builds targeting it are rejected.

Completed binary directories are published atomically without overwriting an
existing output. If a process stops after publication, the complete directory
remains and a retry refuses to replace it. The caller can inspect or remove its
own completed build output before retrying.

The `macos-release` composite action composes those helpers. Its JSON `config`
selects `artifact_type` (`pkg` or `zip`), `artifact_basename`, and the shared
binary `build` settings. A package also supplies `metadata` and an `icon` path;
a ZIP supplies `cli_binary` and `signing_identifier`. The builder supplies the
resolved version, architecture and binary names to the package metadata. Product
callers own the configuration and pass Apple credentials as secrets.

Signed packages are built, notarized, stapled and verified before the action
returns their path. Signed CLI ZIPs are built, signed and notarized before their
path is returned. The action restores signing keychain state even when a later
step fails. Tag runs require signed mode. `unsigned` and `adhoc` modes are for
package development builds. Callers must upload and attest the returned artifact
only after the action succeeds.

The desktop publisher accepts the optional `macos-release-config` input. When
set, the configuration must describe a package, its `artifact_basename` must
equal `application-name`, and `build.version_prefix` must be `strip` or omitted.
The publisher rejects incompatible settings before building or signing. An empty
input retains the existing desktop packaging contract.

The reusable `reuseable-test-macos-release.yml` workflow builds a signed test
artifact only on a manual dispatch from the caller's default branch. It uploads
the verified artifact to the workflow run and does not publish a release.
Governance configuration and generated caller workflows belong in
`blinklabs-io/actions`; this repository owns the reusable implementations and
their input, output, secret, and verification contracts. Product repositories
own their source and assets. Callers provide credentials as declared secrets.

Python helpers are checked with Ruff formatting and linting, mypy, and the
Python behavior tests invoked by the Go test suite. Tool versions are pinned in
`requirements-python-checks.txt`; `pyproject.toml` defines their scope and
settings.

## Governed release orchestration

The `reuseable-release-draft.yml` stage validates release tags and only creates
or reuses a draft owned by the current workflow run. On a manual dispatch it
requires the caller's default branch and creates no release. The reusable exposes
the draft ID through `release-id` for later publishing stages; callers must pass
that ID only to jobs gated on successful preparation. The caller grants
`contents: write` to the stage. Tags use `v` followed by a three-part semantic
version, optionally with a prerelease suffix; build metadata is not accepted.
