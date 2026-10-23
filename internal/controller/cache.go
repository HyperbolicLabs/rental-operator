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

package controller

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

// credentialsLabelValue is the value of LabelCredentials on API key Secrets.
const credentialsLabelValue = "true"

// isCredentialsSecret reports whether s is labeled to hold an API key.
func isCredentialsSecret(s *corev1.Secret) bool {
	return s.Labels[hypv1alpha1.LabelCredentials] == credentialsLabelValue
}

// ManagerCacheOptions configures the manager's cache. It holds only Secrets
// labeled compute.hyperbolic.ai/credentials=true: in a large cluster the
// other Secrets (Helm releases, service account tokens, TLS keys) would
// otherwise all be held in the operator's memory.
func ManagerCacheOptions() cache.Options {
	return cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Secret{}: {Label: labels.SelectorFromSet(labels.Set{hypv1alpha1.LabelCredentials: credentialsLabelValue})},
		},
	}
}
