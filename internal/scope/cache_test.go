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

package scope

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

func TestCacheOptions(t *testing.T) {
	labeled := labels.Set{hypv1alpha1.LabelCredentials: "true"}
	secrets := func(t *testing.T, namespaces []string, clusterNS string) (map[string]bool, bool) {
		t.Helper()
		opts := CacheOptions(namespaces, clusterNS)
		for obj, by := range opts.ByObject {
			if _, ok := obj.(*corev1.Secret); !ok {
				continue
			}
			if !by.Label.Matches(labeled) || by.Label.Matches(labels.Set{}) {
				t.Error("the Secret cache is not limited to labeled Secrets")
			}
			ns := map[string]bool{}
			for n := range by.Namespaces {
				ns[n] = true
			}
			return ns, len(opts.DefaultNamespaces) == 0
		}
		t.Fatal("no cache options for Secrets")
		return nil, false
	}

	ns, everywhere := secrets(t, nil, "operator")
	if !everywhere || len(ns) != 0 {
		t.Errorf("cluster-wide: watches %v (everywhere %v), want every namespace", ns, everywhere)
	}
	ns, everywhere = secrets(t, []string{"ml", "research"}, "operator")
	if everywhere || !ns["ml"] || !ns["research"] || !ns["operator"] || len(ns) != 3 {
		t.Errorf("namespaced: Secrets watched in %v, want ml, research and the operator's namespace", ns)
	}
}
