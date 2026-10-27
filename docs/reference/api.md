# API Reference

## Packages
- [compute.hyperbolic.ai/v1alpha1](#computehyperbolicaiv1alpha1)


## compute.hyperbolic.ai/v1alpha1

Package v1alpha1 contains API Schema definitions for the compute.hyperbolic.ai v1alpha1 API group.

### Resource Types
- [ClusterProviderConfig](#clusterproviderconfig)
- [GPUAvailability](#gpuavailability)
- [GPURental](#gpurental)
- [ProviderConfig](#providerconfig)



#### ClusterProviderConfig



ClusterProviderConfig connects many namespaces to one Hyperbolic account,
for platform teams that manage one API key centrally. Like cert-manager's
ClusterIssuer, its credentials Secret lives in the operator's own namespace,
so creating one never grants access to Secrets anywhere else.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `compute.hyperbolic.ai/v1alpha1` | | |
| `kind` _string_ | `ClusterProviderConfig` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.35/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[ClusterProviderConfigSpec](#clusterproviderconfigspec)_ | spec is the credential, endpoint and the namespaces allowed to use it. |  | Required: \{\} <br /> |
| `status` _[ProviderConfigStatus](#providerconfigstatus)_ | status is the observed credential/account state. |  | Optional: \{\} <br /> |


#### ClusterProviderConfigSpec



ClusterProviderConfigSpec is a ProviderConfigSpec shared by several
namespaces.



_Appears in:_
- [ClusterProviderConfig](#clusterproviderconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `credentials` _[Credentials](#credentials)_ | Credentials says where the Hyperbolic API key comes from. |  |  |
| `endpoint` _string_ | Endpoint is the Hyperbolic API base URL. Defaults to<br />https://api.hyperbolic.ai. It must use https. Plain http is accepted<br />only for loopback test servers, so an API key never crosses a network<br />unencrypted. | https://api.hyperbolic.ai | MaxLength: 2048 <br />Optional: \{\} <br /> |
| `allowedNamespaces` _[LabelSelector](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.35/#labelselector-v1-meta)_ | AllowedNamespaces selects, by namespace labels, the namespaces whose<br />GPURentals and GPUAvailabilities may use this config. Omit it to allow<br />every namespace. Select namespaces by name with the<br />kubernetes.io/metadata.name label. |  | Optional: \{\} <br /> |


#### Credentials



Credentials says where the Hyperbolic API key comes from. Only Secrets are
supported today, and source leaves room for others (for example an external
secret store) without another breaking change.



_Appears in:_
- [ClusterProviderConfigSpec](#clusterproviderconfigspec)
- [ProviderConfigSpec](#providerconfigspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `source` _[CredentialsSource](#credentialssource)_ | source of the API key. | Secret | Enum: [Secret] <br />Optional: \{\} <br /> |
| `secretRef` _[SecretKeySelector](#secretkeyselector)_ | secretRef selects the Secret key, in this object's namespace, that<br />holds the API key (an sk_live_... token). |  |  |


#### CredentialsSource

_Underlying type:_ _string_

CredentialsSource says where an API key is read from.

_Validation:_
- Enum: [Secret]

_Appears in:_
- [Credentials](#credentials)

| Field | Description |
| --- | --- |
| `Secret` |  |


#### DeletionPolicy

_Underlying type:_ _string_

DeletionPolicy decides what happens to the Hyperbolic rental when its
GPURental is deleted.

_Validation:_
- Enum: [Delete Orphan]

_Appears in:_
- [GPURentalSpec](#gpurentalspec)

| Field | Description |
| --- | --- |
| `Delete` | DeletionPolicyDelete terminates the rental before the object is<br />removed. The object is never removed while the rental may be billing.<br /> |
| `Orphan` | DeletionPolicyOrphan removes the object and leaves the rental running:<br />to hand a rental to another system, or to delete an object whose<br />credentials are gone (you then terminate the rental yourself).<br /> |


#### GPUAvailability



GPUAvailability is a filtered view of the Hyperbolic GPU marketplace.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `compute.hyperbolic.ai/v1alpha1` | | |
| `kind` _string_ | `GPUAvailability` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.35/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[GPUAvailabilitySpec](#gpuavailabilityspec)_ | spec is the catalogue filter. |  | Required: \{\} <br /> |
| `status` _[GPUAvailabilityStatus](#gpuavailabilitystatus)_ | status is the filtered catalogue view. |  | Optional: \{\} <br /> |


#### GPUAvailabilitySpec



GPUAvailabilitySpec is a filter over the Hyperbolic marketplace catalogue.
The catalogue is personalized to the account (the endpoint tailors results to
the API key), so a credential is required — this resource shows what *your*
account can rent. All filter fields are optional; with none set it lists every
virtual-machine offering available to the account.



_Appears in:_
- [GPUAvailability](#gpuavailability)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `providerConfigRef` _[ProviderConfigReference](#providerconfigreference)_ | ProviderConfigRef selects the account whose catalogue to show: a<br />ProviderConfig in this namespace (the default kind) or a<br />ClusterProviderConfig. |  |  |
| `gpuType` _string_ | GPUType filters to one model. |  | MaxLength: 63 <br />Pattern: `^[a-z0-9][a-z0-9-]*$` <br />Optional: \{\} <br /> |
| `gpuCount` _integer_ | GPUCount filters to offerings with exactly this many GPUs. |  | Minimum: 1 <br />Optional: \{\} <br /> |
| `region` _string_ | Region filters to one region. |  | Optional: \{\} <br /> |
| `networkType` _string_ | NetworkType filters to one interconnect fabric. |  | Enum: [ethernet infiniband] <br />Optional: \{\} <br /> |
| `maxPriceCentsPerHour` _integer_ | MaxPriceCentsPerHour keeps only offerings at or below this hourly price. |  | Minimum: 1 <br />Optional: \{\} <br /> |
| `onlyAvailable` _boolean_ | OnlyAvailable keeps only offerings that are enabled and have known stock<br />greater than zero (offerings with unknown stock are excluded when true). | false | Optional: \{\} <br /> |


#### GPUAvailabilityStatus



GPUAvailabilityStatus is the filtered catalogue view.



_Appears in:_
- [GPUAvailability](#gpuavailability)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `offerings` _[Offering](#offering) array_ | Offerings matching the spec filter, cheapest first. |  | Optional: \{\} <br /> |
| `offeringCount` _integer_ | OfferingCount is len(offerings). |  | Optional: \{\} <br /> |
| `lastRefreshTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.35/#time-v1-meta)_ | LastRefreshTime is when the shared catalogue cache was last refreshed. |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.35/#condition-v1-meta) array_ | Conditions are Ready (offerings are populated) and Synced (the last<br />catalog fetch succeeded, and False means the offerings may be stale). |  | Optional: \{\} <br /> |
| `observedGeneration` _integer_ | ObservedGeneration is the spec generation last processed. |  | Optional: \{\} <br /> |


#### GPURental



GPURental is the Schema for the gpurentals API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `compute.hyperbolic.ai/v1alpha1` | | |
| `kind` _string_ | `GPURental` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.35/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[GPURentalSpec](#gpurentalspec)_ | spec defines the desired rental. |  | Required: \{\} <br /> |
| `status` _[GPURentalStatus](#gpurentalstatus)_ | status is the observed state of the rental. |  | Optional: \{\} <br /> |


#### GPURentalSpec



GPURentalSpec defines the desired on-demand GPU rental. The rental's shape
(GPU type and count, region, network type, ports, display name) and its
providerConfigRef cannot change after the object is created: rentals
cannot be resized or moved to another account in place, and a rejected
edit is clearer than one that is silently ignored. Delete the object and
create a new one instead.



_Appears in:_
- [GPURental](#gpurental)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `providerConfigRef` _[ProviderConfigReference](#providerconfigreference)_ | ProviderConfigRef selects the Hyperbolic account (credential + endpoint)<br />to rent with: a ProviderConfig in this namespace (the default kind) or a<br />ClusterProviderConfig. |  |  |
| `gpuType` _string_ | GPUType is the GPU model to rent, as the catalog names it (for example<br />h100, h200, b200 or rtx-6000-pro). It is checked against the account's<br />catalog when the rental is created, so new models work without an<br />operator upgrade. An unknown type leaves the rental Blocked with reason<br />AvailabilityUnavailable. |  | MaxLength: 63 <br />Pattern: `^[a-z0-9][a-z0-9-]*$` <br /> |
| `gpuCount` _integer_ | GPUCount is the number of GPUs. Values >8 yield a multi-node rental (one<br />rental, several instances) since a node is 8 GPUs. |  | Minimum: 1 <br /> |
| `region` _string_ | Region to rent in. If omitted, the operator auto-selects the cheapest<br />region that has availability for (gpuType, gpuCount) — and, when Ports is<br />set, only from regions that support custom ports. |  | MaxLength: 63 <br />Optional: \{\} <br /> |
| `networkType` _string_ | NetworkType selects the interconnect fabric. Left unset, the operator<br />picks an available fabric from the marketplace options for the resolved<br />region (so it never fails an availability check by guessing). Set it only<br />to pin a fabric (matters mainly for multi-node clusters). |  | Enum: [ethernet infiniband] <br />Optional: \{\} <br /> |
| `displayName` _string_ | DisplayName is the human-readable name shown in the Hyperbolic dashboard,<br />sent as the rental's label. If unset, the operator uses<br />"<metadata.name>-<first 6 characters of the UID>". The operator uses the<br />label to find its rental if a create's outcome is unknown, so it should<br />be unique within the account. |  | MaxLength: 40 <br />Optional: \{\} <br /> |
| `ports` _integer array_ | Ports is the list of ports to open directly on the instance's public IP<br />(sent as the API's customPorts). Opened as-is (no NAT remapping). Only<br />supported in forge regions, with at most 20 unique ports in 1-65535. Ports cannot be<br />changed after creation. Recreate the GPURental to change them. |  | MaxItems: 20 <br />items:Maximum: 65535 <br />items:Minimum: 1 <br />Optional: \{\} <br /> |
| `terminated` _boolean_ | Terminated permanently releases the rental's GPUs while keeping this<br />object as a read-only record. It is a ONE-WAY switch: once true it cannot<br />be set back to false, and the rental is never re-provisioned — create a<br />new GPURental to rent again. (Deleting the object also terminates,<br />unless deletionPolicy is Orphan, and then removes it.) | false | Optional: \{\} <br /> |
| `deletionPolicy` _[DeletionPolicy](#deletionpolicy)_ | DeletionPolicy decides what deleting this object does to the rental.<br />Delete (the default) terminates it first, and the object is not removed<br />until the rental is confirmed gone. Orphan removes the object and leaves<br />the rental running. | Delete | Enum: [Delete Orphan] <br />Optional: \{\} <br /> |
| `rebootGeneration` _integer_ | RebootGeneration reboots all nodes when its value increases. Leave at 0<br />(or unset) for no reboot. |  | Optional: \{\} <br /> |
| `maxPriceCentsPerHour` _integer_ | MaxPriceCentsPerHour refuses to create the rental if the marketplace<br />price for the resolved option exceeds this ceiling. It can be changed at<br />any time, for example to unblock a rental waiting for a cheaper offer. |  | Minimum: 1 <br />Optional: \{\} <br /> |


#### GPURentalStatus



GPURentalStatus is the observed state of a GPURental.



_Appears in:_
- [GPURental](#gpurental)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `phase` _string_ | Phase summarizes the rental for display: Blocked, Provisioning,<br />Running, Rebooting, Terminating, Terminated, Failed or Unknown.<br />Automation should read the Ready and Synced conditions instead. |  | Optional: \{\} <br /> |
| `rentalId` _string_ | RentalID is the server's rental id: the handle terminate and reboot<br />take, and how this rental is found when listing. |  | Optional: \{\} <br /> |
| `name` _string_ | Name is the server-generated random 3-word rental name (e.g.<br />"conscious-bougainvillea-oryx"). Read-only; surfaced so a human can map<br />this CR to what the dashboard shows. |  | Optional: \{\} <br /> |
| `label` _string_ | Label is the rental's label (the name shown in the dashboard), as the<br />API reports it. |  | Optional: \{\} <br /> |
| `region` _string_ | Region is the resolved region the rental runs in. |  | Optional: \{\} <br /> |
| `networkType` _string_ | NetworkType is the rental's interconnect fabric (ethernet or infiniband). |  | Optional: \{\} <br /> |
| `costCentsPerHour` _integer_ | CostCentsPerHour is the current hourly cost of the rental, in cents. |  | Optional: \{\} <br /> |
| `openPorts` _integer array_ | OpenPorts echoes the custom ports opened on the instance IP. |  | Optional: \{\} <br /> |
| `instances` _[InstanceStatus](#instancestatus) array_ | Instances is the per-node SSH/IP/phase view (populated once Running). |  | Optional: \{\} <br /> |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.35/#condition-v1-meta) array_ | Conditions are Ready (is the rental running, the reason says why not)<br />and Synced (could the last reconcile reach the Hyperbolic API). |  | Optional: \{\} <br /> |
| `lastSyncTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.35/#time-v1-meta)_ | LastSyncTime is when the operator last reconciled against the API. |  | Optional: \{\} <br /> |
| `terminatedAt` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.35/#time-v1-meta)_ | TerminatedAt is when the rental was observed terminated, by the<br />operator (spec.terminated) or externally. Once set, this object is a<br />read-only record and is never provisioned again. Create a new GPURental<br />to rent again. |  | Optional: \{\} <br /> |
| `observedGeneration` _integer_ | ObservedGeneration is the spec generation last processed. |  | Optional: \{\} <br /> |


#### InstanceStatus



InstanceStatus is the per-node view of a (possibly multi-node) rental.



_Appears in:_
- [GPURentalStatus](#gpurentalstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `instanceRef` _string_ | InstanceRef uniquely identifies a node; it is the reboot target. |  |  |
| `phase` _string_ | Phase is the raw provider phase for this node (e.g. Running, Rebooting). |  | Optional: \{\} <br /> |
| `ssh` _string_ | SSH is the connection string, "user@host:port". |  | Optional: \{\} <br /> |
| `internalIp` _string_ | InternalIP is the node's private-network address. |  | Optional: \{\} <br /> |


#### Offering



Offering is a single matched marketplace option.



_Appears in:_
- [GPUAvailabilityStatus](#gpuavailabilitystatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `gpuType` _string_ |  |  |  |
| `gpuCount` _integer_ |  |  |  |
| `region` _string_ |  |  |  |
| `networkType` _string_ | NetworkType is the interconnect fabric (ethernet\|infiniband). |  |  |
| `ethernetVariant` _string_ | EthernetVariant is normal\|rocev2 for ethernet offerings, when known. |  |  |
| `costCentsPerHour` _integer_ | CostCentsPerHour is the hourly price in cents. |  |  |
| `available` _integer_ | Available is the known in-stock count. Nil means the provider did not<br />report stock (unknown), which is distinct from zero. |  | Optional: \{\} <br /> |
| `enabled` _boolean_ | Enabled is whether the offering is currently orderable. |  |  |


#### ProviderConfig



ProviderConfig is the Schema for the providerconfigs API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `compute.hyperbolic.ai/v1alpha1` | | |
| `kind` _string_ | `ProviderConfig` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.35/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  | Optional: \{\} <br /> |
| `spec` _[ProviderConfigSpec](#providerconfigspec)_ | spec is the credential and endpoint. |  | Required: \{\} <br /> |
| `status` _[ProviderConfigStatus](#providerconfigstatus)_ | status is the observed credential/account state. |  | Optional: \{\} <br /> |


#### ProviderConfigReference



ProviderConfigReference selects the Hyperbolic account (credentials and
endpoint) a resource uses.



_Appears in:_
- [GPUAvailabilitySpec](#gpuavailabilityspec)
- [GPURentalSpec](#gpurentalspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `kind` _string_ | kind is ProviderConfig (in the resource's own namespace, the default)<br />or ClusterProviderConfig. | ProviderConfig | Enum: [ProviderConfig ClusterProviderConfig] <br />Optional: \{\} <br /> |
| `name` _string_ | name of the referenced config. |  | MaxLength: 253 <br />MinLength: 1 <br /> |


#### ProviderConfigSpec



ProviderConfigSpec holds the Hyperbolic credential and endpoint.



_Appears in:_
- [ClusterProviderConfigSpec](#clusterproviderconfigspec)
- [ProviderConfig](#providerconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `credentials` _[Credentials](#credentials)_ | Credentials says where the Hyperbolic API key comes from. |  |  |
| `endpoint` _string_ | Endpoint is the Hyperbolic API base URL. Defaults to<br />https://api.hyperbolic.ai. It must use https. Plain http is accepted<br />only for loopback test servers, so an API key never crosses a network<br />unencrypted. | https://api.hyperbolic.ai | MaxLength: 2048 <br />Optional: \{\} <br /> |


#### ProviderConfigStatus



ProviderConfigStatus reflects credential validity and account balance.



_Appears in:_
- [ClusterProviderConfig](#clusterproviderconfig)
- [ProviderConfig](#providerconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.35/#condition-v1-meta) array_ | Conditions are Ready and Authenticated (the API key works) and Synced<br />(the last reconcile could read the key and reach the API). |  | Optional: \{\} <br /> |
| `balanceCents` _integer_ | BalanceCents is the account balance in cents, from the last check. |  | Optional: \{\} <br /> |
| `maxOverdraftCents` _integer_ | MaxOverdraftCents is the account's overdraft allowance in cents. |  | Optional: \{\} <br /> |
| `activeRentals` _integer_ | ActiveRentals is how many rentals the account has active, including<br />rentals started outside this cluster, as Hyperbolic reports it. |  | Optional: \{\} <br /> |
| `costCentsPerHour` _integer_ | CostCentsPerHour is the combined hourly cost of the account's active<br />rentals in cents, as Hyperbolic reports it. |  | Optional: \{\} <br /> |
| `lastChecked` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.35/#time-v1-meta)_ | LastChecked is when the credential, balance and active rentals were last<br />read from the API. |  | Optional: \{\} <br /> |
| `managedRentals` _integer_ | ManagedRentals is how many GPURentals use this config and have not<br />finished (their rental is not known to be terminated). While it is above<br />zero, deleting the config waits. |  | Optional: \{\} <br /> |
| `observedGeneration` _integer_ | ObservedGeneration is the spec generation last processed. |  | Optional: \{\} <br /> |


#### SecretKeySelector



SecretKeySelector selects one key of a Secret in the same namespace.



_Appears in:_
- [Credentials](#credentials)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | name of the Secret holding the Hyperbolic API key. |  | MinLength: 1 <br /> |
| `key` _string_ | key within the Secret whose value is the API key. | api-key | Optional: \{\} <br /> |


