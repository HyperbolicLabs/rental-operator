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

// ProviderConfigSpec holds the Hyperbolic credential and endpoint.
type ProviderConfigSpec struct {
	// Credentials says where the Hyperbolic API key comes from.
	Credentials Credentials `json:"credentials"`

	// Endpoint is the Hyperbolic API base URL. Defaults to
	// https://api.hyperbolic.ai. It must use https. Plain http is accepted
	// only for loopback test servers, so an API key never crosses a network
	// unencrypted.
	// +kubebuilder:default=`https://api.hyperbolic.ai`
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && (url(self).getScheme() == 'https' || (url(self).getScheme() == 'http' && url(self).getHostname() in ['localhost', '127.0.0.1', '::1']))",message="endpoint must be an https URL. Plain http is allowed only for localhost, 127.0.0.1 or ::1"
	// +optional
	Endpoint string `json:"endpoint,omitempty"`
}

// ProviderConfigStatus reflects credential validity and account balance.
type ProviderConfigStatus struct {
	// Conditions are Ready and Authenticated (the API key works) and Synced
	// (the last reconcile could read the key and reach the API).
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// BalanceCents is the account balance in cents, from the last check.
	// +optional
	BalanceCents *int64 `json:"balanceCents,omitempty"`

	// MaxOverdraftCents is the account's overdraft allowance in cents.
	// +optional
	MaxOverdraftCents *int64 `json:"maxOverdraftCents,omitempty"`

	// ActiveRentals is how many rentals the account has active, including
	// rentals started outside this cluster, as Hyperbolic reports it.
	// +optional
	ActiveRentals *int32 `json:"activeRentals,omitempty"`

	// CostCentsPerHour is the combined hourly cost of the account's active
	// rentals in cents, as Hyperbolic reports it.
	// +optional
	CostCentsPerHour *int64 `json:"costCentsPerHour,omitempty"`

	// LastChecked is when the credential, balance and active rentals were last
	// read from the API.
	// +optional
	LastChecked *metav1.Time `json:"lastChecked,omitempty"`

	// ManagedRentals is how many GPURentals use this config and have not
	// finished (their rental is not known to be terminated). While it is above
	// zero, deleting the config waits.
	// +optional
	ManagedRentals int32 `json:"managedRentals,omitempty"`

	// ObservedGeneration is the spec generation last processed.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hpc,categories=hyperbolic
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.spec.endpoint`
// +kubebuilder:printcolumn:name="Balance",type=integer,JSONPath=`.status.balanceCents`,description="Balance in cents"
// +kubebuilder:printcolumn:name="Cents/Hr",type=integer,JSONPath=`.status.costCentsPerHour`,description="Hourly cost of the account's active rentals in cents"
// +kubebuilder:printcolumn:name="Rentals",type=integer,JSONPath=`.status.managedRentals`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ProviderConfig is the Schema for the providerconfigs API.
type ProviderConfig struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec is the credential and endpoint.
	// +required
	Spec ProviderConfigSpec `json:"spec"`

	// status is the observed credential/account state.
	// +optional
	Status ProviderConfigStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ProviderConfigList contains a list of ProviderConfig.
type ProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProviderConfig `json:"items"`
}

// ClusterProviderConfigSpec is a ProviderConfigSpec shared by several
// namespaces.
type ClusterProviderConfigSpec struct {
	ProviderConfigSpec `json:",inline"`

	// AllowedNamespaces selects, by namespace labels, the namespaces whose
	// GPURentals and GPUAvailabilities may use this config. Omit it to allow
	// every namespace. Select namespaces by name with the
	// kubernetes.io/metadata.name label.
	// +optional
	AllowedNamespaces *metav1.LabelSelector `json:"allowedNamespaces,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=hcpc,categories=hyperbolic
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.spec.endpoint`
// +kubebuilder:printcolumn:name="Balance",type=integer,JSONPath=`.status.balanceCents`,description="Balance in cents"
// +kubebuilder:printcolumn:name="Cents/Hr",type=integer,JSONPath=`.status.costCentsPerHour`,description="Hourly cost of the account's active rentals in cents"
// +kubebuilder:printcolumn:name="Rentals",type=integer,JSONPath=`.status.managedRentals`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ClusterProviderConfig connects many namespaces to one Hyperbolic account,
// for platform teams that manage one API key centrally. Like cert-manager's
// ClusterIssuer, its credentials Secret lives in the operator's own namespace,
// so creating one never grants access to Secrets anywhere else.
type ClusterProviderConfig struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec is the credential, endpoint and the namespaces allowed to use it.
	// +required
	Spec ClusterProviderConfigSpec `json:"spec"`

	// status is the observed credential/account state.
	// +optional
	Status ProviderConfigStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ClusterProviderConfigList contains a list of ClusterProviderConfig.
type ClusterProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterProviderConfig `json:"items"`
}
