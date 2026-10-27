# Observability

The operator exposes Prometheus metrics about your rentals, your configs and its calls to the Hyperbolic API, and the chart ships alert rules for the situations that cost money or need a person. Billing itself is Hyperbolic's: the account's balance and spend are in the [dashboard](https://app.hyperbolic.ai) and on the ProviderConfig's status.

- [Turn on metrics](#turn-on-metrics)
- [Metrics](#metrics)
- [Alerts](#alerts)
- [Useful queries](#useful-queries)

## Turn on metrics

Metrics are off by default. With the [Prometheus Operator](https://prometheus-operator.dev/) (for example kube-prometheus-stack):

```console
helm upgrade rental-operator oci://ghcr.io/hyperboliclabs/charts/rental-operator \
  --version X.Y.Z --namespace rental-operator-system --reuse-values \
  --set metrics.enabled=true \
  --set metrics.serviceMonitor.enabled=true \
  --set metrics.prometheusRule.enabled=true \
  --set metrics.serviceMonitor.labels.release=kube-prometheus-stack \
  --set metrics.prometheusRule.labels.release=kube-prometheus-stack
```

The `release` labels are an example. Use whatever labels your Prometheus selects ServiceMonitors and PrometheusRules by.

The endpoint serves HTTPS on port 8443 with a self-signed certificate, and answers only requests whose bearer token is allowed to `GET /metrics`. Grant that to the service account Prometheus scrapes with:

```console
kubectl create clusterrolebinding rental-operator-metrics-reader \
  --clusterrole=rental-operator-metrics-reader \
  --serviceaccount=monitoring:kube-prometheus-stack-prometheus
```

Without the Prometheus Operator, scrape the `rental-operator-metrics` Service on port `metrics` the same way, with `honor_labels: true`, and load [`files/alerts.yaml`](../charts/rental-operator/files/alerts.yaml) as a rule file. The series carry the namespace of the object they describe. Without `honor_labels`, Prometheus replaces it with the operator pod's namespace. `metrics.secure=false` serves plain HTTP with no authentication instead. Use it only where nothing untrusted can reach the pod.

| Value | Default | Description |
|---|---|---|
| `metrics.enabled` | `false` | Serve metrics through the `<fullname>-metrics` Service. |
| `metrics.port` | `8443` | Port of the endpoint and the Service. |
| `metrics.secure` | `true` | HTTPS with token authentication and authorization. |
| `metrics.serviceMonitor.enabled` | `false` | Create a ServiceMonitor. |
| `metrics.serviceMonitor.interval` | `60s` | Scrape interval. |
| `metrics.serviceMonitor.labels` | `{}` | Extra labels on the ServiceMonitor. |
| `metrics.prometheusRule.enabled` | `false` | Create a PrometheusRule with the alerts below. |
| `metrics.prometheusRule.labels` | `{}` | Extra labels on the PrometheusRule. |

## Metrics

Gauges about objects are read from the operator's cache at scrape time, so they always match `kubectl get` and disappear when the object does. With more than one replica, every replica reports them, so aggregate with `max by`.

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `rental_operator_gpurental_status` | gauge | `namespace`, `name`, `phase`, `reason` | Always 1. The GPURental's phase and the reason on its `Ready` condition. |
| `rental_operator_gpurental_termination_pending` | gauge | `namespace`, `name` | 1 while the GPURental has been deleted or set to `terminated` and its rental is not confirmed gone. |
| `rental_operator_providerconfig_managed_rentals` | gauge | `kind`, `namespace`, `name` | GPURentals using the config that have not finished. `namespace` is empty for a ClusterProviderConfig. |
| `rental_operator_providerconfig_condition` | gauge | `kind`, `namespace`, `name`, `type`, `status` | Always 1. One series per condition (`Ready`, `Authenticated`, `Synced`). |
| `rental_operator_rental_operations_total` | counter | `operation` (`create`, `terminate`), `result` (`success`, `failed`, `unknown`) | Creates and terminations sent to the API. `unknown` is a create that timed out or failed with a 5xx and is settled by a lookup. |
| `rental_operator_api_requests_total` | counter | `method`, `path`, `code` | Every request to the Hyperbolic API. `code` is the HTTP status, or `error` when no response arrived. |
| `rental_operator_api_request_duration_seconds` | histogram | `method`, `path` | Time until the API's response headers arrived. |

The manager also serves controller-runtime's standard metrics (`controller_runtime_reconcile_total`, `workqueue_depth` and others) and Go runtime metrics.

## Alerts

| Alert | Severity | Fires when | What to do |
|---|---|---|---|
| `GPURentalTerminationStuck` | critical | A deleted or `terminated` GPURental's rental is not confirmed gone after 15 minutes. | It may be billing. `kubectl describe gpurental` shows a `TerminationBlocked` or `TerminateFailed` event: restore the API key Secret, or set `deletionPolicy: Orphan` and terminate the rental in the dashboard. |
| `GPURentalTerminateFailing` | warning | More than 5 terminate requests failed in 15 minutes. | Check `rental_operator_api_requests_total` for the status codes. The operator keeps retrying every 30 seconds. |
| `GPURentalCreateOutcomeUnknown` | critical | For 5 minutes, several active rentals carry one GPURental's label. | The extras are billing. Terminate them in the dashboard, and the operator adopts the one left. |
| `GPURentalProvisioningSlow` | warning | A rental has been provisioning for 20 minutes. | Usually capacity at Hyperbolic. Check its `Ready` condition and the rental in the dashboard, and delete the GPURental to give up. |

The rules live in [`charts/rental-operator/files/alerts.yaml`](../charts/rental-operator/files/alerts.yaml) and are tested with `promtool` (`make test-alerts`).

## Useful queries

Rentals by phase:

```promql
count by (phase) (max by (namespace, name, phase) (rental_operator_gpurental_status))
```

Share of Hyperbolic API requests that failed over the last 10 minutes:

```promql
sum(rate(rental_operator_api_requests_total{code=~"5..|error"}[10m]))
  / sum(rate(rental_operator_api_requests_total[10m]))
```
