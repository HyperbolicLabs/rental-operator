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
	"time"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	"github.com/HyperbolicLabs/hyperbolic-go/hyperbolictest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

// conditionOf returns the condition of type t, failing the spec if absent.
func conditionOf(conds []metav1.Condition, t string) metav1.Condition {
	GinkgoHelper()
	c := apimeta.FindStatusCondition(conds, t)
	Expect(c).NotTo(BeNil(), "no %s condition in %+v", t, conds)
	return *c
}

var _ = Describe("GPURental status contract", func() {
	ctx := context.Background()

	It("records a rental that ends without the operator and never rents it again", func() {
		acct := newFakeAccount(ctx)
		r := newRentalReconciler()
		gr := acct.rental("ended-elsewhere")
		key, id := acct.createRental(ctx, r, gr)
		Expect(acct.api.SetRentalStatus(id, hyperbolic.RentalStatusRunning)).To(BeTrue())
		reconcileOnce(ctx, r, key)

		Expect(acct.api.TerminateExternally(id)).To(BeTrue())
		reconcileOnce(ctx, r, key)

		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		Expect(gr.Status.Phase).To(Equal(hypv1alpha1.PhaseTerminated))
		Expect(gr.Status.TerminatedAt).NotTo(BeNil())
		ready := conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionReady)
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Reason).To(Equal(hypv1alpha1.ReasonTerminatedExternally))

		By("leaving the record alone on later reconciles")
		lists := acct.api.Requests(http.MethodGet, hyperbolictest.PathActiveRentals)
		reconcileOnce(ctx, r, key)
		reconcileOnce(ctx, r, key)
		Expect(acct.api.Requests(http.MethodPost, hyperbolictest.PathRentals)).To(Equal(1))
		Expect(acct.api.Requests(http.MethodGet, hyperbolictest.PathActiveRentals)).To(Equal(lists))
		Expect(acct.api.ActiveRentals()).To(BeEmpty())
	})

	It("reports a failed poll on Synced without touching the rental's phase", func() {
		acct := newFakeAccount(ctx)
		r := newRentalReconciler()
		gr := acct.rental("flaky-poll")
		key, _ := acct.createRental(ctx, r, gr)

		acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodGet, Path: hyperbolictest.PathActiveRentals, Status: http.StatusServiceUnavailable})
		reconcileOnce(ctx, r, key)

		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		Expect(gr.Status.Phase).To(Equal(hypv1alpha1.PhaseProvisioning))
		Expect(gr.Status.TerminatedAt).To(BeNil())
		Expect(conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionReady).Reason).To(Equal(hypv1alpha1.ReasonProvisioning))
		synced := conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionSynced)
		Expect(synced.Status).To(Equal(metav1.ConditionFalse))
		Expect(synced.Reason).To(Equal(hypv1alpha1.ReasonAPIError))

		By("recovering on the next successful poll")
		reconcileOnce(ctx, r, key)
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		Expect(conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionSynced).Status).To(Equal(metav1.ConditionTrue))
	})

	It("terminates for spec.terminated and keeps the object as a record", func() {
		acct := newFakeAccount(ctx)
		r := newRentalReconciler()
		gr := acct.rental("stop-me")
		key, _ := acct.createRental(ctx, r, gr)

		gr.Spec.Terminated = true
		Expect(k8sClient.Update(ctx, gr)).To(Succeed())
		reconcileOnce(ctx, r, key)

		Expect(acct.api.ActiveRentals()).To(BeEmpty())
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		Expect(gr.Status.TerminatedAt).NotTo(BeNil())
		Expect(gr.Status.Phase).To(Equal(hypv1alpha1.PhaseTerminated))
		Expect(conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionReady).Reason).To(Equal(hypv1alpha1.ReasonTerminated))
	})

	It("keeps showing a stale catalog but reports the failed refresh on Synced", func() {
		acct := newFakeAccount(ctx)
		ga := &hypv1alpha1.GPUAvailability{
			ObjectMeta: metav1.ObjectMeta{Name: "all", Namespace: acct.namespace},
			Spec:       hypv1alpha1.GPUAvailabilitySpec{ProviderConfigRef: acct.config},
		}
		Expect(k8sClient.Create(ctx, ga)).To(Succeed())
		key := client.ObjectKeyFromObject(ga)
		r := newAvailabilityReconciler(time.Nanosecond)
		reconcileOnce(ctx, r, key)

		acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodGet, Path: hyperbolictest.PathRentalOptions, Status: http.StatusBadGateway})
		reconcileOnce(ctx, r, key)

		Expect(k8sClient.Get(ctx, key, ga)).To(Succeed())
		Expect(ga.Status.Offerings).NotTo(BeEmpty())
		Expect(conditionOf(ga.Status.Conditions, hypv1alpha1.ConditionReady).Status).To(Equal(metav1.ConditionTrue))
		synced := conditionOf(ga.Status.Conditions, hypv1alpha1.ConditionSynced)
		Expect(synced.Status).To(Equal(metav1.ConditionFalse))
		Expect(synced.Reason).To(Equal(hypv1alpha1.ReasonAPIError))
	})
})

var _ = DescribeTable("rental status maps to phase and Ready",
	func(apiStatus, phase string, ready metav1.ConditionStatus, reason string) {
		r := newRentalReconciler()
		gr := &hypv1alpha1.GPURental{}
		r.applyRentalToStatus(gr, &hyperbolic.RentalWithStatus{RentalID: "rnt_1", Status: hyperbolic.RentalStatus(apiStatus)})
		Expect(gr.Status.Phase).To(Equal(phase))
		c := conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionReady)
		Expect(c.Status).To(Equal(ready))
		Expect(c.Reason).To(Equal(reason))
	},
	Entry(nil, string(hyperbolic.RentalStatusPending), hypv1alpha1.PhaseProvisioning, metav1.ConditionFalse, hypv1alpha1.ReasonProvisioning),
	Entry(nil, string(hyperbolic.RentalStatusRunning), hypv1alpha1.PhaseRunning, metav1.ConditionTrue, hypv1alpha1.ReasonRunning),
	Entry(nil, string(hyperbolic.RentalStatusRebooting), hypv1alpha1.PhaseRebooting, metav1.ConditionFalse, hypv1alpha1.ReasonRebooting),
	Entry(nil, string(hyperbolic.RentalStatusTerminating), hypv1alpha1.PhaseTerminating, metav1.ConditionFalse, hypv1alpha1.ReasonTerminating),
	Entry(nil, string(hyperbolic.RentalStatusFailed), hypv1alpha1.PhaseFailed, metav1.ConditionFalse, hypv1alpha1.ReasonFailed),
	Entry(nil, "SomethingNew", hypv1alpha1.PhaseUnknown, metav1.ConditionUnknown, hypv1alpha1.ReasonUnknown),
)
