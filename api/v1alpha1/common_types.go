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

// Condition type strings used across the operator's CRDs.
const (
	// ConditionReady is the top-level readiness condition.
	ConditionReady = "Ready"
	// ConditionAuthenticated indicates the API key authenticated successfully.
	ConditionAuthenticated = "Authenticated"
)

// Finalizers and annotations owned by the operator.
const (
	// FinalizerTerminate holds a GPURental until its rental is terminated.
	FinalizerTerminate = "compute.hyperbolic.ai/terminate"

	// AnnotationRentalID records the rental's numeric id as soon as a create
	// succeeds, before status is written, so the id survives a failed status
	// update.
	AnnotationRentalID = "compute.hyperbolic.ai/rental-id"

	// AnnotationRebootedGeneration records the spec.rebootGeneration that was
	// last applied, so a reboot is not repeated.
	AnnotationRebootedGeneration = "compute.hyperbolic.ai/rebooted-generation"
)
