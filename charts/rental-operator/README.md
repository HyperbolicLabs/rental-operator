# Rental Operator

Rent Hyperbolic on-demand GPUs by applying Kubernetes objects. You declare a `GPURental`, the operator rents the GPUs through the Hyperbolic API with your API key, and the SSH details appear on its status. Deleting the object terminates the rental.

The Rental Operator is in beta. Its API is `compute.hyperbolic.ai/v1alpha1`, and [API versioning](https://github.com/HyperbolicLabs/rental-operator/blob/main/docs/api-versioning.md) explains what that promises.

## Install

You need Kubernetes 1.35 to 1.37, Helm 3, and a Hyperbolic API key.

```console
helm install rental-operator oci://ghcr.io/hyperboliclabs/charts/rental-operator \
  --version X.Y.Z --namespace rental-operator-system --create-namespace
```

Helm installs the CRDs on the first install only. [Install, upgrade and uninstall](https://github.com/HyperbolicLabs/rental-operator/blob/main/docs/install.md) covers upgrading them, verifying a release's signatures, and the namespaced install.

## Rent a GPU

1. Store your API key in a Secret labeled `compute.hyperbolic.ai/credentials=true`. The operator reads no other Secrets.
2. Create a `ProviderConfig` that points at the Secret.
3. Save your SSH public key as an `SSHKey`, and create a `GPURental` that lists it in `sshKeyRefs`.

The [quickstart](https://github.com/HyperbolicLabs/rental-operator#quickstart) walks through each step.

## Values

| Value | Default | Description |
|---|---|---|
| `image.repository` | `ghcr.io/hyperboliclabs/rental-operator` | Operator image. |
| `image.tag` | the chart's `appVersion` | Image tag. |
| `watchNamespaces` | `[]` | Namespaces to watch. Empty watches every namespace with a ClusterRole. A list installs a Role in each namespace instead, with no cluster-wide Secret access. |
| `leaderElection.enabled` | `true` | Leader election, so more than one replica is safe. |
| `replicaCount` | `1` | Replicas. |
| `resources` | 50m and 64Mi requested, 500m and 256Mi limits | Container resources. |
| `metrics.enabled` | `false` | Serve Prometheus metrics through a Service. |
| `metrics.serviceMonitor.enabled` | `false` | Create a ServiceMonitor. |
| `metrics.prometheusRule.enabled` | `false` | Create a PrometheusRule with the operator's alerts. |

[values.yaml](https://github.com/HyperbolicLabs/rental-operator/blob/main/charts/rental-operator/values.yaml) documents every value, and [Observability](https://github.com/HyperbolicLabs/rental-operator/blob/main/docs/observability.md) lists the metrics and alerts.

## Before you uninstall

Delete your GPURentals first. Without the operator, nothing terminates the rentals, and they keep billing until you terminate them in the [Hyperbolic dashboard](https://app.hyperbolic.ai).

## Help

Ask questions in [GitHub Discussions](https://github.com/HyperbolicLabs/rental-operator/discussions) and report bugs in [issues](https://github.com/HyperbolicLabs/rental-operator/issues). Report vulnerabilities privately, as [SECURITY.md](https://github.com/HyperbolicLabs/rental-operator/blob/main/SECURITY.md) describes.
