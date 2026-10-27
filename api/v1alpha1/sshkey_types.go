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

// SSHKeySpec is an SSH public key to save on a Hyperbolic account, so
// GPURentals can authorize it.
// +kubebuilder:validation:XValidation:rule="self.providerConfigRef == oldSelf.providerConfigRef",message="providerConfigRef is immutable. Create a new SSHKey instead"
type SSHKeySpec struct {
	// ProviderConfigRef selects the account to save the key on: a
	// ProviderConfig in this namespace (the default kind) or a
	// ClusterProviderConfig.
	ProviderConfigRef ProviderConfigReference `json:"providerConfigRef"`

	// PublicKey is the key in OpenSSH authorized_keys format, such as the
	// contents of ~/.ssh/id_ed25519.pub: its type, the base64 key and an
	// optional comment. The account holds each key once, whatever its
	// comment. It cannot change. Create a new SSHKey for another key.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=16384
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z0-9@.-]+ [A-Za-z0-9+/]+={0,3}( [^\\n]*)?\\n?$')",message="publicKey must be one OpenSSH public key line, such as the contents of ~/.ssh/id_ed25519.pub"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="publicKey is immutable. Create a new SSHKey instead"
	PublicKey string `json:"publicKey"`

	// DeletionPolicy decides what deleting this object does to the saved
	// key. Delete (the default) deletes it from the account if the operator
	// saved it, once no live rental uses it. Orphan leaves it saved. A key
	// the account already had is never deleted.
	// +kubebuilder:default=Delete
	// +optional
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`
}

// Reasons on an SSHKey's Ready condition, besides the shared ones. The set
// is part of the API: reasons are added over time but not renamed.
const (
	// ReasonSaved means the operator saved the key on the account. Deleting
	// the SSHKey deletes it, unless deletionPolicy is Orphan.
	ReasonSaved = "Saved"
	// ReasonAdopted means the account already had the key. The operator
	// uses it and never deletes it.
	ReasonAdopted = "Adopted"
	// ReasonKeyRejected means the API refused to save the key.
	ReasonKeyRejected = "KeyRejected"
	// ReasonKeyInUse means deleting the key waits until no live rental
	// uses it.
	ReasonKeyInUse = "KeyInUse"
)

// SSHKeyStatus is the observed state of an SSHKey.
type SSHKeyStatus struct {
	// ID is the saved key's id on the account.
	// +optional
	ID int64 `json:"id,omitempty"`

	// Conditions are Ready (the key is saved and GPURentals can use it, and
	// the reason says whether the operator saved it or adopted it) and
	// Synced (could the last reconcile reach the Hyperbolic API).
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
// +kubebuilder:resource:categories=hyperbolic
// +kubebuilder:printcolumn:name="ID",type=integer,JSONPath=`.status.id`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// SSHKey saves an SSH public key on a Hyperbolic account. GPURentals list
// it in spec.sshKeyRefs to authorize it on their rentals. Saving a key makes
// it the account's newest, which the API authorizes by default on rentals
// created without naming keys, including teammates' rentals created in the
// dashboard.
type SSHKey struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec is the key to save and the account to save it on.
	// +required
	Spec SSHKeySpec `json:"spec"`

	// status is the observed state of the saved key.
	// +optional
	Status SSHKeyStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// SSHKeyList contains a list of SSHKey.
type SSHKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SSHKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SSHKey{}, &SSHKeyList{})
}
