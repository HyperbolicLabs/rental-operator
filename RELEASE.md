# Releases

Which releases are supported, and on which Kubernetes versions, is in [SUPPORT.md](SUPPORT.md).

## Versioning

The operator image, the Helm chart and the [`hyperbolic-go`](https://github.com/HyperbolicLabs/hyperbolic-go) SDK follow [semantic versioning](https://semver.org/). A release `vX.Y.Z` sets the image tag, the chart `version` and the chart `appVersion` to `X.Y.Z`, so one number identifies all three.

| Release | Contains | Upgrade |
|---|---|---|
| Patch, `0.1.z` | Bug and security fixes. No CRD schema changes. | Upgrade the chart. Rolling back is safe. |
| Minor, `0.y.0` | Features. Before 1.0, also breaking API changes, each called out in the release notes with migration steps. | Apply the new CRDs, then upgrade the chart, as [docs/install.md](docs/install.md#upgrade) describes. |
| Major, `x.0.0` | Breaking changes after 1.0. | As the release notes describe. |
| Pre-release, `X.Y.Z-rc.N` | A candidate for testing. Never tagged `latest`. | Test clusters only. |

The version follows from the pull requests merged since the last release. Their titles are Conventional Commits. A breaking change, marked with `!`, makes the next release a minor before 1.0 and a major after. A feature makes it a minor, and anything else a patch. CI fails on a pull request that changes a CRD incompatibly unless it is labeled `breaking-api-change`, so a breaking change cannot slip into a patch.

How the custom resources' API versions evolve is a separate policy, described in [API versioning](docs/api-versioning.md).

## What a release contains

- The image `ghcr.io/hyperboliclabs/rental-operator:X.Y.Z` for `linux/amd64` and `linux/arm64`, signed with cosign, with an SPDX SBOM and SLSA build provenance attached.
- The chart `oci://ghcr.io/hyperboliclabs/charts/rental-operator` version `X.Y.Z`, signed with cosign.
- A GitHub Release whose notes list the pull requests merged since the previous release, breaking changes first, with the packaged chart attached.

[Verify a release](docs/install.md#verify-a-release) shows how to check the signatures before installing.

## Cadence

Patch releases ship as fixes land. A security fix ships as soon as it is ready, as [SECURITY.md](SECURITY.md) describes. Minor releases ship when their features are done, with no fixed schedule before 1.0.

## Cutting a release

1. If the operator needs unreleased `hyperbolic-go` changes, release `hyperbolic-go` first and move the operator's `go.mod` to that version in a pull request.
2. In a pull request, update the release row and the tested Kubernetes versions in [SUPPORT.md](SUPPORT.md).
3. Run the **Release** workflow on `main` from Actions. Leave the version empty to release the suggested one, or enter a pre-release of it, such as `v0.2.0-rc.1`, or a larger version. `make release-version` previews the suggestion, and `make release-version RELEASE=v1.0.0` checks a version of your own.
4. An owner approves the run in the `release` environment.
5. When it finishes, verify the signatures and install the published chart on a kind cluster, as [docs/development.md](docs/development.md#run-it-on-kind) describes, using the GHCR chart instead of the checkout.

## How the release workflow works

[`release.yml`](.github/workflows/release.yml) runs only when started by hand. It:

1. checks that lint, tests, verification and the end-to-end tests passed on the commit
2. chooses the version, and refuses one that is already released or smaller than the suggestion
3. waits for an owner's approval
4. builds and pushes the multi-arch image, tagged `X.Y.Z`, and `latest` unless it is a pre-release, with BuildKit attaching the SBOM and provenance
5. packages the chart with `version` and `appVersion` set to the release, so it pulls the matching image, and pushes it to GHCR
6. signs the image and the chart with cosign, keyless, using the workflow's GitHub identity, and publishes the chart's Artifact Hub metadata
7. creates the tag and the GitHub Release with generated notes and the packaged chart

Nothing is tagged until everything before it succeeded. Actions are pinned to commit SHAs. The committed `Chart.yaml` version is a placeholder set at package time, so a release changes no file.

One-time setup: make the GHCR packages public, or give consumers a pull secret, and set the `ARTIFACTHUB_REPOSITORY_ID` Actions variable to the chart's Artifact Hub repository ID so Artifact Hub lists it under a verified publisher.
