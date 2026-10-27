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
	"context"
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/account"
)

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// patchAnnotations sets and removes annotations on obj with a merge patch,
// and sends nothing when they are already as asked.
func patchAnnotations(ctx context.Context, c client.Client, obj client.Object, set map[string]string, remove ...string) error {
	annotations := obj.GetAnnotations()
	changed := false
	for k, v := range set {
		if annotations[k] != v {
			changed = true
		}
	}
	for _, k := range remove {
		if _, ok := annotations[k]; ok {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	base := obj.DeepCopyObject().(client.Object)
	if annotations == nil {
		annotations = map[string]string{}
	}
	for k, v := range set {
		annotations[k] = v
	}
	for _, k := range remove {
		delete(annotations, k)
	}
	obj.SetAnnotations(annotations)
	return c.Patch(ctx, obj, client.MergeFrom(base))
}

// credentialErrorReason is the reason to report when a resource's
// credentials cannot be resolved.
func credentialErrorReason(err error) string {
	if errors.Is(err, account.ErrNamespaceNotAllowed) {
		return hypv1alpha1.ReasonNamespaceNotAllowed
	}
	return hypv1alpha1.ReasonCredentialError
}

// configKey identifies the config that a resource in namespace refers to,
// for account.CredentialsMemory.
func configKey(namespace string, ref hypv1alpha1.ProviderConfigReference) string {
	if ref.IsCluster() {
		return hypv1alpha1.ClusterProviderConfigKind + "/" + ref.Name
	}
	return namespace + "/" + ref.Name
}

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

// createDeleteDependencies are what a reconciler that creates and deletes
// things on the account needs besides apiDependencies: the remembered
// credentials that let it delete after a Secret is gone, a clock for
// create-pending, and a recorder for events.
func createDeleteDependencies(credentials *account.CredentialsMemory, clk clock.PassiveClock, recorder record.EventRecorder) []dependency {
	return []dependency{
		{"Credentials", credentials == nil},
		{"Clock", clk == nil},
		{"Recorder", recorder == nil},
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
