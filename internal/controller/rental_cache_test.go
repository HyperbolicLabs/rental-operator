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
	"fmt"
	"net/http"
	"time"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	"github.com/HyperbolicLabs/hyperbolic-go/hyperbolictest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

var _ = Describe("One rental list per account", func() {
	ctx := context.Background()
	var (
		acct  *fakeAccount
		r     *GPURentalReconciler
		clk   *clocktesting.FakePassiveClock
		lists func() int
	)

	BeforeEach(func() {
		acct = newFakeAccount(ctx)
		clk = clocktesting.NewFakePassiveClock(time.Now())
		r = newRentalReconciler()
		r.Clock = clk
		r.Rentals = NewRentalCache(clk)
		lists = func() int { return acct.api.Requests(http.MethodGet, hyperbolictest.PathActiveRentals) }
	})

	// rentals creates n GPURentals and returns their keys and rental ids.
	rentals := func(n int) ([]types.NamespacedName, []string) {
		GinkgoHelper()
		keys := make([]types.NamespacedName, 0, n)
		ids := make([]string, 0, n)
		for i := range n {
			key, id := acct.createRental(ctx, r, acct.rental(fmt.Sprintf("shared-%d", i)))
			keys = append(keys, key)
			ids = append(ids, id)
		}
		return keys, ids
	}
	pollAll := func(keys []types.NamespacedName) {
		GinkgoHelper()
		for _, key := range keys {
			reconcileOnce(ctx, r, key)
		}
	}

	It("serves every GPURental on the account from one list call per interval", func() {
		keys, ids := rentals(5)
		for _, id := range ids {
			Expect(acct.api.SetRentalStatus(id, hyperbolic.RentalStatusRunning)).To(BeTrue())
		}
		r.Rentals.Invalidate(credKey(acct.api.URL, acct.api.APIKey)) // forget lists taken while they were starting

		before := lists()
		pollAll(keys)
		Expect(lists() - before).To(Equal(1))

		pollAll(keys)
		Expect(lists()-before).To(Equal(1), "still fresh")

		clk.SetTime(clk.Now().Add(rentalListTTL + time.Second))
		pollAll(keys)
		Expect(lists() - before).To(Equal(2))
	})

	It("refreshes every 10s while a rental is provisioning", func() {
		keys, _ := rentals(2) // both still Pending
		pollAll(keys)
		before := lists()

		clk.SetTime(clk.Now().Add(rentalListProvisioningTTL + time.Second))
		pollAll(keys)
		Expect(lists() - before).To(Equal(1))
	})

	It("never reads a rental as gone from a list taken before it existed", func() {
		keys, _ := rentals(1)
		pollAll(keys) // the account's list is now cached

		// A rental the cached list has never seen, recorded on an object the
		// way a crash after create leaves it (id annotation, no status).
		created, err := acct.api.Client(GinkgoTB()).Rentals.Create(ctx, h100x1("late"))
		Expect(err).NotTo(HaveOccurred())
		gr := acct.rental("late")
		gr.Annotations = map[string]string{hypv1alpha1.AnnotationRentalID: created.RentalID}
		Expect(k8sClient.Create(ctx, gr)).To(Succeed())
		key := client.ObjectKeyFromObject(gr)
		reconcileOnce(ctx, r, key) // finalizer
		reconcileOnce(ctx, r, key)

		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		Expect(gr.Status.TerminatedAt).To(BeNil())
		Expect(gr.Status.RentalID).To(Equal(created.RentalID))
	})

	It("does not store a list fetched while the account changed", func() {
		cache := NewRentalCache(clk)
		hc := acct.api.Client(GinkgoTB(), hyperbolic.WithRetryPolicy(hyperbolic.NoRetries()))
		key := "account"
		acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodGet, Path: hyperbolictest.PathActiveRentals, Delay: 300 * time.Millisecond})

		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			_, err := cache.List(ctx, hc, key)
			Expect(err).NotTo(HaveOccurred())
		}()
		Eventually(lists).Should(Equal(1)) // the slow fetch is in flight
		cache.Invalidate(key)              // a create happens meanwhile
		<-done

		_, err := cache.List(ctx, hc, key)
		Expect(err).NotTo(HaveOccurred())
		Expect(lists()).To(Equal(2), "the in-flight result was not cached")
	})
})
