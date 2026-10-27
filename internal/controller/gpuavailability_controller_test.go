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
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

var _ = Describe("GPUAvailability Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()
		typeNamespacedName := types.NamespacedName{Name: resourceName, Namespace: "default"}

		BeforeEach(func() {
			By("creating a valid GPUAvailability")
			err := k8sClient.Get(ctx, typeNamespacedName, &hypv1alpha1.GPUAvailability{})
			if err != nil && errors.IsNotFound(err) {
				resource := &hypv1alpha1.GPUAvailability{
					ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: "default"},
					Spec: hypv1alpha1.GPUAvailabilitySpec{
						// Missing ProviderConfig, so cred resolution fails before any
						// catalogue call and a CredentialError condition is recorded.
						ProviderConfigRef: hypv1alpha1.LocalRef{Name: "missing"},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &hypv1alpha1.GPUAvailability{}
			if err := k8sClient.Get(ctx, typeNamespacedName, resource); err == nil {
				By("cleaning up the GPUAvailability")
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}
		})

		It("records a credential-error condition when the ProviderConfig is missing", func() {
			r := &GPUAvailabilityReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Catalog: NewCatalogCache(0)}

			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			got := &hypv1alpha1.GPUAvailability{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, got)).To(Succeed())
			cond := apimeta.FindStatusCondition(got.Status.Conditions, hypv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal("CredentialError"))
		})
	})
})
