/*
Copyright 2026 Hyperbolic Labs, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GPURentalSpec defines the desired on-demand GPU rental. The rental's shape
// (GPU type and count, region, network type, ports, display name) and its
// providerConfigRef cannot change after the object is created: rentals
// cannot be resized or moved to another account in place, and a rejected
// edit is clearer than one that is silently ignored. Delete the object and
// create a new one instead.
// +kubebuilder:validation:XValidation:rule="!oldSelf.terminated || self.terminated",message="spec.terminated is one-way: it cannot be set back to false once true (the rental is gone; create a new GPURental to rent again)"
// +kubebuilder:validation:XValidation:rule="self.gpuType == oldSelf.gpuType && self.gpuCount == oldSelf.gpuCount",message="gpuType and gpuCount are immutable. Create a new GPURental instead"
// +kubebuilder:validation:XValidation:rule="has(self.region) == has(oldSelf.region) && (!has(self.region) || self.region == oldSelf.region)",message="region is immutable. Create a new GPURental instead"
// +kubebuilder:validation:XValidation:rule="has(self.networkType) == has(oldSelf.networkType) && (!has(self.networkType) || self.networkType == oldSelf.networkType)",message="networkType is immutable. Create a new GPURental instead"
// +kubebuilder:validation:XValidation:rule="has(self.ports) == has(oldSelf.ports) && (!has(self.ports) || self.ports == oldSelf.ports)",message="ports are immutable. Create a new GPURental instead"
// +kubebuilder:validation:XValidation:rule="has(self.displayName) == has(oldSelf.displayName) && (!has(self.displayName) || self.displayName == oldSelf.displayName)",message="displayName is immutable. Create a new GPURental instead"
// +kubebuilder:validation:XValidation:rule="self.providerConfigRef == oldSelf.providerConfigRef",message="providerConfigRef is immutable. Create a new GPURental instead"
type GPURentalSpec struct {
	// ProviderConfigRef selects the Hyperbolic account (credential + endpoint)
	// to rent with: a ProviderConfig in this namespace (the default kind) or a
	// ClusterProviderConfig.
	ProviderConfigRef ProviderConfigReference `json:"providerConfigRef"`

	// GPUType is the GPU model to rent, as the catalog names it (for example
	// h100, h200, b200 or rtx-6000-pro). It is checked against the account's
	// catalog when the rental is created, so new models work without an
	// operator upgrade. An unknown type leaves the rental Blocked with reason
	// AvailabilityUnavailable.
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9-]*$`
	// +kubebuilder:validation:MaxLength=63
	GPUType string `json:"gpuType"`

	// GPUCount is the number of GPUs. Values >8 yield a multi-node rental (one
	// rental, several instances) since a node is 8 GPUs.
	// +kubebuilder:validation:Minimum=1
	GPUCount int32 `json:"gpuCount"`

	// Region to rent in. If omitted, the operator auto-selects the cheapest
	// region that has availability for (gpuType, gpuCount) — and, when Ports is
	// set, only from regions that support custom ports.
	// +kubebuilder:validation:MaxLength=63
	// +optional
	Region string `json:"region,omitempty"`

	// NetworkType selects the interconnect fabric. Left unset, the operator
	// picks an available fabric from the marketplace options for the resolved
	// region (so it never fails an availability check by guessing). Set it only
	// to pin a fabric (matters mainly for multi-node clusters).
	// +kubebuilder:validation:Enum=ethernet;infiniband
	// +optional
	NetworkType string `json:"networkType,omitempty"`

	// DisplayName is the human-readable name shown in the Hyperbolic dashboard,
	// sent as the rental's label. If unset, the operator uses
	// "<metadata.name>-<first 6 characters of the UID>". The operator uses the
	// label to find its rental if a create's outcome is unknown, so it should
	// be unique within the account.
	// +kubebuilder:validation:MaxLength=40
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// Ports is the list of ports to open directly on the instance's public IP
	// (sent as the API's customPorts). Opened as-is (no NAT remapping). Only
	// supported in forge regions, with at most 20 unique ports in 1-65535. Ports cannot be
	// changed after creation. Recreate the GPURental to change them.
	// +kubebuilder:validation:MaxItems=20
	// +kubebuilder:validation:items:Minimum=1
	// +kubebuilder:validation:items:Maximum=65535
	// +listType=set
	// +optional
	Ports []int32 `json:"ports,omitempty"`

	// Terminated permanently releases the rental's GPUs while keeping this
	// object as a read-only record. It is a ONE-WAY switch: once true it cannot
	// be set back to false, and the rental is never re-provisioned — create a
	// new GPURental to rent again. (Deleting the object also terminates,
	// unless deletionPolicy is Orphan, and then removes it.)
	// +kubebuilder:default=false
	// +optional
	Terminated bool `json:"terminated,omitempty"`

	// DeletionPolicy decides what deleting this object does to the rental.
	// Delete (the default) terminates it first, and the object is not removed
	// until the rental is confirmed gone. Orphan removes the object and leaves
	// the rental running.
	// +kubebuilder:default=Delete
	// +optional
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`

	// RebootGeneration reboots all nodes when its value increases. Leave at 0
	// (or unset) for no reboot.
	// +optional
	RebootGeneration int32 `json:"rebootGeneration,omitempty"`

	// MaxPriceCentsPerHour refuses to create the rental if the marketplace
	// price for the resolved option exceeds this ceiling. It can be changed at
	// any time, for example to unblock a rental waiting for a cheaper offer.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxPriceCentsPerHour *int64 `json:"maxPriceCentsPerHour,omitempty"`

	// SSHKeyRefs names SSHKeys in this namespace to authorize on the rental.
	// The rental is not created until each one is Ready and uses the same
	// providerConfigRef. They can change while the rental runs: added keys
	// are attached and removed ones detached, unless the rental doesn't
	// allow it, which Synced reports with reason SSHKeyChangeRejected. Keys
	// the operator did not attach are left alone. Without sshKeyRefs, the
	// API authorizes the account's most recently saved key.
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=name
	// +optional
	SSHKeyRefs []SSHKeyReference `json:"sshKeyRefs,omitempty"`
}

// SSHKeyReference names an SSHKey in the rental's namespace.
type SSHKeyReference struct {
	// name of the SSHKey.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// InstanceStatus is the per-node view of a (possibly multi-node) rental.
type InstanceStatus struct {
	// InstanceRef uniquely identifies a node; it is the reboot target.
	InstanceRef string `json:"instanceRef"`
	// Phase is the raw provider phase for this node (e.g. Running, Rebooting).
	// +optional
	Phase string `json:"phase,omitempty"`
	// SSH is the connection string, "user@host:port".
	// +optional
	SSH string `json:"ssh,omitempty"`
	// InternalIP is the node's private-network address.
	// +optional
	InternalIP string `json:"internalIp,omitempty"`
}

// Phases of a GPURental, for display. Automation should read conditions.
const (
	// PhaseBlocked means nothing has been rented yet and the rental cannot be
	// created right now. The Ready condition's reason says why.
	PhaseBlocked = "Blocked"
	// PhaseProvisioning means the rental exists and is starting (the API's
	// Pending).
	PhaseProvisioning = "Provisioning"
	PhaseRunning      = "Running"
	PhaseRebooting    = "Rebooting"
	PhaseTerminating  = "Terminating"
	PhaseTerminated   = "Terminated"
	PhaseFailed       = "Failed"
	PhaseUnknown      = "Unknown"
)

// Reasons on a GPURental's Ready condition. The set is part of the API:
// reasons are added over time but not renamed.
const (
	ReasonProvisioning = "Provisioning"
	ReasonRunning      = "Running"
	ReasonRebooting    = "Rebooting"
	ReasonTerminating  = "Terminating"
	// ReasonTerminated means the operator terminated the rental, because
	// spec.terminated was set or the object is being deleted.
	ReasonTerminated = "Terminated"
	// ReasonTerminatedExternally means the rental ended without the operator
	// asking (the balance ran out, it was ended in the dashboard, hardware
	// failed). It is never rented again automatically.
	ReasonTerminatedExternally = "TerminatedExternally"
	ReasonFailed               = "Failed"
	ReasonUnknown              = "Unknown"
	// Reasons a rental is Blocked: nothing was created and nothing is charged.
	ReasonAvailabilityUnavailable  = "AvailabilityUnavailable"
	ReasonInsufficientBalance      = "InsufficientBalance"
	ReasonRegionDisabled           = "RegionDisabled"
	ReasonPortsUnsupportedInRegion = "PortsUnsupportedInRegion"
	ReasonNoSSHKey                 = "NoSSHKey"
	// ReasonSSHKeyNotReady means an SSHKey in spec.sshKeyRefs does not
	// exist, is not saved yet, is being deleted or uses another config. On a
	// running rental it is reported on Synced, and the keys are left as they
	// are.
	ReasonSSHKeyNotReady = "SSHKeyNotReady"
	// ReasonSSHKeyChangeRejected is reported on Synced when the API refuses
	// to attach or detach a key on a running rental. Some rentals don't
	// allow it. The rental keeps the keys it has.
	ReasonSSHKeyChangeRejected = "SSHKeyChangeRejected"
	// ReasonNamespaceNotAllowed means the ClusterProviderConfig does not allow
	// this namespace (its allowedNamespaces selector does not match).
	ReasonNamespaceNotAllowed = "NamespaceNotAllowed"
	// ReasonCreateOutcomeUnknown means a create may have taken effect but the
	// rental could not be identified unambiguously (several rentals carry
	// this object's label). The operator stops and waits for a person rather
	// than risk renting twice.
	ReasonCreateOutcomeUnknown = "CreateOutcomeUnknown"
)

// GPURentalStatus is the observed state of a GPURental.
type GPURentalStatus struct {
	// Phase summarizes the rental for display: Blocked, Provisioning,
	// Running, Rebooting, Terminating, Terminated, Failed or Unknown.
	// Automation should read the Ready and Synced conditions instead.
	// +optional
	Phase string `json:"phase,omitempty"`

	// RentalID is the server's rental id: the handle terminate and reboot
	// take, and how this rental is found when listing.
	// +optional
	RentalID string `json:"rentalId,omitempty"`

	// Name is the server-generated random 3-word rental name (e.g.
	// "conscious-bougainvillea-oryx"). Read-only; surfaced so a human can map
	// this CR to what the dashboard shows.
	// +optional
	Name string `json:"name,omitempty"`

	// Label is the rental's label (the name shown in the dashboard), as the
	// API reports it.
	// +optional
	Label string `json:"label,omitempty"`

	// Region is the resolved region the rental runs in.
	// +optional
	Region string `json:"region,omitempty"`

	// NetworkType is the rental's interconnect fabric (ethernet or infiniband).
	// +optional
	NetworkType string `json:"networkType,omitempty"`

	// CostCentsPerHour is the current hourly cost of the rental, in cents.
	// +optional
	CostCentsPerHour *int64 `json:"costCentsPerHour,omitempty"`

	// OpenPorts echoes the custom ports opened on the instance IP.
	// +optional
	OpenPorts []int32 `json:"openPorts,omitempty"`

	// Instances is the per-node SSH/IP/phase view (populated once Running).
	// +optional
	Instances []InstanceStatus `json:"instances,omitempty"`

	// SSHKeyIDs are the ids of the saved keys the operator authorized on the
	// rental, from spec.sshKeyRefs.
	// +listType=set
	// +optional
	SSHKeyIDs []int64 `json:"sshKeyIds,omitempty"`

	// Conditions are Ready (is the rental running, the reason says why not)
	// and Synced (could the last reconcile reach the Hyperbolic API).
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastSyncTime is when the operator last reconciled against the API.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// TerminatedAt is when the rental was observed terminated, by the
	// operator (spec.terminated) or externally. Once set, this object is a
	// read-only record and is never provisioned again. Create a new GPURental
	// to rent again.
	// +optional
	TerminatedAt *metav1.Time `json:"terminatedAt,omitempty"`

	// ObservedGeneration is the spec generation last processed.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=gpr,categories=hyperbolic
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="GPU",type=string,JSONPath=`.spec.gpuType`
// +kubebuilder:printcolumn:name="Count",type=integer,JSONPath=`.spec.gpuCount`
// +kubebuilder:printcolumn:name="Region",type=string,JSONPath=`.status.region`
// +kubebuilder:printcolumn:name="Cents/Hr",type=integer,JSONPath=`.status.costCentsPerHour`
// +kubebuilder:printcolumn:name="Rental-ID",type=string,JSONPath=`.status.rentalId`
// +kubebuilder:printcolumn:name="Rental-Name",type=string,JSONPath=`.status.name`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GPURental is the Schema for the gpurentals API.
type GPURental struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired rental.
	// +required
	Spec GPURentalSpec `json:"spec"`

	// status is the observed state of the rental.
	// +optional
	Status GPURentalStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// GPURentalList contains a list of GPURental.
type GPURentalList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GPURental `json:"items"`
}
