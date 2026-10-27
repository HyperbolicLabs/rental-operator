# Install, upgrade and uninstall

- [Prerequisites](#prerequisites)
- [Install](#install)
- [Namespaced install](#namespaced-install)
- [Verify a release](#verify-a-release)
- [Upgrade](#upgrade)
- [Uninstall](#uninstall)

## Prerequisites

- Kubernetes 1.35, 1.36 or 1.37, and `kubectl`. These are the versions CI tests. See [SUPPORT.md](../SUPPORT.md).
- [Helm](https://helm.sh) 3.x.
- A **Hyperbolic API key** (`sk_live_...`) from the [dashboard](https://app.hyperbolic.ai).
- An **SSH public key** if you want to log into the rented machines. Save it on the account with an [SSHKey](resources.md#sshkey), or in the dashboard. A rental gets the keys in its `sshKeyRefs`, or else the account's newest key. If the account has no key, rentals stay `Blocked` with `NoSSHKey` instead of provisioning a machine you can't reach.

## Install

From a published release (chart and image are pulled from GHCR):

```console
helm install rental-operator \
  oci://ghcr.io/hyperboliclabs/charts/rental-operator --version X.Y.Z \
  --namespace rental-operator-system --create-namespace
```

Or from a checkout of this repo:

```console
helm install rental-operator ./charts/rental-operator \
  --namespace rental-operator-system --create-namespace
```

This installs the CRDs, the controller `Deployment`, and the RBAC it needs. By default the operator watches all namespaces, and serves no metrics. See [Observability](observability.md) to turn them on. Check that it started:

```console
kubectl -n rental-operator-system rollout status deploy/rental-operator
```

## Namespaced install

By default the operator gets a `ClusterRole`, and Kubernetes RBAC can't limit Secret access by label, so it is allowed to read Secrets in every namespace (it only ever reads labeled ones). To keep it out of everything else, list the namespaces it should manage. The chart then installs a `Role` in each of them and no cluster-wide Secret access at all:

```console
helm install rental-operator ./charts/rental-operator \
  --namespace rental-operator-system --create-namespace \
  --set 'watchNamespaces={ml-team,research}'
```

GPURentals, ProviderConfigs and their Secrets in other namespaces are then ignored. ClusterProviderConfigs still work: the chart adds a narrow `ClusterRole` for them and for reading namespace labels (no Secrets), and reads their Secrets only in the operator's namespace.

## Verify a release

Every release is signed with [cosign](https://docs.sigstore.dev/) (keyless, from the release workflow's GitHub identity), and the image carries an SPDX SBOM and SLSA build provenance. To verify before installing:

```console
cosign verify ghcr.io/hyperboliclabs/rental-operator:X.Y.Z \
  --certificate-identity 'https://github.com/HyperbolicLabs/rental-operator/.github/workflows/release.yml@refs/heads/main' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
cosign verify ghcr.io/hyperboliclabs/charts/rental-operator:X.Y.Z \
  --certificate-identity 'https://github.com/HyperbolicLabs/rental-operator/.github/workflows/release.yml@refs/heads/main' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
docker buildx imagetools inspect ghcr.io/hyperboliclabs/rental-operator:X.Y.Z --format '{{ json .SBOM }}'
```

## Upgrade

Helm installs the CRDs on first install but does **not** upgrade them. Apply the new CRDs first, then upgrade the release:

```console
helm pull oci://ghcr.io/hyperboliclabs/charts/rental-operator --version X.Y.Z --untar
kubectl apply -f rental-operator/crds/
helm upgrade rental-operator oci://ghcr.io/hyperboliclabs/charts/rental-operator \
  --version X.Y.Z --namespace rental-operator-system
```

From a checkout, apply `charts/rental-operator/crds/` instead.

Read the [release notes](https://github.com/HyperbolicLabs/rental-operator/releases) first. Before 1.0 a minor release may change the API, and its notes say how to migrate. Rentals keep running during the upgrade, and the new operator picks them up from their status and annotations.

## Uninstall

Delete your objects **while the operator is still running**. Without the operator nothing terminates the rentals, so they keep billing, and nothing removes the finalizers on GPURentals, SSHKeys and configs, so they cannot be deleted.

```console
kubectl delete gpurentals --all -A --wait
kubectl delete gpuavailabilities,sshkeys,providerconfigs --all -A --wait
kubectl delete clusterproviderconfigs --all --wait
helm uninstall rental-operator --namespace rental-operator-system
```

Helm leaves the CRDs in place. Remove them once nothing is left:

```console
kubectl delete crd gpurentals.compute.hyperbolic.ai gpuavailabilities.compute.hyperbolic.ai \
  sshkeys.compute.hyperbolic.ai providerconfigs.compute.hyperbolic.ai clusterproviderconfigs.compute.hyperbolic.ai
```
