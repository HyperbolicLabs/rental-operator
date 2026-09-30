/*
Copyright 2026.

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

var _ = Describe("ProviderConfig Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()
		typeNamespacedName := types.NamespacedName{Name: resourceName, Namespace: "default"}

		BeforeEach(func() {
			By("creating a valid ProviderConfig")
			err := k8sClient.Get(ctx, typeNamespacedName, &hypv1alpha1.ProviderConfig{})
			if err != nil && errors.IsNotFound(err) {
				resource := &hypv1alpha1.ProviderConfig{
					ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: "default"},
					Spec: hypv1alpha1.ProviderConfigSpec{
						// Points at a Secret that doesn't exist, so validation fails
						// gracefully and a SecretError condition is recorded.
						APIKeySecretRef: hypv1alpha1.SecretKeyRef{Name: "missing", Key: "api-key"},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &hypv1alpha1.ProviderConfig{}
			if err := k8sClient.Get(ctx, typeNamespacedName, resource); err == nil {
				By("cleaning up the ProviderConfig")
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}
		})

		It("records a not-ready condition when the Secret is missing", func() {
			r := &ProviderConfigReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}

			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			got := &hypv1alpha1.ProviderConfig{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, got)).To(Succeed())
			cond := apimeta.FindStatusCondition(got.Status.Conditions, hypv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("SecretError"))
		})
	})
})
