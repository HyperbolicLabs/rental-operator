# rental-operator documentation

| Page | What it covers |
|---|---|
| [Architecture](architecture.md) | Components, the GPURental lifecycle, how creates and deletes stay safe, polling and API budget, credentials. |
| [Install, upgrade and uninstall](install.md) | Prerequisites, cluster-wide and namespaced installs, verifying a release, CRD upgrades, removing the operator safely. |
| [Custom resources](resources.md) | Every field of ProviderConfig, ClusterProviderConfig, GPURental, GPUAvailability and SSHKey, with their conditions and reasons. |
| [Common operations](operations.md) | Regions, ports, terminating, orphaning, rebooting. |
| [Observability](observability.md) | Metrics, alerts and how to scrape them. |
| [API reference](reference/api.md) | Every field of every kind, generated from the Go types (`make api-docs`). |
| [API versioning](api-versioning.md) | What each API version promises, how the API graduates, and how to upgrade across versions. |
| [Development](development.md) | Running the operator locally. |

Also at the repository root: [SUPPORT.md](../SUPPORT.md), [RELEASE.md](../RELEASE.md), [ROADMAP.md](../ROADMAP.md), [CONTRIBUTING.md](../CONTRIBUTING.md), [GOVERNANCE.md](../GOVERNANCE.md), [MAINTAINERS.md](../MAINTAINERS.md), [ADOPTERS.md](../ADOPTERS.md) and [SECURITY.md](../SECURITY.md).
