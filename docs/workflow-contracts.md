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
