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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

var _ = Describe("ProviderConfig in use", func() {
	ctx := context.Background()
	var (
		acct  *fakeAccount
		pcr   *ProviderConfigReconciler
		pcKey client.ObjectKey
	)

	BeforeEach(func() {
		acct = newFakeAccount(ctx)
		pcr = newConfigReconciler()
		pcKey = client.ObjectKey{Namespace: acct.namespace, Name: acct.config.Name}
	})

	config := func() *hypv1alpha1.ProviderConfig {
		GinkgoHelper()
		pc := &hypv1alpha1.ProviderConfig{}
		Expect(k8sClient.Get(ctx, pcKey, pc)).To(Succeed())
		return pc
	}

	It("waits for its GPURentals before it can be deleted", func() {
		r := newRentalReconciler()
		key, _ := acct.createRental(ctx, r, acct.rental("user"))
		reconcileOnce(ctx, pcr, pcKey)
		Expect(config().Finalizers).To(ContainElement(hypv1alpha1.FinalizerInUse))
		Expect(config().Status.ManagedRentals).To(Equal(int32(1)))

		By("holding the config while the rental may be billing")
		Expect(k8sClient.Delete(ctx, config())).To(Succeed())
		reconcileOnce(ctx, pcr, pcKey)
		pc := config()
		Expect(pc.DeletionTimestamp).NotTo(BeNil())
		ready := conditionOf(pc.Status.Conditions, hypv1alpha1.ConditionReady)
		Expect(ready.Reason).To(Equal("InUse"))
		Expect(ready.Message).To(ContainSubstring("user"))

		By("still letting the rental use its credentials")
		reconcileOnce(ctx, r, key)
		gr := &hypv1alpha1.GPURental{}
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		Expect(conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionSynced).Status).To(Equal(metav1.ConditionTrue))

		By("letting go once the rental is gone")
		Expect(k8sClient.Delete(ctx, gr)).To(Succeed())
		reconcileOnce(ctx, r, key)
		Expect(acct.api.ActiveRentals()).To(BeEmpty())
		reconcileOnce(ctx, pcr, pcKey)
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, pcKey, &hypv1alpha1.ProviderConfig{}))).To(BeTrue())
	})

	It("is not held by a terminated record", func() {
		r := newRentalReconciler()
		gr := acct.rental("finished")
		key, _ := acct.createRental(ctx, r, gr)
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		gr.Spec.Terminated = true
		Expect(k8sClient.Update(ctx, gr)).To(Succeed())
		reconcileOnce(ctx, r, key)
		reconcileOnce(ctx, pcr, pcKey)
		Expect(config().Status.ManagedRentals).To(BeZero())

		Expect(k8sClient.Delete(ctx, config())).To(Succeed())
		reconcileOnce(ctx, pcr, pcKey)
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, pcKey, &hypv1alpha1.ProviderConfig{}))).To(BeTrue())
	})
})

var _ = DescribeTable("GPURental updates that requeue its ProviderConfig",
	func(change func(*hypv1alpha1.GPURental), want bool) {
		before := &hypv1alpha1.GPURental{}
		after := before.DeepCopy()
		change(after)
		Expect(rentalUsageChanged.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: after})).To(Equal(want))
	},
	Entry("a routine status update", func(gr *hypv1alpha1.GPURental) { gr.Status.Phase = hypv1alpha1.PhaseRunning }, false),
	Entry("deletion starting", func(gr *hypv1alpha1.GPURental) { now := metav1.Now(); gr.DeletionTimestamp = &now }, true),
	Entry("becoming a terminated record", func(gr *hypv1alpha1.GPURental) { now := metav1.Now(); gr.Status.TerminatedAt = &now }, true),
	Entry("its rental being created", func(gr *hypv1alpha1.GPURental) { gr.Status.RentalID = "rnt_7" }, true),
)
