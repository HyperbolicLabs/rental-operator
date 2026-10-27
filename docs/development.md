# Development

Build, test and contribution rules are in [CONTRIBUTING.md](../CONTRIBUTING.md). This page covers running the operator locally.

- [Run it on kind](#run-it-on-kind)
- [Run it out of cluster](#run-it-out-of-cluster)
- [Releasing](#releasing)

## Run it on kind

```console
# 1. Create a cluster
kind create cluster

# 2. Build the image and load it into kind
make docker-build IMG=rental-operator:dev
kind load docker-image rental-operator:dev

# 3. Install the chart pointing at the loaded image
helm install rental-operator ./charts/rental-operator \
  --namespace rental-operator-system --create-namespace \
  --set image.repository=rental-operator --set image.tag=dev \
  --set image.pullPolicy=IfNotPresent

# 4. Add your key, a ProviderConfig and a GPURental (see the Quickstart)
```

Anything you create on kind rents real GPUs with your key. Delete your GPURentals before `kind delete cluster`.

## Run it out of cluster

Run the controller against your current kubeconfig:

```console
make install     # apply the CRDs
make run         # run the controller locally
```

## Releasing

Versioning, what a release contains and the maintainer checklist are in [RELEASE.md](../RELEASE.md).
