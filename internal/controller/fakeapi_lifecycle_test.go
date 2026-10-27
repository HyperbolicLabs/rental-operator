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
	"net/http"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	"github.com/HyperbolicLabs/hyperbolic-go/hyperbolictest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

var _ = Describe("Against the fake Hyperbolic API", func() {
	ctx := context.Background()

	It("creates, observes and terminates a GPURental", func() {
		acct := newFakeAccount(ctx)
		r := newRentalReconciler()
		gr := acct.rental("train")
		Expect(k8sClient.Create(ctx, gr)).To(Succeed())
		key := client.ObjectKeyFromObject(gr)

		By("adding the finalizer, then creating the rental")
		reconcileOnce(ctx, r, key)
		reconcileOnce(ctx, r, key)
		Expect(acct.api.Requests(http.MethodPost, hyperbolictest.PathRentals)).To(Equal(1))
		Expect(acct.api.ActiveRentals()).To(HaveLen(1))
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		Expect(gr.Status.RentalID).NotTo(BeEmpty())
		Expect(gr.Status.Phase).To(Equal(hypv1alpha1.PhaseProvisioning))
		Expect(gr.Annotations).To(HaveKeyWithValue(hypv1alpha1.AnnotationRentalID, Not(BeEmpty())))
		id := gr.Status.RentalID

		By("reporting connection details and price once it runs")
		Expect(acct.api.SetRentalStatus(id, hyperbolic.RentalStatusRunning)).To(BeTrue())
		reconcileOnce(ctx, r, key)
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		Expect(gr.Status.Phase).To(Equal(hypv1alpha1.PhaseRunning))
		Expect(gr.Status.Instances).To(HaveLen(1))
		Expect(gr.Status.Instances[0].SSH).NotTo(BeEmpty())
		Expect(gr.Status.CostCentsPerHour).To(HaveValue(Equal(int64(275))))
		Expect(apimeta.IsStatusConditionTrue(gr.Status.Conditions, hypv1alpha1.ConditionReady)).To(BeTrue())

		By("terminating the rental when the object is deleted")
		Expect(k8sClient.Delete(ctx, gr)).To(Succeed())
		reconcileOnce(ctx, r, key)
		Expect(acct.api.ActiveRentals()).To(BeEmpty())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, gr))).To(BeTrue())
	})

	It("validates a ProviderConfig and reports the account as the API does", func() {
		acct := newFakeAccount(ctx)
		acct.api.SetBalance(12_345, 500)
		r := newConfigReconciler()
		key := client.ObjectKey{Namespace: acct.namespace, Name: acct.config.Name}

		reconcileOnce(ctx, r, key)

		pc := &hypv1alpha1.ProviderConfig{}
		Expect(k8sClient.Get(ctx, key, pc)).To(Succeed())
		Expect(apimeta.IsStatusConditionTrue(pc.Status.Conditions, hypv1alpha1.ConditionReady)).To(BeTrue())
		Expect(pc.Status.BalanceCents).To(HaveValue(Equal(int64(12_345))))
		Expect(pc.Status.MaxOverdraftCents).To(HaveValue(Equal(int64(500))))
		Expect(pc.Status.ActiveRentals).To(HaveValue(BeZero()))
		Expect(pc.Status.CostCentsPerHour).To(HaveValue(BeZero()))

		By("showing a rental started outside the cluster")
		_, err := acct.api.Client(GinkgoTB()).Rentals.Create(ctx, h100x1(""))
		Expect(err).NotTo(HaveOccurred())
		reconcileOnce(ctx, r, key)
		Expect(k8sClient.Get(ctx, key, pc)).To(Succeed())
		Expect(pc.Status.ActiveRentals).To(HaveValue(Equal(int32(1))))
		Expect(pc.Status.CostCentsPerHour).To(HaveValue(Equal(int64(275))))
	})

	It("reports a failing account read as an API error", func() {
		acct := newFakeAccount(ctx)
		acct.api.InjectFault(hyperbolictest.Fault{
			Method: http.MethodGet, Path: hyperbolictest.PathActiveRentalSummary,
			Status: http.StatusBadRequest,
		})
		r := &ProviderConfigReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), NewClient: testClientFactory}
		key := client.ObjectKey{Namespace: acct.namespace, Name: acct.config.Name}

		reconcileOnce(ctx, r, key)

		pc := &hypv1alpha1.ProviderConfig{}
		Expect(k8sClient.Get(ctx, key, pc)).To(Succeed())
		synced := apimeta.FindStatusCondition(pc.Status.Conditions, hypv1alpha1.ConditionSynced)
		Expect(synced).NotTo(BeNil())
		Expect(synced.Status).To(Equal(metav1.ConditionFalse))
		Expect(synced.Reason).To(Equal(hypv1alpha1.ReasonAPIError))
	})

	It("reports a rejected API key as Unauthenticated", func() {
		acct := newFakeAccount(ctx)
		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: acct.namespace, Name: "hyperbolic-key"}, secret)).To(Succeed())
		secret.Data["api-key"] = []byte("sk_test_revoked")
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())
		r := newConfigReconciler()
		key := client.ObjectKey{Namespace: acct.namespace, Name: acct.config.Name}

		reconcileOnce(ctx, r, key)

		pc := &hypv1alpha1.ProviderConfig{}
		Expect(k8sClient.Get(ctx, key, pc)).To(Succeed())
		cond := apimeta.FindStatusCondition(pc.Status.Conditions, hypv1alpha1.ConditionReady)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("Unauthenticated"))
	})

	It("filters the account's catalog into a GPUAvailability", func() {
		acct := newFakeAccount(ctx)
		ga := &hypv1alpha1.GPUAvailability{
			ObjectMeta: metav1.ObjectMeta{Name: "h100", Namespace: acct.namespace},
			Spec:       hypv1alpha1.GPUAvailabilitySpec{ProviderConfigRef: acct.config, GPUType: "h100"},
		}
		Expect(k8sClient.Create(ctx, ga)).To(Succeed())
		r := newAvailabilityReconciler(0)

		reconcileOnce(ctx, r, client.ObjectKeyFromObject(ga))

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ga), ga)).To(Succeed())
		Expect(ga.Status.Offerings).To(HaveLen(2)) // h100 x1 and h100 x8 in the fake's default catalog
		Expect(ga.Status.Offerings[0].CostCentsPerHour).To(Equal(int64(275)))
	})
})
