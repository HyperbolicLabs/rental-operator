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
apiVersion: compute.hyperbolic.ai/v1alpha1
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
- [Contributing](#contributing)

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

- **`ProviderConfig`** — points at the `Secret` holding your API key, validates it, and reports your account's balance and active rentals as Hyperbolic reports them.
- **`GPURental`** — one object == one on-demand rental. The operator creates it, polls it to Running, publishes SSH/IP details on status, and terminates it on delete via a finalizer.
- **`GPUAvailability`** — a filtered, always-fresh view of what your account can rent. A shared per-account cache queries the catalogue once per interval and populates every `GPUAvailability` for that account, so adding more of them costs no extra API calls.

## Prerequisites

- A Kubernetes cluster (v1.25+) and `kubectl`.
- [Helm](https://helm.sh) 3.x.
- A **Hyperbolic API key** (`sk_live_...`) from the [dashboard](https://app.hyperbolic.ai).
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

**Namespaced install (least privilege).** By default the operator gets a `ClusterRole`, and Kubernetes RBAC can't limit Secret access by label, so it is allowed to read Secrets in every namespace (it only ever reads labeled ones). To keep it out of everything else, list the namespaces it should manage. The chart then installs a `Role` in each of them and no cluster-wide permissions at all:

```console
helm install rental-operator ./charts/rental-operator \
  --namespace rental-operator-system --create-namespace \
  --set 'watchNamespaces={ml-team,research}'
```

GPURentals, ProviderConfigs and their Secrets in other namespaces are then ignored. ClusterProviderConfigs still work: the chart adds a narrow `ClusterRole` for them and for reading namespace labels (no Secrets), and reads their Secrets only in the operator's namespace.

> The chart ships the CRDs under `charts/rental-operator/crds/`. Helm installs CRDs on first install but does **not** upgrade them; on a chart upgrade that changes the CRDs, re-apply them with `kubectl apply -f charts/rental-operator/crds/`.

## Quickstart

1. **Store your API key** (in whatever namespace your rentals will live in):

   ```console
   kubectl create secret generic hyperbolic-key \
     --from-literal=api-key=sk_live_xxxxxxxxxxxxxxxxxxxx
   kubectl label secret hyperbolic-key compute.hyperbolic.ai/credentials=true
   ```

   The operator only reads Secrets with that label, so it never holds the rest of your cluster's Secrets in memory.

2. **Create a ProviderConfig:**

   ```yaml
   apiVersion: compute.hyperbolic.ai/v1alpha1
   kind: ProviderConfig
   metadata:
     name: default
   spec:
     credentials:
       secretRef:
         name: hyperbolic-key
         key: api-key
   ```

   Check it authenticated:

   ```console
   $ kubectl get providerconfig default
   NAME      ENDPOINT                    BALANCE   CENTS/HR   RENTALS   READY   AGE
   default   https://api.hyperbolic.ai   124035    0          0         True    5s
   ```

3. **Rent GPUs:**

   ```yaml
   apiVersion: compute.hyperbolic.ai/v1alpha1
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

   The object stays (with phase `Terminating`) until Hyperbolic confirms the rental is gone, so you are never left paying for GPUs with nothing in the cluster pointing at them.

> **Before you delete the cluster:** delete your GPURentals first. `kind delete cluster` (or losing a cluster any other way) removes the operator too, and nothing is left to terminate the rentals, which keep billing until you terminate them in the [dashboard](https://app.hyperbolic.ai).

## Custom resources

### ProviderConfig

| Field | Type | Description |
|---|---|---|
| `spec.credentials.source` | string | Where the API key comes from. Only `Secret` (the default) today. |
| `spec.credentials.secretRef.name` | string (required) | Secret in this namespace holding the API key. It must be labeled `compute.hyperbolic.ai/credentials=true`. Otherwise the ProviderConfig reports `SecretNotLabeled` with the command to fix it. |
| `spec.credentials.secretRef.key` | string | Key within the Secret. Default `api-key`. |
| `spec.endpoint` | string | API base URL. Default `https://api.hyperbolic.ai`. Must be `https`. Plain `http` is accepted only for `localhost`, `127.0.0.1` or `::1` test servers. |
| `status.balanceCents` | int | Account balance (cents) at last check. |
| `status.activeRentals` | int | Rentals active on the account, including ones started outside this cluster, from Hyperbolic's active rental summary. |
| `status.costCentsPerHour` | int | Combined hourly cost (cents) of those rentals, from the same summary. |
| `status.managedRentals` | int | GPURentals using this config that have not finished. While above zero, deleting the config waits (finalizer `compute.hyperbolic.ai/in-use`), so its credentials outlive the rentals they pay for. |
| `status.conditions[]` | condition | `Ready`, `Authenticated`. |

### ClusterProviderConfig

A cluster-scoped ProviderConfig for platform teams that manage one Hyperbolic API key centrally and let several namespaces rent through it. It takes the same `spec` as a ProviderConfig plus `allowedNamespaces`, and rentals use it with `providerConfigRef: { kind: ClusterProviderConfig, name: shared }`.

Like cert-manager's `ClusterIssuer`, its credentials Secret lives in the **operator's own namespace** (`rental-operator-system` with the chart), never in the namespaces that use it, so creating one never grants access to Secrets anywhere else. Label that Secret `compute.hyperbolic.ai/credentials=true` too.

| Field | Type | Description |
|---|---|---|
| `spec.credentials`, `spec.endpoint` | | As on ProviderConfig. The Secret is read from the operator's namespace. |
| `spec.allowedNamespaces` | label selector | Namespaces whose GPURentals and GPUAvailabilities may use this config, by namespace labels (`kubernetes.io/metadata.name` selects by name). Omit to allow every namespace. Others are Blocked with `NamespaceNotAllowed`. |

```yaml
apiVersion: compute.hyperbolic.ai/v1alpha1
kind: ClusterProviderConfig
metadata:
  name: shared
spec:
  credentials:
    secretRef: { name: hyperbolic-shared-key }
  allowedNamespaces:
    matchLabels: { hyperbolic.ai/tenant: ml }
```

### GPURental

| Spec field | Type | Description |
|---|---|---|
| `providerConfigRef` | `{kind, name}` (required) | The account to rent with: a ProviderConfig in this namespace (`kind` defaults to `ProviderConfig`) or a `ClusterProviderConfig`. |
| `gpuType` | string (required) | GPU model as the catalog names it, e.g. `h100`, `h200`, `b200`, `rtx-6000-pro`. Checked against your account's catalog at create time, so new models work without an operator upgrade. |
| `gpuCount` | int (required) | GPUs to rent. `>8` yields a multi-node rental. |
| `region` | string | Region to rent in. Omit to auto-pick the cheapest available. |
| `networkType` | enum | `ethernet` or `infiniband`. Omit to let the operator pick an available fabric. |
| `displayName` | string, up to 40 characters | Name shown in the Hyperbolic dashboard (the rental label). Defaults to `<metadata.name>-<6 characters of the UID>`. The operator uses the label to find its rental if a create's outcome is unknown, so keep it unique within the account. |
| `ports` | []int | Ports to open on the instance IP (forge regions only, at most 20, unique, 1-65535). |
| `maxPriceCentsPerHour` | int | Refuse to create above this hourly price. Can be changed at any time. |
| `terminated` | bool | Permanently release the GPUs but keep the object as a read-only record. This is one-way and cannot be unset. Create a new GPURental to rent again. |
| `deletionPolicy` | `Delete` \| `Orphan` | What deleting the object does. `Delete` (default) terminates the rental first and keeps the object until it is confirmed gone. `Orphan` removes the object and leaves the rental running. |
| `rebootGeneration` | int | Increase to reboot all nodes. |

`providerConfigRef`, `gpuType`, `gpuCount`, `region`, `networkType`, `ports` and `displayName` cannot change after the GPURental is created. The API server rejects the edit. Rentals can't be resized or moved to another account in place, so delete the object and create a new one (a `Blocked` rental never rented anything, so recreating it costs nothing).

| Status field | Description |
|---|---|
| `phase` | `Provisioning` (created, coming up) / `Running` / `Rebooting` / `Terminating` / `Terminated` / `Failed` / `Blocked` (can't be created yet, see the reason on `Ready`) / `Unknown`. |
| `rentalId` | The server's string rental id. |
| `name` | The server's 3-word rental name (shown in the dashboard). |
| `label` | The rental's label (the name in the dashboard). |
| `region` | Resolved region. |
| `networkType` | The rental's fabric: `ethernet` or `infiniband`. |
| `costCentsPerHour` | Current hourly cost, in cents. |
| `openPorts` | Custom ports opened on the instance. |
| `instances[]` | Per-node `instanceRef`, `phase`, `ssh`, `internalIp`. |
| `terminatedAt` | When the rental was seen terminated. Once set, the object is a read-only record and never rents again. |
| `conditions[]` | `Ready` and `Synced` (see below). |

### GPUAvailability

A filtered view of the marketplace **for your account**. The catalogue is personalized to the API key, so a `providerConfigRef` is required; the filter fields are optional. The operator writes matching offerings (cheapest first) to status.

| Spec field | Type | Description |
|---|---|---|
| `providerConfigRef.name` | string (required) | The ProviderConfig whose account catalogue to show. |
| `gpuType` | string | Filter to one model. |
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

The catalogue is personalized to your account (queried with your key), so it shows what your account can actually rent. `available` is the known in-stock count, or absent when the provider doesn't report stock. A shared per-account cache means many `GPUAvailability` objects on one account cost a single API call per refresh interval (`--catalog-refresh-interval`, default 5m).

## Common operations

**List everything the operator manages** (every kind is in the `hyperbolic` category):
```console
kubectl get hyperbolic -A
```
Short names work too: `hpc` (ProviderConfig), `hcpc` (ClusterProviderConfig), `gpr` (GPURental), `gpa` (GPUAvailability).

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
They open directly on the instance's public IP at the same numbers (`status.openPorts`). Ports cannot be changed after creation. To change them, delete and recreate.

**Cap spend:**
```yaml
spec:
  maxPriceCentsPerHour: 3000   # refuse to create above $30/hr
```

**Delete the object but keep the rental running** (to hand it to another system, or when the API key is gone and the operator cannot terminate it):
```yaml
spec:
  deletionPolicy: Orphan
```
Without it, a GPURental whose credentials can't be read stays in `Terminating` with a `TerminationBlocked` event that explains how to proceed: restore the Secret, or set `deletionPolicy: Orphan` and terminate the rental in the dashboard. If the Secret disappears while the operator is running (as in `kubectl delete namespace`), the operator still terminates with the last key it read for that ProviderConfig.

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

Every resource reports two conditions, and automation should read them rather than `phase`:

- **`Ready`** says whether the thing is usable (the rental is running, the config's key works, the catalog view is populated). Its reason says why not.
- **`Synced`** says whether the last reconcile could read the API key and reach the Hyperbolic API. `Synced=False` with `APIError` or `CredentialError` is a transient or configuration problem. It never changes `Ready`, so a failed poll never marks a running rental as gone.

Inspect them with:

```console
kubectl describe gpurental train-run-42
```

| Reason | Meaning / fix |
|---|---|
| `Running` | Rental is up; `status.instances[]` has your SSH. |
| `Provisioning` | Rental created, waiting for nodes. |
| `Terminated` | The operator terminated the rental (`spec.terminated`). The object stays as a record. |
| `TerminatedExternally` | The rental ended without the operator (balance ran out, ended in the dashboard, hardware failure). It is never rented again automatically. Create a new GPURental. |
| `AvailabilityUnavailable` | No matching, in-stock option for that gpuType/count/region/fabric/price. Loosen the spec. |
| `InsufficientBalance` | Not enough balance (+ overdraft) for ~1h. Top up. |
| `RegionDisabled` | The region isn't enabled for your account. Pick another or omit `region`. |
| `PortsUnsupportedInRegion` | `spec.ports` set in a region without custom-port support. Use a supported region or drop `ports`. |
| `NoSSHKey` | Your account has no SSH key. Register one in the dashboard, then it retries. |
| `NamespaceNotAllowed` | The ClusterProviderConfig's `allowedNamespaces` doesn't match this namespace. |
| `CredentialError` | The Secret/key is missing or invalid. |
| `APIError` | The Hyperbolic API failed (5xx, timeout, rate limit). Retried every 30s. |
| `CreateOutcomeUnknown` | A create may have succeeded, but several active rentals carry this object's label, so the operator can't tell which is its own. It creates nothing more. Terminate the extras in the dashboard and it adopts the one left. |

Preflight failures never create a rental (no charge), and the operator backs off rather than hot-looping. Fix the spec or the account and it recovers on its own.

Creates are never retried blindly. Before each create the operator records `compute.hyperbolic.ai/create-pending` on the object. If a create times out or fails with a 5xx (it may still have happened), the operator looks the rental up by its label and adopts it, and only creates again once a minute has passed with nothing found.

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

Releases are cut with the **Release** workflow, run by hand on `main` and approved by an owner. It checks that lint, tests, verification and the end-to-end tests passed on the commit and chooses the version from the pull request titles since the last release. `make release-version` previews it. It then builds, signs and publishes the image and the chart, and only then creates the tag and the GitHub Release with generated notes and the packaged chart.

- The image `ghcr.io/hyperboliclabs/rental-operator:X.Y.Z` is built for `linux/amd64` and `linux/arm64`, tagged `latest` unless it is a pre-release, and carries an SPDX SBOM and SLSA build provenance.
- The chart `oci://ghcr.io/hyperboliclabs/charts/rental-operator` has `version` and `appVersion` set to the release, so it pulls the matching image.

Both are signed with [cosign](https://docs.sigstore.dev/), keyless, from the release workflow's GitHub identity. To verify before installing:

```console
cosign verify ghcr.io/hyperboliclabs/rental-operator:X.Y.Z \
  --certificate-identity 'https://github.com/HyperbolicLabs/rental-operator/.github/workflows/release.yml@refs/heads/main' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
cosign verify ghcr.io/hyperboliclabs/charts/rental-operator:X.Y.Z \
  --certificate-identity 'https://github.com/HyperbolicLabs/rental-operator/.github/workflows/release.yml@refs/heads/main' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
docker buildx imagetools inspect ghcr.io/hyperboliclabs/rental-operator:X.Y.Z --format '{{ json .SBOM }}'
```

Actions are pinned to commit SHAs. The committed `Chart.yaml` version is a placeholder set at package time, so a release changes no file.

One-time setup: make the GHCR packages public, or give consumers a pull secret, and set the `ARTIFACTHUB_REPOSITORY_ID` Actions variable to the chart's Artifact Hub repository ID so Artifact Hub lists it under a verified publisher.

## Security

- Your API key lives only in a `Secret` you control. The operator reads it at reconcile, sends it solely to the configured Hyperbolic endpoint over HTTPS, and never logs it or writes it to status.
- The operator acts entirely as your account — it's an API client, so its blast radius is exactly what your API key can already do.
- It needs `get`/`list`/`watch` on `Secrets`, full access to its own CRDs, and `create`/`patch` on `Events` (see the chart's RBAC). Its only external egress is `api.hyperbolic.ai:443`.
- It only reads and caches Secrets labeled `compute.hyperbolic.ai/credentials=true`. Kubernetes RBAC cannot grant access by label, so in the default cluster-wide install its role can still read any Secret. The label keeps everything else out of its memory and its logic.
- Every request carries `User-Agent: rental-operator/<version> hyperbolic-go/<version>`. That is the only usage signal. There is no other telemetry.
- Report vulnerabilities privately, as [SECURITY.md](SECURITY.md) describes.

## Limitations

- **SSH keys are not managed by the operator.** Rentals use the SSH key registered on your Hyperbolic account (via the dashboard). Register one before renting. Per-rental keys aren't expressible in the public API today.
- **On-demand only.** Reserved-term rentals (prepaid commitments) are out of scope.
- **Ports are immutable** after creation and supported only in forge regions.
- **Polling.** The API has no push notifications, so status is poll-driven (≈10s while pending, ≈60s once running). All GPURentals on one account share a single list of rentals per interval, so a hundred rentals cost the same API budget as one.

## Contributing

Issues and PRs are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) and the [code of conduct](CODE_OF_CONDUCT.md).

---

Licensed under the [Apache License 2.0](LICENSE).
