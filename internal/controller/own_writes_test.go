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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

// asWritten returns an object of kind in namespace with spec exactly as a
// user's YAML spells it, without going through the Go types.
func asWritten(kind, namespace, name string, spec map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
	u.SetAPIVersion(hypv1alpha1.GroupVersion.String())
	u.SetKind(kind)
	u.SetNamespace(namespace)
	u.SetName(name)
	return u
}

var _ = Describe("The operator's own writes", func() {
	ctx := context.Background()

	It("leave a GPURental's spec as the user wrote it", func() {
		acct := newFakeAccount(ctx)
		u := asWritten("GPURental", acct.namespace, "as-written", map[string]any{
			"providerConfigRef": map[string]any{"name": acct.config.Name},
			"gpuType":           "h100",
			"gpuCount":          int64(1),
			"lifetime":          map[string]any{"ttl": "8h"},
		})
		Expect(k8sClient.Create(ctx, u)).To(Succeed())
		key := client.ObjectKeyFromObject(u)
		r := newRentalReconciler()
		reconcileOnce(ctx, r, key) // finalizer
		reconcileOnce(ctx, r, key) // create

		Expect(k8sClient.Get(ctx, key, u)).To(Succeed())
		Expect(u.GetFinalizers()).To(ContainElement(hypv1alpha1.FinalizerTerminate))
		Expect(u.GetGeneration()).To(Equal(int64(1)), "the spec was rewritten")
		ttl, _, _ := unstructured.NestedString(u.Object, "spec", "lifetime", "ttl")
		Expect(ttl).To(Equal("8h"))
	})
})
