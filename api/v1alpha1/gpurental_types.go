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

// GPURentalSpec defines the desired on-demand GPU rental.
// +kubebuilder:validation:XValidation:rule="!oldSelf.terminated || self.terminated",message="spec.terminated is one-way: it cannot be set back to false once true (the rental is gone; create a new GPURental to rent again)"
type GPURentalSpec struct {
	// ProviderConfigRef selects the ProviderConfig (credential + endpoint) in
	// this namespace to use for the rental.
	ProviderConfigRef LocalRef `json:"providerConfigRef"`

	// GPUType is the GPU model to rent.
	// +kubebuilder:validation:Enum=h100;h200;b200;rtx-6000-pro
	GPUType string `json:"gpuType"`

	// GPUCount is the number of GPUs. Values >8 yield a multi-node rental (one
	// rental, several instances) since a node is 8 GPUs.
	// +kubebuilder:validation:Minimum=1
	GPUCount int32 `json:"gpuCount"`

	// Region to rent in. If omitted, the operator auto-selects the cheapest
	// region that has availability for (gpuType, gpuCount) — and, when Ports is
	// set, only from regions that support custom ports.
	// +optional
	Region string `json:"region,omitempty"`

	// NetworkType selects the interconnect fabric. Left unset, the operator
	// picks an available fabric from the marketplace options for the resolved
	// region (so it never fails an availability check by guessing). Set it only
	// to pin a fabric (matters mainly for multi-node clusters).
	// +kubebuilder:validation:Enum=ethernet;infiniband
	// +optional
	NetworkType string `json:"networkType,omitempty"`

	// DisplayName is the human-readable name shown in the Hyperbolic dashboard.
	// It is sent as the rental's `label`. If unset, no label is sent and the
	// platform's server-generated name (status.name) is the identity. Opt-in
	// only — it is never derived from metadata.name and never used for matching.
	// +kubebuilder:validation:MaxLength=40
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// Ports is the list of ports to open directly on the instance's public IP
	// (sent as the API's customPorts). Opened as-is (no NAT remapping). Only
	// supported in forge regions; at most 20, unique, 1-65535. Immutable once
	// the rental exists — changing this on a live rental is rejected with a
	// condition; recreate to change ports.
	// +kubebuilder:validation:MaxItems=20
	// +optional
	Ports []int32 `json:"ports,omitempty"`

	// Terminated permanently releases the rental's GPUs while keeping this
	// object as a read-only record. It is a ONE-WAY switch: once true it cannot
	// be set back to false, and the rental is never re-provisioned — create a
	// new GPURental to rent again. (Deleting the object also terminates, via the
	// finalizer, and then removes it.)
	// +kubebuilder:default=false
	// +optional
	Terminated bool `json:"terminated,omitempty"`

	// RebootGeneration reboots all nodes when its value increases. Leave at 0
	// (or unset) for no reboot.
	// +optional
	RebootGeneration int32 `json:"rebootGeneration,omitempty"`

	// MaxPriceCentsPerHour refuses to create the rental if the marketplace
	// price for the resolved option exceeds this ceiling.
	// +optional
	MaxPriceCentsPerHour *int64 `json:"maxPriceCentsPerHour,omitempty"`
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

// GPURentalStatus is the observed state of a GPURental.
type GPURentalStatus struct {
	// Phase mirrors the Hyperbolic rental status
	// (Pending/Running/Rebooting/Terminating/Terminated/Failed/Unknown).
	// +optional
	Phase string `json:"phase,omitempty"`

	// RentalID is the server's string (nanoid) rental id — the stable external
	// handle, used to match this rental when listing.
	// +optional
	RentalID string `json:"rentalId,omitempty"`

	// NumericID is the server's numeric rental id. This is what the
	// terminate/reboot APIs take (their `rentalId` parameter is this number,
	// not the string RentalID above).
	// +optional
	NumericID *int64 `json:"numericId,omitempty"`

	// Name is the server-generated random 3-word rental name (e.g.
	// "conscious-bougainvillea-oryx"). Read-only; surfaced so a human can map
	// this CR to what the dashboard shows.
	// +optional
	Name string `json:"name,omitempty"`

	// Region is the resolved region the rental runs in.
	// +optional
	Region string `json:"region,omitempty"`

	// CostCentsPerHour is the current hourly cost of the rental, in cents.
	// +optional
	CostCentsPerHour *int64 `json:"costCentsPerHour,omitempty"`

	// OpenPorts echoes the custom ports opened on the instance IP.
	// +optional
	OpenPorts []int32 `json:"openPorts,omitempty"`

	// Instances is the per-node SSH/IP/phase view (populated once Running).
	// +optional
	Instances []InstanceStatus `json:"instances,omitempty"`

	// Conditions represent the current state of the rental (Ready, plus
	// reasons such as Provisioning, AvailabilityUnavailable, InsufficientBalance,
	// RegionDisabled, PortsUnsupportedInRegion, PortsImmutable).
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastSyncTime is when the operator last reconciled against the API.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Terminated is a sticky record that this rental was explicitly terminated
	// (via spec.terminated). Once true the CR is a read-only tombstone and is
	// never re-provisioned.
	// +optional
	Terminated bool `json:"terminated,omitempty"`

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
// +kubebuilder:printcolumn:name="Rental-ID",type=string,JSONPath=`.status.rentalId`
// +kubebuilder:printcolumn:name="Rental-Name",type=string,JSONPath=`.status.name`
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

func init() {
	SchemeBuilder.Register(&GPURental{}, &GPURentalList{})
}
