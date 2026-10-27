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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/HyperbolicLabs/rental-operator/internal/account"
)

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// dependency is one field a reconciler needs, and whether it is missing.
type dependency struct {
	name    string
	missing bool
}

// checkDependencies names the dependencies a reconciler was built without,
// so a wiring mistake fails at startup instead of on the first reconcile.
func checkDependencies(reconciler string, deps ...dependency) error {
	var missing []string
	for _, d := range deps {
		if d.missing {
			missing = append(missing, d.name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%s reconciler is missing %s", reconciler, strings.Join(missing, ", "))
}

// apiDependencies are what every reconciler needs: the Kubernetes client
// and scheme, and the factory for Hyperbolic API clients.
func apiDependencies(c client.Client, scheme *runtime.Scheme, newClient account.ClientFactory) []dependency {
	return []dependency{
		{"Client", c == nil},
		{"Scheme", scheme == nil},
		{"NewClient", newClient == nil},
	}
}

// int32sToInts and intsToInt32s convert port lists between the API types
// (int) and the CRD types (int32).
func int32sToInts(in []int32) []int {
	out := make([]int, len(in))
	for i, v := range in {
		out[i] = int(v)
	}
	return out
}

func intsToInt32s(in []int) []int32 {
	if len(in) == 0 {
		return nil
	}
	out := make([]int32, len(in))
	for i, v := range in {
		out[i] = int32(v)
	}
	return out
}
