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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

var _ = Describe("Rental lifetime", func() {
	ctx := context.Background()
	var (
		acct *fakeAccount
		r    *GPURentalReconciler
		clk  *clocktesting.FakePassiveClock
	)

	BeforeEach(func() {
		acct = newFakeAccount(ctx)
		clk = clocktesting.NewFakePassiveClock(time.Now())
		r = newRentalReconciler()
		r.Clock = clk
	})

	withTTL := func(gr *hypv1alpha1.GPURental, ttl time.Duration) *hypv1alpha1.GPURental {
		gr.Spec.Lifetime = &hypv1alpha1.Lifetime{TTL: &metav1.Duration{Duration: ttl}}
		return gr
	}
	// runningAt creates gr, brings its rental to Running and records it.
	runningAt := func(gr *hypv1alpha1.GPURental) types.NamespacedName {
		GinkgoHelper()
		key, id := acct.createRental(ctx, r, gr)
		Expect(acct.api.SetRentalStatus(id, hyperbolic.RentalStatusRunning)).To(BeTrue())
		reconcileOnce(ctx, r, key)
		return key
	}
	get := func(key types.NamespacedName) *hypv1alpha1.GPURental {
		GinkgoHelper()
		gr := &hypv1alpha1.GPURental{}
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		return gr
	}
	after := func(d time.Duration) { clk.SetTime(clk.Now().Add(d)) }

	It("terminates when the TTL runs out and keeps the object as a record", func() {
		key := runningAt(withTTL(acct.rental("ttl"), time.Hour))
		gr := get(key)
		Expect(gr.Status.RunningSince).NotTo(BeNil())
		Expect(gr.Status.ExpiresAt.Time).To(BeTemporally("~", gr.Status.RunningSince.Add(time.Hour), time.Second))

		after(59 * time.Minute)
		res := reconcileOnce(ctx, r, key)
		Expect(res.RequeueAfter).To(BeNumerically("<=", time.Minute))
		Expect(acct.api.ActiveRentals()).To(HaveLen(1))

		after(2 * time.Minute)
		reconcileOnce(ctx, r, key)
		Expect(acct.api.ActiveRentals()).To(BeEmpty())
		gr = get(key)
		Expect(gr.Status.Phase).To(Equal(hypv1alpha1.PhaseTerminated))
		Expect(gr.Status.TerminatedAt).NotTo(BeNil())
		Expect(conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionReady).Reason).To(Equal(hypv1alpha1.ReasonTTLExpired))

		By("never renting again")
		reconcileOnce(ctx, r, key)
		Expect(acct.api.Requests(http.MethodPost, hyperbolictest.PathRentals)).To(Equal(1))
	})

	It("follows a TTL extended while the rental runs", func() {
		key := runningAt(withTTL(acct.rental("extended"), time.Hour))
		after(59 * time.Minute)
		gr := get(key)
		gr.Spec.Lifetime.TTL = &metav1.Duration{Duration: 2 * time.Hour}
		Expect(k8sClient.Update(ctx, gr)).To(Succeed())

		after(2 * time.Minute)
		reconcileOnce(ctx, r, key)
		Expect(acct.api.ActiveRentals()).To(HaveLen(1))
		gr = get(key)
		Expect(gr.Status.ExpiresAt.Time).To(BeTemporally("~", gr.Status.RunningSince.Add(2*time.Hour), time.Second))
	})

	It("terminates a rental that never starts within the provisioning timeout", func() {
		key, _ := acct.createRental(ctx, r, acct.rental("stuck")) // stays Pending
		Expect(get(key).Spec.Lifetime.ProvisioningTimeout.Duration).To(Equal(30 * time.Minute))

		after(29 * time.Minute)
		reconcileOnce(ctx, r, key)
		Expect(acct.api.ActiveRentals()).To(HaveLen(1))

		after(2 * time.Minute)
		reconcileOnce(ctx, r, key)
		Expect(acct.api.ActiveRentals()).To(BeEmpty())
		Expect(conditionOf(get(key).Status.Conditions, hypv1alpha1.ConditionReady).Reason).To(Equal(hypv1alpha1.ReasonProvisioningTimedOut))
	})

	It("leaves a rental alone once it is running, with no TTL", func() {
		key, id := acct.createRental(ctx, r, acct.rental("in-time"))
		after(5 * time.Minute)
		Expect(acct.api.SetRentalStatus(id, hyperbolic.RentalStatusRunning)).To(BeTrue())
		reconcileOnce(ctx, r, key)

		after(time.Hour)
		reconcileOnce(ctx, r, key)
		Expect(acct.api.ActiveRentals()).To(HaveLen(1))
		gr := get(key)
		Expect(gr.Status.Phase).To(Equal(hypv1alpha1.PhaseRunning))
		Expect(gr.Status.ExpiresAt).To(BeNil())
	})
})
