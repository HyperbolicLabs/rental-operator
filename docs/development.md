# Development

Build, test and contribution rules are in [CONTRIBUTING.md](../CONTRIBUTING.md). This page covers running the operator locally.

- [Run it on kind](#run-it-on-kind)
- [See metrics locally](#see-metrics-locally)
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

## See metrics locally

Install Prometheus with the trimmed-down values in [`hack/monitoring/kind-values.yaml`](../hack/monitoring/kind-values.yaml), then turn on the operator's metrics and alert rules:

```console
helm install kps oci://ghcr.io/prometheus-community/charts/kube-prometheus-stack \
  --version 92.10.1 --namespace monitoring --create-namespace \
  -f hack/monitoring/kind-values.yaml --wait
helm upgrade rental-operator ./charts/rental-operator -n rental-operator-system --reuse-values \
  --set metrics.enabled=true --set metrics.serviceMonitor.enabled=true \
  --set metrics.prometheusRule.enabled=true
kubectl create clusterrolebinding rental-operator-metrics-reader-prometheus \
  --clusterrole=rental-operator-metrics-reader \
  --serviceaccount=monitoring:kps-kube-prometheus-stack-prometheus
kubectl -n monitoring port-forward svc/kps-kube-prometheus-stack-prometheus 9090
```

Open <http://localhost:9090> and query `{__name__=~"rental_operator_.*"}`, or see the alerts under Alerts.

## Run it out of cluster

Run the controller against your current kubeconfig:

```console
make install     # apply the CRDs
make run         # run the controller locally
```

## Releasing

Versioning, what a release contains and the maintainer checklist are in [RELEASE.md](../RELEASE.md).
