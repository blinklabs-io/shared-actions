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
notarytool keychain profile. It requires an accepted notarization result. For a
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
original default keychain and deletes the temporary keychain. Application and
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
