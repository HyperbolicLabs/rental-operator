# Common operations

Recipes for day-to-day use. Field details are in [Custom resources](resources.md).

- [List everything the operator manages](#list-everything-the-operator-manages)
- [Pick a region and fabric](#pick-a-region-and-fabric)
- [Open custom ports](#open-custom-ports)
- [Cap the hourly price](#cap-the-hourly-price)
- [Terminate but keep the object as a record](#terminate-but-keep-the-object-as-a-record)
- [Delete the object but keep the rental running](#delete-the-object-but-keep-the-rental-running)
- [Reboot all nodes](#reboot-all-nodes)
- [Name it in the dashboard](#name-it-in-the-dashboard)
- [Before you delete the cluster](#before-you-delete-the-cluster)

## List everything the operator manages

Every kind is in the `hyperbolic` category:

```console
kubectl get hyperbolic -A
```

Short names work too: `hpc` (ProviderConfig), `hcpc` (ClusterProviderConfig), `gpr` (GPURental), `gpa` (GPUAvailability).

## Pick a region and fabric

```yaml
spec:
  gpuType: h200
  gpuCount: 16          # multi-node
  region: us-southeast-3
  networkType: infiniband
```

Omit `region` to get the cheapest available one, and `networkType` to let the operator pick an available fabric.

## Open custom ports

Forge regions only:

```yaml
spec:
  gpuType: h100
  gpuCount: 8
  ports: [8080, 8443, 6006]
```

They open directly on the instance's public IP at the same numbers (`status.openPorts`). Ports cannot be changed after creation. To change them, delete and recreate.

## Cap the hourly price

```yaml
spec:
  maxPriceCentsPerHour: 3000   # refuse to create above $30/hr
```

## Terminate but keep the object as a record

```yaml
spec:
  terminated: true    # permanently releases the GPUs; cannot be unset
```

The object becomes read-only. To rent again, create a new GPURental.

## Delete the object but keep the rental running

To hand a rental to another system, or when the API key is gone and the operator cannot terminate it:

```yaml
spec:
  deletionPolicy: Orphan
```

Without it, a GPURental whose credentials can't be read stays in `Terminating` with a `TerminationBlocked` event that explains how to proceed: restore the Secret, or set `deletionPolicy: Orphan` and terminate the rental in the dashboard. If the Secret disappears while the operator is running (as in `kubectl delete namespace`), the operator still terminates with the last key it read for that ProviderConfig.

## Reboot all nodes

Increase `spec.rebootGeneration`:

```console
kubectl patch gpurental train-run-42 --type merge \
  -p "{\"spec\":{\"rebootGeneration\":$(( $(kubectl get gpurental train-run-42 -o jsonpath='{.spec.rebootGeneration}') + 1 ))}}"
```

## Name it in the dashboard

```yaml
spec:
  displayName: team-a-sweep-42
```

The operator uses this label to find its rental if a create's outcome is unknown, so keep it unique within the account. The default, `<metadata.name>-<6 characters of the UID>`, always is.

## Before you delete the cluster

Delete your GPURentals first. `kind delete cluster`, or losing a cluster any other way, removes the operator too, and nothing is left to terminate the rentals. They keep billing until you terminate them in the [dashboard](https://app.hyperbolic.ai).

```console
kubectl delete gpurentals --all -A --wait
```
