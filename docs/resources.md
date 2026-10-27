# Custom resources

Every kind is in the `hyperbolic` category, so `kubectl get hyperbolic -A` lists them all. Short names: `hpc` (ProviderConfig), `hcpc` (ClusterProviderConfig), `gpr` (GPURental), `gpa` (GPUAvailability).

- [ProviderConfig](#providerconfig)
- [ClusterProviderConfig](#clusterproviderconfig)
- [GPURental](#gpurental)
- [GPUAvailability](#gpuavailability)
- [SSHKey](#sshkey)
- [Status and conditions](#status-and-conditions)

## ProviderConfig

| Field | Type | Description |
|---|---|---|
| `spec.credentials.source` | string | Where the API key comes from. Only `Secret` (the default) today. |
| `spec.credentials.secretRef.name` | string (required) | Secret in this namespace holding the API key. It must be labeled `compute.hyperbolic.ai/credentials=true`. Otherwise the ProviderConfig reports `SecretNotLabeled` with the command to fix it. |
| `spec.credentials.secretRef.key` | string | Key within the Secret. Default `api-key`. |
| `spec.endpoint` | string | API base URL. Default `https://api.hyperbolic.ai`. Must be `https`. Plain `http` is accepted only for `localhost`, `127.0.0.1` or `::1` test servers. |
| `status.balanceCents` / `status.maxOverdraftCents` | int | Account balance and overdraft allowance (cents) at last check. |
| `status.activeRentals` / `status.costCentsPerHour` | int | Rentals active on the account and their combined hourly cost (cents), including rentals started outside this cluster, from Hyperbolic's active rental summary. |
| `status.lastChecked` | time | When the key, balance and summary were last read. They are read every 10 minutes and when a GPURental using the config is created or ends. |
| `status.managedRentals` | int | GPURentals using this config that have not finished. While above zero, or while SSHKeys use the config, deleting the config waits (finalizer `compute.hyperbolic.ai/in-use`), so its credentials outlive the rentals they pay for and the keys they saved. |
| `status.conditions[]` | condition | `Ready`, `Authenticated`, `Synced`. |

## ClusterProviderConfig

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

## GPURental

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
| `sshKeyRefs` | `[]{name}` | [SSHKeys](#sshkey) in this namespace to authorize on the rental. The rental waits, `Blocked` with `SSHKeyNotReady`, until each one is Ready and uses the same `providerConfigRef`. They can change while the rental runs: added keys are attached and removed ones detached, and keys the operator did not attach are left alone. Some rentals don't allow it, and `Synced` then says `SSHKeyChangeRejected`. Omit to get the account's newest saved key. |

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
| `sshKeyIds` | The saved keys the operator authorized on the rental, from `sshKeyRefs`. |
| `terminatedAt` | When the rental was seen terminated. Once set, the object is a read-only record and never rents again. |
| `conditions[]` | `Ready` and `Synced` (see below). |

## GPUAvailability

A filtered view of the marketplace **for your account**. The catalogue is personalized to the API key, so a `providerConfigRef` is required. The filter fields are optional. The operator writes matching offerings (cheapest first) to status.

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

## SSHKey

Saves an SSH public key on the account, so GPURentals can authorize it by name. The account holds each key once, matched on its type and body but not its comment, so when the account already has the key, the SSHKey adopts that copy instead of saving another.

| Spec field | Type | Description |
|---|---|---|
| `providerConfigRef` | `{kind, name}` (required) | The account to save the key on. Cannot change. |
| `publicKey` | string (required) | One OpenSSH public key line, such as the contents of `~/.ssh/id_ed25519.pub`. Cannot change. |
| `deletionPolicy` | `Delete` \| `Orphan` | What deleting the object does. `Delete` (default) deletes the key from the account if the operator saved it, once no live rental uses it. `Orphan` leaves it saved. An adopted key is never deleted. |

| Status field | Description |
|---|---|
| `id` | The saved key's id on the account. |
| `conditions[]` | `Ready`, with reason `Saved` or `Adopted` once GPURentals can use the key, and `Synced`. |

```console
$ kubectl get sshkey me
NAME   ID    READY   REASON   AGE
me     812   True    Saved    5s
```

> [!WARNING]
> Saving a key makes it the account's newest, and the API authorizes the newest key on every rental created without naming keys. That includes GPURentals without `sshKeyRefs` and rentals teammates create in the dashboard. Name keys in `sshKeyRefs` rather than relying on the default.

The key is saved under the name `<namespace>/<name>`, which the dashboard shows. If it is deleted outside the operator, the operator saves it again. Deleting the SSHKey waits while a live rental uses the key, with reason `KeyInUse`, until it is removed from those GPURentals' `sshKeyRefs`, detached in the dashboard, or the rentals end.

## Status and conditions

Every resource reports two conditions, and automation should read them rather than `phase`:

- **`Ready`** says whether the thing is usable (the rental is running, the config's key works, the catalog view is populated). Its reason says why not.
- **`Synced`** says whether the last reconcile could read the API key and reach the Hyperbolic API. `Synced=False` with `APIError` or `CredentialError` is a transient or configuration problem. It never changes `Ready`, so a failed poll never marks a running rental as gone.

Inspect them with:

```console
kubectl describe gpurental train-run-42
```

| Reason | Meaning / fix |
|---|---|
| `Running` | Rental is up. `status.instances[]` has your SSH. |
| `Provisioning` | Rental created, waiting for nodes. |
| `Terminated` | The operator terminated the rental (`spec.terminated`). The object stays as a record. |
| `TerminatedExternally` | The rental ended without the operator (balance ran out, ended in the dashboard, hardware failure). It is never rented again automatically. Create a new GPURental. |
| `AvailabilityUnavailable` | No matching, in-stock option for that gpuType/count/region/fabric/price. Loosen the spec. |
| `InsufficientBalance` | Not enough balance (+ overdraft) for ~1h. Top up. |
| `RegionDisabled` | The region isn't enabled for your account. Pick another or omit `region`. |
| `PortsUnsupportedInRegion` | `spec.ports` set in a region without custom-port support. Use a supported region or drop `ports`. |
| `NoSSHKey` | Your account has no saved SSH key. Save one with an [SSHKey](#sshkey) or in the dashboard, then it retries. |
| `SSHKeyChangeRejected` | The API refused to attach or detach a key on this running rental, because the rental doesn't allow key changes. It keeps the keys it has. Delete the GPURental and create a new one to rent with other keys. |
| `SSHKeyNotReady` | An SSHKey in `sshKeyRefs` does not exist, is not saved yet, is being deleted or is saved with another config. A rental not created yet waits. On a running rental it is reported on `Synced` and the keys stay as they are. |
| `NamespaceNotAllowed` | The ClusterProviderConfig's `allowedNamespaces` doesn't match this namespace. |
| `CredentialError` | The Secret/key is missing or invalid. |
| `APIError` | The Hyperbolic API failed (5xx, timeout, rate limit). Retried every 30s. |
| `CreateOutcomeUnknown` | A create may have succeeded, but several active rentals carry this object's label, so the operator can't tell which is its own. It creates nothing more. Terminate the extras in the dashboard and it adopts the one left. |
| `Saved` | SSHKey: the operator saved the key. Deleting the SSHKey deletes it, unless `deletionPolicy` is `Orphan`. |
| `Adopted` | SSHKey: the account already had the key. GPURentals can use it, and the operator never deletes it. |
| `KeyRejected` | SSHKey: the API refused to save the key, for example an unsupported key type. The message has the API's reason. |
| `KeyInUse` | SSHKey: deletion waits until no live rental uses the key. |

Preflight failures never create a rental (no charge), and the operator backs off rather than hot-looping. Fix the spec or the account and it recovers on its own.

Creates are never retried blindly. Before each create the operator records `compute.hyperbolic.ai/create-pending` on the object. If a create times out or fails with a 5xx (it may still have happened), the operator looks the rental up by its label and adopts it, and only creates again once a minute has passed with nothing found. Saving an SSH key works the same way, and the account refuses a second copy of a key, so a key is never saved twice.
