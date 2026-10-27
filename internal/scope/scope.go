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

// Package scope decides what the operator watches: which namespaces, and
// which Secrets its cache may hold.
package scope

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/HyperbolicLabs/rental-operator/internal/account"
)

// CacheOptions configures the manager's cache. It holds only Secrets
// labeled compute.hyperbolic.ai/credentials=true: in a large cluster the
// other Secrets (Helm releases, service account tokens, TLS keys) would
// otherwise all be held in the operator's memory. With watchNamespaces set,
// it watches only those namespaces, which is what lets the namespaced install
// run with a Role per namespace instead of a ClusterRole. Secrets are then
// also watched in clusterResourceNamespace, where ClusterProviderConfig
// Secrets live.
func CacheOptions(watchNamespaces []string, clusterResourceNamespace string) cache.Options {
	secrets := cache.ByObject{Label: account.CredentialsSelector()}
	opts := cache.Options{}
	if len(watchNamespaces) > 0 {
		opts.DefaultNamespaces = make(map[string]cache.Config, len(watchNamespaces))
		secrets.Namespaces = make(map[string]cache.Config, len(watchNamespaces)+1)
		for _, ns := range watchNamespaces {
			opts.DefaultNamespaces[ns] = cache.Config{}
			secrets.Namespaces[ns] = cache.Config{}
		}
		if clusterResourceNamespace != "" {
			secrets.Namespaces[clusterResourceNamespace] = cache.Config{}
		}
	}
	opts.ByObject = map[client.Object]cache.ByObject{&corev1.Secret{}: secrets}
	return opts
}

// ParseNamespaces splits a comma-separated --watch-namespaces value,
// dropping blanks and duplicates. Empty means every namespace.
func ParseNamespaces(v string) []string {
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
