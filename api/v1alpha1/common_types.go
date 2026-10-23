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

// LocalRef is a reference to another object in the same namespace by name.
type LocalRef struct {
	// name of the referenced object.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// SecretKeySelector selects one key of a Secret in the same namespace.
type SecretKeySelector struct {
	// name of the Secret holding the Hyperbolic API key.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// key within the Secret whose value is the API key.
	// +kubebuilder:default=api-key
	// +optional
	Key string `json:"key,omitempty"`
}

// CredentialsSource says where an API key is read from.
// +kubebuilder:validation:Enum=Secret
type CredentialsSource string

// CredentialsSourceSecret reads the API key from a Kubernetes Secret.
const CredentialsSourceSecret CredentialsSource = "Secret"

// Credentials says where the Hyperbolic API key comes from. Only Secrets are
// supported today, and source leaves room for others (for example an external
// secret store) without another breaking change.
type Credentials struct {
	// source of the API key.
	// +kubebuilder:default=Secret
	// +optional
	Source CredentialsSource `json:"source,omitempty"`
	// secretRef selects the Secret key, in this object's namespace, that
	// holds the API key (an sk_live_... token).
	SecretRef SecretKeySelector `json:"secretRef"`
}

// DeletionPolicy decides what happens to the Hyperbolic rental when its
// GPURental is deleted.
// +kubebuilder:validation:Enum=Delete;Orphan
type DeletionPolicy string

const (
	// DeletionPolicyDelete terminates the rental before the object is
	// removed. The object is never removed while the rental may be billing.
	DeletionPolicyDelete DeletionPolicy = "Delete"
	// DeletionPolicyOrphan removes the object and leaves the rental running:
	// to hand a rental to another system, or to delete an object whose
	// credentials are gone (you then terminate the rental yourself).
	DeletionPolicyOrphan DeletionPolicy = "Orphan"
)

// Condition types. Conditions are the contract for automation. Phase fields
// are for display. Every kind reports Ready and Synced.
const (
	// ConditionReady says whether the resource is usable: a rental is
	// running, a config's credentials work, a catalog view is populated. Its
	// reason says why not.
	ConditionReady = "Ready"
	// ConditionSynced says whether the last reconcile could read its
	// credentials and reach the Hyperbolic API. False means a transient or
	// configuration problem, not a statement about the rental itself.
	ConditionSynced = "Synced"
	// ConditionAuthenticated indicates the API key authenticated successfully.
	ConditionAuthenticated = "Authenticated"
)

// Reasons shared by every kind.
const (
	// ReasonReconcileSuccess is Synced=True: the last reconcile reached the API.
	ReasonReconcileSuccess = "ReconcileSuccess"
	// ReasonAPIError is a failed call to the Hyperbolic API (a 5xx, a timeout,
	// rate limiting). The operator retries.
	ReasonAPIError = "APIError"
	// ReasonCredentialError means the API key could not be read: the
	// ProviderConfig or its Secret is missing, or the Secret has no such key.
	ReasonCredentialError = "CredentialError"
)

// Finalizers, labels and annotations owned by the operator.
const (
	// LabelCredentials must be "true" on Secrets that hold Hyperbolic API
	// keys. The operator only reads and caches Secrets with this label, so it
	// never holds the cluster's other Secrets in memory.
	LabelCredentials = "compute.hyperbolic.ai/credentials"

	// FinalizerTerminate holds a GPURental until its rental is terminated.
	FinalizerTerminate = "compute.hyperbolic.ai/terminate"

	// FinalizerInUse holds a ProviderConfig while GPURentals that may still
	// be billing use it, so credentials cannot disappear before the rentals
	// they pay for.
	FinalizerInUse = "compute.hyperbolic.ai/in-use"

	// AnnotationRentalID records the rental's id as soon as a create
	// succeeds, before status is written, so the id survives a failed status
	// update.
	AnnotationRentalID = "compute.hyperbolic.ai/rental-id"

	// AnnotationRebootedGeneration records the spec.rebootGeneration that was
	// last applied, so a reboot is not repeated.
	AnnotationRebootedGeneration = "compute.hyperbolic.ai/rebooted-generation"

	// AnnotationCreatePending is written, with a timestamp, before a create is
	// sent. If it is present without a rental id, the outcome of that create
	// is unknown and the operator looks the rental up by label before it
	// creates anything again.
	AnnotationCreatePending = "compute.hyperbolic.ai/create-pending"
)
