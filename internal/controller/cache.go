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
	"strings"

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
// otherwise all be held in the operator's memory. With watchNamespaces set,
// it watches only those namespaces, which is what lets the namespaced install
// run with a Role per namespace instead of a ClusterRole.
func ManagerCacheOptions(watchNamespaces []string) cache.Options {
	opts := cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Secret{}: {Label: labels.SelectorFromSet(labels.Set{hypv1alpha1.LabelCredentials: credentialsLabelValue})},
		},
	}
	if len(watchNamespaces) > 0 {
		opts.DefaultNamespaces = make(map[string]cache.Config, len(watchNamespaces))
		for _, ns := range watchNamespaces {
			opts.DefaultNamespaces[ns] = cache.Config{}
		}
	}
	return opts
}

// ParseWatchNamespaces splits a comma-separated --watch-namespaces value,
// dropping blanks and duplicates. Empty means every namespace.
func ParseWatchNamespaces(v string) []string {
	var out []string
	seen := map[string]bool{}
	for _, ns := range strings.Split(v, ",") {
		ns = strings.TrimSpace(ns)
		if ns != "" && !seen[ns] {
			seen[ns] = true
			out = append(out, ns)
		}
	}
	return out
}
