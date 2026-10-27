# Development

Build, test and contribution rules are in [CONTRIBUTING.md](../CONTRIBUTING.md). This page covers running the operator locally and cutting a release.

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

Releases are cut with the **Release** workflow, run by hand on `main` and approved by an owner. It checks that lint, tests, verification and the end-to-end tests passed on the commit and chooses the version from the pull request titles since the last release. `make release-version` previews it. It then builds, signs and publishes the image and the chart, and only then creates the tag and the GitHub Release with generated notes and the packaged chart.

Actions are pinned to commit SHAs. The committed `Chart.yaml` version is a placeholder set at package time, so a release changes no file.

One-time setup: make the GHCR packages public, or give consumers a pull secret, and set the `ARTIFACTHUB_REPOSITORY_ID` Actions variable to the chart's Artifact Hub repository ID so Artifact Hub lists it under a verified publisher.
