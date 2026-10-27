# Architecture

rental-operator is a Kubernetes controller that rents Hyperbolic on-demand GPUs through the public REST API, using your API key the way any customer would. It keeps no state of its own: everything durable lives on the custom resources, in their annotations and status, or server-side at Hyperbolic. Restarting or upgrading the operator loses nothing.

- [Components](#components)
- [GPURental lifecycle](#gpurental-lifecycle)
- [Creating a rental: never rent twice](#creating-a-rental-never-rent-twice)
- [Deleting a rental: never leak](#deleting-a-rental-never-leak)
- [Polling and API budget](#polling-and-api-budget)
- [Credentials](#credentials)

## Components

```mermaid
flowchart LR
  subgraph objects["Your objects"]
    direction TB
    config["ProviderConfig or<br/>ClusterProviderConfig<br/>+ labeled Secret"]
    rental["GPURental"]
    avail["GPUAvailability"]
  end
  subgraph operator["rental-operator"]
    direction TB
    cfgctl["Config controllers<br/>key check, balance"]
    rentctl["GPURental controller<br/>preflight, create, poll, terminate"]
    availctl["GPUAvailability controller"]
    cache[("Per-account cache<br/>rental list, catalog")]
  end
  api["Hyperbolic API"]
  config <--> cfgctl
  rental <--> rentctl
  avail <--> availctl
  rentctl -- "reads" --> cache
  availctl -- "reads" --> cache
  cfgctl -- "balance" --> api
  rentctl -- "create, terminate" --> api
  cache -- "one call per account<br/>per interval" --> api
```

The manager runs four controllers:

| Controller | Watches | Does |
|---|---|---|
| ProviderConfig | ProviderConfigs, labeled Secrets, GPURentals | Checks the API key, shows the account's balance and active rental summary as the API reports them, and holds the `in-use` finalizer while rentals still need the key. |
| ClusterProviderConfig | ClusterProviderConfigs, labeled Secrets in the operator's namespace, GPURentals | The same for a cluster-scoped config, plus `allowedNamespaces`. |
| GPURental | GPURentals | Preflight, create, poll, reboot, terminate. Everything that spends money. |
| GPUAvailability | GPUAvailabilities | Filters the account's catalog into status. |

Each controller acts on what it observes on the object and the API, not on the event that woke it, so a missed or repeated event never changes the outcome.

## GPURental lifecycle

`status.phase` is for people reading `kubectl get`. Automation should read the `Ready` and `Synced` conditions. See [Status and conditions](resources.md#status-and-conditions).

```mermaid
stateDiagram-v2
  direction TB
  [*] --> Blocked: preflight fails
  [*] --> Provisioning: created
  Blocked --> Provisioning: preflight passes
  Blocked --> [*]: deleted
  Provisioning --> Running: nodes up
  Running --> Rebooting: reboot
  Rebooting --> Running
  Provisioning --> Terminating: delete, terminated
  Running --> Terminating: delete, terminated
  Terminating --> Terminated: confirmed gone
  Running --> Terminated: ended outside the operator
  Terminated --> [*]
```

- **Provisioning** to **Running** is when billing starts. A rental ends through `kubectl delete` or `spec.terminated: true`. Bumping `spec.rebootGeneration` reboots it.
- **Blocked** means nothing was rented and nothing is billing. The reason on `Ready` says why (no stock, over the price cap, low balance, no SSH key). The operator retries every 5 minutes, and immediately when you change the spec.
- **Terminated** is final. The object stays as a read-only record when the rental ended because of `spec.terminated` or something outside the operator. It is never rented again. Create a new GPURental. When the rental ended because you deleted the object, the object goes away once Hyperbolic confirms the rental is gone.
- **Failed** and **Unknown** come from Hyperbolic's own status for the rental, or from a create whose outcome cannot be settled (`CreateOutcomeUnknown`).

## Creating a rental: never rent twice

A create that times out or fails with a 5xx may still have rented the GPUs. Retrying it blindly is how a cluster ends up paying for two rentals, so the operator records its intent before it sends anything and settles an unknown outcome by looking the rental up instead of creating again.

```mermaid
sequenceDiagram
  autonumber
  participant C as GPURental controller
  participant K as Kubernetes API
  participant H as Hyperbolic API

  C->>H: Read catalog (cached) and balance
  Note over C: Preflight: stock, price cap,<br/>balance. Fail = Blocked.
  C->>K: Annotate create-pending with the time
  Note over C,K: If this write fails, nothing is sent.
  C->>H: Create rental, labeled with the object's name and UID
  alt Created
    H-->>C: Rental id
    C->>K: Annotate rental-id, clear create-pending
    C->>K: Status: Provisioning
  else Definite failure, nothing created
    H-->>C: 4xx error
    C->>K: Clear create-pending, Blocked with the reason
  else Timeout or 5xx: outcome unknown
    H--xC: No usable answer
    C->>H: Next reconcile: find rentals by label
    alt Exactly one
      C->>K: Adopt it: annotate rental-id
    else None, a minute after the create
      C->>H: Create again
    else Several
      C->>K: Ready=Unknown, CreateOutcomeUnknown.<br/>Create nothing until a person decides.
    end
  end
```

- The rental id is written to an annotation before status, because an annotation survives a failed status write. If even that write fails, the next reconcile finds the rental by its label.
- The label is `<metadata.name>-<6 characters of the UID>`, or `spec.displayName`. The UID part keeps it unique when objects reuse a name.
- A rental is adopted only if its GPU type, count and region match the spec, so a stranger's rental with the same dashboard name is never taken over.

## Deleting a rental: never leak

A GPURental carries the finalizer `compute.hyperbolic.ai/terminate`. Kubernetes keeps the object until the operator removes it, and the operator removes it only when nothing can still be billing.

```mermaid
sequenceDiagram
  autonumber
  actor U as You
  participant K as Kubernetes API
  participant C as GPURental controller
  participant H as Hyperbolic API

  U->>K: kubectl delete gpurental
  K-->>C: Deletion requested, the finalizer keeps the object
  alt deletionPolicy: Orphan
    C->>K: Orphaned event, remove the finalizer
    Note over H: The rental keeps running
  else Nothing was ever rented
    C->>K: Remove the finalizer
  else A rental may be billing
    opt The API key Secret is gone
      Note over C: Use the last key read for this config,<br/>or wait with a TerminationBlocked event
    end
    C->>H: Terminate
    loop Every 10s until Hyperbolic stops listing it
      C->>H: List rentals
    end
    C->>K: Terminated event, remove the finalizer
  end
  K-->>U: Object deleted
```

- "May be billing" includes a create whose outcome is still unknown. The operator first finds that rental by label.
- `kubectl delete namespace` deletes the Secret and the ProviderConfig alongside the rentals. The operator still terminates them with the last key it read for that config, and the ProviderConfig's `in-use` finalizer keeps the config until its rentals are gone.
- If the operator cannot terminate (the key is gone and it never read it, or several rentals carry the label), the object stays in `Terminating` with a Warning event that says how to proceed. A stuck finalizer is cheaper than a GPU billing with nothing pointing at it.
- The operator must be running for any of this. Deleting the cluster or uninstalling the operator first leaves rentals billing.

## Polling and API budget

The Hyperbolic API has no push notifications, so status is poll-driven.

| What | How often | Shared |
|---|---|---|
| Rental status | 10s while any rental on the account is provisioning, 60s otherwise | One list of rentals per account per interval, whatever the number of GPURentals |
| Catalog (rental options) | `--catalog-refresh-interval`, default 5m | One query per account per interval, for every GPUAvailability and preflight |
| Balance and active rental summary | Every 10 minutes, and when a GPURental using the config is created or ends | One read of each per config |

A list that was in flight while a rental was created or terminated is discarded, and a rental missing from a cached list is re-checked with a fresh read before it is marked as ended. A failed poll never marks a running rental as gone: it sets `Synced=False` and retries in 30s.

## Credentials

- The operator caches only Secrets labeled `compute.hyperbolic.ai/credentials=true`. Kubernetes RBAC cannot grant access by label, so in the default cluster-wide install its role can still read any Secret. The [namespaced install](install.md#namespaced-install) removes that access.
- A ClusterProviderConfig reads its Secret from the operator's own namespace, as cert-manager's ClusterIssuer does, so creating one never grants access to Secrets elsewhere.
- The key is sent only to the configured endpoint, over HTTPS (plain HTTP is accepted only for loopback test servers). It is never logged or written to status.
- Every request carries `User-Agent: rental-operator/<version> hyperbolic-go/<version>`. There is no other telemetry.
