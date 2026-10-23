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
	// APIKeySecretRef points at a Secret in this namespace holding the
	// Hyperbolic API key (an sk_live_... token).
	APIKeySecretRef SecretKeyRef `json:"apiKeySecretRef"`

	// Endpoint is the Hyperbolic API base URL. Defaults to
	// https://api.hyperbolic.ai.
	// +kubebuilder:default=`https://api.hyperbolic.ai`
	// +optional
	Endpoint string `json:"endpoint,omitempty"`
}

// ProviderConfigStatus reflects credential validity and account balance.
type ProviderConfigStatus struct {
	// Conditions represent the current state (Ready, Authenticated).
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

	// LastChecked is when the credential was last validated against the API.
	// +optional
	LastChecked *metav1.Time `json:"lastChecked,omitempty"`

	// ObservedGeneration is the spec generation last processed.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hpc,categories=hyperbolic
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.spec.endpoint`
// +kubebuilder:printcolumn:name="Balance",type=integer,JSONPath=`.status.balanceCents`,description="Balance in cents"
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

func init() {
	SchemeBuilder.Register(&ProviderConfig{}, &ProviderConfigList{})
}
