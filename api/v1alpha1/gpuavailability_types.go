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

// GPUAvailabilitySpec is a filter over the Hyperbolic marketplace catalogue.
// The catalogue is personalized to the account (the endpoint tailors results to
// the API key), so a credential is required — this resource shows what *your*
// account can rent. All filter fields are optional; with none set it lists every
// virtual-machine offering available to the account.
type GPUAvailabilitySpec struct {
	// ProviderConfigRef selects the account whose catalogue to show: a
	// ProviderConfig in this namespace (the default kind) or a
	// ClusterProviderConfig.
	ProviderConfigRef ProviderConfigReference `json:"providerConfigRef"`

	// GPUType filters to one model.
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9-]*$`
	// +kubebuilder:validation:MaxLength=63
	// +optional
	GPUType string `json:"gpuType,omitempty"`

	// GPUCount filters to offerings with exactly this many GPUs.
	// +kubebuilder:validation:Minimum=1
	// +optional
	GPUCount *int32 `json:"gpuCount,omitempty"`

	// Region filters to one region.
	// +optional
	Region string `json:"region,omitempty"`

	// NetworkType filters to one interconnect fabric.
	// +kubebuilder:validation:Enum=ethernet;infiniband
	// +optional
	NetworkType string `json:"networkType,omitempty"`

	// MaxPriceCentsPerHour keeps only offerings at or below this hourly price.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxPriceCentsPerHour *int64 `json:"maxPriceCentsPerHour,omitempty"`

	// OnlyAvailable keeps only offerings that are enabled and have known stock
	// greater than zero (offerings with unknown stock are excluded when true).
	// +kubebuilder:default=false
	// +optional
	OnlyAvailable bool `json:"onlyAvailable,omitempty"`
}

// Offering is a single matched marketplace option.
type Offering struct {
	GPUType  string `json:"gpuType"`
	GPUCount int32  `json:"gpuCount"`
	Region   string `json:"region"`
	// NetworkType is the interconnect fabric (ethernet|infiniband).
	NetworkType string `json:"networkType,omitempty"`
	// EthernetVariant is normal|rocev2 for ethernet offerings, when known.
	EthernetVariant string `json:"ethernetVariant,omitempty"`
	// CostCentsPerHour is the hourly price in cents.
	CostCentsPerHour int64 `json:"costCentsPerHour"`
	// Available is the known in-stock count. Nil means the provider did not
	// report stock (unknown), which is distinct from zero.
	// +optional
	Available *int32 `json:"available,omitempty"`
	// Enabled is whether the offering is currently orderable.
	Enabled bool `json:"enabled"`
}

// GPUAvailabilityStatus is the filtered catalogue view.
type GPUAvailabilityStatus struct {
	// Offerings matching the spec filter, cheapest first.
	// +optional
	Offerings []Offering `json:"offerings,omitempty"`

	// OfferingCount is len(offerings).
	// +optional
	OfferingCount int32 `json:"offeringCount,omitempty"`

	// LastRefreshTime is when the shared catalogue cache was last refreshed.
	// +optional
	LastRefreshTime *metav1.Time `json:"lastRefreshTime,omitempty"`

	// Conditions are Ready (offerings are populated) and Synced (the last
	// catalog fetch succeeded, and False means the offerings may be stale).
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the spec generation last processed.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=gpa,categories=hyperbolic
// +kubebuilder:printcolumn:name="GPU",type=string,JSONPath=`.spec.gpuType`,description="gpuType filter (blank = any)"
// +kubebuilder:printcolumn:name="Region",type=string,JSONPath=`.spec.region`,description="region filter (blank = any)"
// +kubebuilder:printcolumn:name="Offerings",type=integer,JSONPath=`.status.offeringCount`
// +kubebuilder:printcolumn:name="Cheapest-Cents",type=integer,JSONPath=`.status.offerings[0].costCentsPerHour`,description="Lowest hourly price among matches (cents)"
// +kubebuilder:printcolumn:name="Refreshed",type=date,JSONPath=`.status.lastRefreshTime`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// GPUAvailability is a filtered view of the Hyperbolic GPU marketplace.
type GPUAvailability struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec is the catalogue filter.
	// +required
	Spec GPUAvailabilitySpec `json:"spec"`

	// status is the filtered catalogue view.
	// +optional
	Status GPUAvailabilityStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// GPUAvailabilityList contains a list of GPUAvailability.
type GPUAvailabilityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GPUAvailability `json:"items"`
}
