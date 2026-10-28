# Support

## Getting help

- **Questions and bugs:** open a GitHub issue. Include the operator, chart and Kubernetes versions, the resource YAML and the operator logs, with API keys removed.
- **Security issues:** report them privately, as [SECURITY.md](SECURITY.md) describes.
- **Your Hyperbolic account** (balance, charges, a rental you cannot reach or terminate): contact Hyperbolic from the [dashboard](https://app.hyperbolic.ai). This repository cannot see your account.

## Supported releases

rental-operator follows [semantic versioning](https://semver.org/).

Before 1.0, only the latest minor release is supported. Fixes, including security fixes, ship as patch releases of that minor. A new minor release may change the API, and its [release notes](https://github.com/HyperbolicLabs/rental-operator/releases) list the migration steps. The policy for 1.0 and later will be published before 1.0.

| Release | Released | Status | Supported until |
|---|---|---|---|
| 0.1 | 2026-10-28 | Supported | The release of 0.2 |

## Compatibility

Each release supports the three newest Kubernetes minor versions at the time it ships. Every one of them runs the full end-to-end suite in CI, on the `kindest/node` image listed below, installing both the kustomize manifests and the Helm chart.

| rental-operator | Kubernetes | Tested on | Helm | hyperbolic-go |
|---|---|---|---|---|
| 0.1 | 1.35, 1.36, 1.37 | v1.35.8, v1.36.4, v1.37.0 (kind v0.33.0) | 3.x (CI uses 3.22) | 0.1 |

Other versions may work, but they are not tested, and problems that only happen on them are fixed on a best-effort basis.

### Updating the matrix

When a Kubernetes minor version is released or reaches end of life upstream:

1. Bump `KIND_VERSION` and `KIND_SHA256` in [`test-e2e.yml`](.github/workflows/test-e2e.yml) to a kind release that supports it.
2. Replace the matrix entries with the node images from that kind release's notes, pinned by digest, keeping the three newest minor versions.
3. Update the table above and the prerequisites in [docs/install.md](docs/install.md).
