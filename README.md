```
▐██ ▐██ ▐█████▄      ▐██████ ▐██████ ▐██████ ▐██████ ▐██████ ▐██████ ▐██████ ▐██████
▐██ ▐██ ▐██  ▐█      ▐██ ▐██ ▐██ ▐██ ▐██     ▐██ ▐██ ▐██ ▐██   ▐██   ▐██ ▐██ ▐██ ▐██
▐██████ ▐██████      ▐██ ▐██ ▐██████ ▐████   ▐██████ ▐██████   ▐██   ▐██ ▐██ ▐██████
▐██ ▐██ ▐██  ▐█      ▐██ ▐██ ▐██     ▐██     ▐██▐██  ▐██ ▐██   ▐██   ▐██ ▐██ ▐██▐██ 
▐██ ▐██ ▐█████▀      ▐██████ ▐██     ▐██████ ▐██ ▐██ ▐██ ▐██   ▐██   ▐██████ ▐██ ▐██
```
# rental-operator

Rent [Hyperbolic](https://hyperbolic.ai) on-demand GPUs from your own Kubernetes cluster, declaratively.

Drop your Hyperbolic API key into a `Secret`, `kubectl apply` a `GPURental`, and the operator provisions the GPUs against the Hyperbolic public API, keeps them reconciled, and surfaces the SSH/connection details on the resource's status. Delete the object and the rental is terminated and the GPUs released.

```yaml
apiVersion: hyperbolic.ai/v1alpha1
kind: GPURental
metadata:
  name: train-run-42
spec:
  providerConfigRef: { name: default }
  gpuType: h100
  gpuCount: 8
```

```console
$ kubectl get gpurentals
NAME           PHASE     GPU    COUNT   REGION         RENTAL-ID   RENTAL-NAME                    AGE
train-run-42   Running   h100   8       us-central-1   8817        conscious-bougainvillea-oryx   6m
```

---

## Contents

- [How it works](#how-it-works)
- [Prerequisites](#prerequisites)
- [Install](#install)
- [Quickstart](#quickstart)
- [Custom resources](#custom-resources)
- [Common operations](#common-operations)
- [Status & conditions](#status--conditions)
- [Local development (kind)](#local-development-kind)
- [Security](#security)
- [Limitations](#limitations)

---

## How it works

The operator runs a controller in your cluster that consumes the Hyperbolic public REST API (`https://api.hyperbolic.ai`) exactly as any customer would — with your API key. It holds no state of its own: everything durable lives either on the custom resources or server-side at Hyperbolic.

```
   Your Kubernetes cluster
   ┌─────────────────────────────────────────────────┐
   │  Secret(api-key)   ProviderConfig   GPURental   │
   │        │                │              │        │
   │        └──────┬─────────┘              │        │
   │               ▼                        ▼        │
   │          rental-operator (controller)       │
   │               │  reconcile: create/poll/term    │
   └───────────────┼─────────────────────────────────┘
                   │  HTTPS + Bearer <api-key>
                   ▼
        https://api.hyperbolic.ai/v2/on-demand/*
```

Three custom resources:

- **`ProviderConfig`** — points at the `Secret` holding your API key, validates it, and reports your account balance.
- **`GPURental`** — one object == one on-demand rental. The operator creates it, polls it to Running, publishes SSH/IP details on status, and terminates it on delete via a finalizer.
- **`GPUAvailability`** — a filtered, always-fresh view of what your account can rent. A shared per-account cache queries the catalogue once per interval and populates every `GPUAvailability` for that account, so adding more of them costs no extra API calls.

## Prerequisites

- A Kubernetes cluster (v1.25+) and `kubectl`.
- [Helm](https://helm.sh) 3.x.
- A **Hyperbolic API key** (`sk_live_…`) from the [dashboard](https://app.hyperbolic.ai).
- An **SSH public key registered on your Hyperbolic account** (in the dashboard) if you want to log into the rented VM — rentals bake in your account's registered key. The operator does **not** manage SSH keys (see [Limitations](#limitations)); if the API reports the account has no key, it surfaces a `NoSSHKey` condition instead of provisioning a machine you can't reach.

## Install

From a published release (recommended — chart + image are pulled from GHCR):

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

This installs the CRDs, the controller `Deployment`, and the RBAC it needs. By default the operator watches all namespaces.

> The chart ships the CRDs under `charts/rental-operator/crds/`. Helm installs CRDs on first install but does **not** upgrade them; on a chart upgrade that changes the CRDs, re-apply them with `kubectl apply -f charts/rental-operator/crds/`.

## Quickstart

1. **Store your API key** (in whatever namespace your rentals will live in):

   ```console
   kubectl create secret generic hyperbolic-key \
     --from-literal=api-key=sk_live_xxxxxxxxxxxxxxxxxxxx
   ```

2. **Create a ProviderConfig:**

   ```yaml
   apiVersion: hyperbolic.ai/v1alpha1
   kind: ProviderConfig
   metadata:
     name: default
   spec:
     apiKeySecretRef:
       name: hyperbolic-key
       key: api-key
   ```

   Check it authenticated:

   ```console
   $ kubectl get providerconfig default
   NAME      ENDPOINT                     BALANCE   READY   AGE
   default   https://api.hyperbolic.ai    124035    True    5s
   ```

3. **Rent GPUs:**

   ```yaml
   apiVersion: hyperbolic.ai/v1alpha1
   kind: GPURental
   metadata:
     name: train-run-42
   spec:
     providerConfigRef: { name: default }
     gpuType: h100
     gpuCount: 8
   ```

   ```console
   $ kubectl get gpurental train-run-42 -w
   ```

4. **Get your SSH connection** once it's Running:

   ```console
   $ kubectl get gpurental train-run-42 -o jsonpath='{.status.instances[*].ssh}'
   root@144.202.10.31:22
   ```

5. **Release the GPUs** by deleting the object:

   ```console
   $ kubectl delete gpurental train-run-42
   ```

## Custom resources

### ProviderConfig

| Field | Type | Description |
|---|---|---|
| `spec.apiKeySecretRef.name` | string (required) | Secret in this namespace holding the API key. |
| `spec.apiKeySecretRef.key` | string | Key within the Secret. Default `api-key`. |
| `spec.endpoint` | string | API base URL. Default `https://api.hyperbolic.ai`. |
| `status.balanceCents` | int | Account balance (cents) at last check. |
| `status.conditions[]` | condition | `Ready`, `Authenticated`. |

### GPURental

| Spec field | Type | Description |
|---|---|---|
| `providerConfigRef.name` | string (required) | The ProviderConfig (credential + endpoint) in this namespace to use. |
| `gpuType` | enum (required) | `h100`, `h200`, `b200`, `rtx-6000-pro`. |
| `gpuCount` | int (required) | GPUs to rent. `>8` yields a multi-node rental. |
| `region` | string | Region to rent in. Omit to auto-pick the cheapest available. |
| `networkType` | enum | `ethernet` or `infiniband`. Omit to let the operator pick an available fabric. |
| `displayName` | string (≤40) | Name shown in the Hyperbolic dashboard (the rental label). Opt-in. |
| `ports` | []int | Ports to open on the instance IP (forge regions only, ≤20, unique). Immutable once created. |
| `maxPriceCentsPerHour` | int | Refuse to create above this hourly price. |
| `terminated` | bool | Permanently release the GPUs but keep the object as a read-only record. One-way — cannot be unset; create a new GPURental to rent again. |
| `rebootGeneration` | int | Increase to reboot all nodes. |

| Status field | Description |
|---|---|
| `phase` | `Provisioning` (created, coming up) / `Running` / `Rebooting` / `Terminating` / `Terminated` / `Failed` / `Blocked` (can't be created yet — see the reason on `Ready`) / `Unknown`. |
| `rentalId` | The server's string rental id. |
| `name` | The server's 3-word rental name (shown in the dashboard). |
| `region` | Resolved region. |
| `costCentsPerHour` | Current hourly cost, in cents. |
| `openPorts` | Custom ports opened on the instance. |
| `instances[]` | Per-node `instanceRef`, `phase`, `ssh`, `internalIp`. |
| `conditions[]` | `Ready` plus a reason (see below). |

### GPUAvailability

A filtered view of the marketplace **for your account**. The catalogue is personalized to the API key, so a `providerConfigRef` is required; the filter fields are optional. The operator writes matching offerings (cheapest first) to status.

| Spec field | Type | Description |
|---|---|---|
| `providerConfigRef.name` | string (required) | The ProviderConfig whose account catalogue to show. |
| `gpuType` | enum | Filter to one model. |
| `gpuCount` | int | Filter to this GPU count. |
| `region` | string | Filter to one region. |
| `networkType` | enum | Filter to `ethernet` / `infiniband`. |
| `maxPriceCentsPerHour` | int | Keep offerings at or below this price. |
| `onlyAvailable` | bool | Keep only enabled offerings with known stock > 0. |

```console
$ kubectl get gpuavailability h100-ca
NAME      GPU    REGION      OFFERINGS   CHEAPEST-CENTS   REFRESHED   AGE
h100-ca   h100   ca-east-1   2           275              30s         3m

$ kubectl get gpuavailability h100-ca -o jsonpath='{.status.offerings[0]}'
{"gpuType":"h100","gpuCount":1,"region":"ca-east-1","networkType":"ethernet","costCentsPerHour":275,"available":9,"enabled":true}
```

The catalogue is personalized to your account (queried with your key), so it shows what your account can actually rent. `available` is tri-state — a known count, or absent when the provider doesn't report stock. A shared per-account cache means many `GPUAvailability` objects on one account cost a single API call per refresh interval (`--catalog-refresh-interval`, default 5m).

## Common operations

**Pick a region and fabric:**
```yaml
spec:
  gpuType: h200
  gpuCount: 16          # multi-node
  region: us-southeast-3
  networkType: infiniband
```

**Open custom ports** (forge regions only):
```yaml
spec:
  gpuType: h100
  gpuCount: 8
  ports: [8080, 8443, 6006]
```
They open directly on the instance's public IP at the same numbers (`status.openPorts`). Ports cannot be changed on a live rental — to change them, delete and recreate.

**Cap spend:**
```yaml
spec:
  maxPriceCentsPerHour: 3000   # refuse to create above $30/hr
```

**Terminate but keep the object as a record** (one-way; the CR becomes read-only):
```yaml
spec:
  terminated: true    # permanently releases the GPUs; cannot be unset
```

**Reboot all nodes:** bump `spec.rebootGeneration`.

**Name it in the dashboard:**
```yaml
spec:
  displayName: team-a-sweep-42
```

## Status & conditions

Everything the operator decides shows up on the `Ready` condition. Inspect it with:

```console
kubectl describe gpurental train-run-42
```

| Reason | Meaning / fix |
|---|---|
| `Running` | Rental is up; `status.instances[]` has your SSH. |
| `Provisioning` | Rental created, waiting for nodes. |
| `AvailabilityUnavailable` | No matching, in-stock option for that gpuType/count/region/fabric/price. Loosen the spec. |
| `InsufficientBalance` | Not enough balance (+ overdraft) for ~1h. Top up. |
| `RegionDisabled` | The region isn't enabled for your account. Pick another or omit `region`. |
| `PortsUnsupportedInRegion` | `spec.ports` set in a region without custom-port support. Use a supported region or drop `ports`. |
| `PortsImmutable` | You changed `spec.ports` on a live rental. Recreate to change ports. |
| `NoSSHKey` | Your account has no SSH key. Register one in the dashboard, then it retries. |
| `CredentialError` | The Secret/key is missing or invalid. |

Preflight failures never create a rental (no charge), and the operator backs off rather than hot-looping — fix the spec/account and it self-heals.

## Local development (kind)

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

# 4. Add your key + a ProviderConfig + a GPURental (see Quickstart)
```

Or run the controller out-of-cluster against your kubeconfig (installs CRDs and runs the manager locally):

```console
make install     # apply CRDs
make run         # run the controller locally
```

Run the unit tests:

```console
go test ./internal/...
```

## Releasing

Releases are cut by publishing a **GitHub Release** with a semver tag (`vX.Y.Z`). Merges to `main` only run lint/tests — they never publish. Publishing a Release triggers `.github/workflows/release.yml`, which:

1. builds and pushes a multi-arch image (`linux/amd64` + `linux/arm64`) to `ghcr.io/hyperboliclabs/rental-operator:X.Y.Z` (and `:latest`);
2. packages the Helm chart with `version`/`appVersion` set to the tag (so the chart pulls the matching image) and pushes it to `oci://ghcr.io/hyperboliclabs/charts/rental-operator`;
3. attaches the packaged `.tgz` to the Release.

The tag is the single source of truth for the version — the committed `Chart.yaml` version is only a placeholder and is overridden at package time. To release: cut the tag/Release; no manual version bump or image build is needed.

> One-time setup: the GHCR packages (image + chart) must be made public (or consumers configured with a pull secret), and the repository's Actions need `packages: write` (already declared in the workflow).

## Security

- Your API key lives only in a `Secret` you control. The operator reads it at reconcile, sends it solely to the configured Hyperbolic endpoint over HTTPS, and never logs it or writes it to status.
- The operator acts entirely as your account — it's an API client, so its blast radius is exactly what your API key can already do.
- It needs `get`/`list`/`watch` on `Secrets` and full access to its own CRDs (see the chart's RBAC). Its only external egress is `api.hyperbolic.ai:443`.

## Limitations

- **SSH keys are not managed by the operator.** Rentals use the SSH key registered on your Hyperbolic account (via the dashboard). Register one before renting. Per-rental keys aren't expressible in the public API today.
- **On-demand only.** Reserved-term rentals (prepaid commitments) are out of scope.
- **Ports are immutable** after creation and supported only in forge regions.
- **Polling.** The API has no push notifications, so status is poll-driven (≈10s while pending, ≈60s once running).

---

Licensed under the Apache License 2.0.
