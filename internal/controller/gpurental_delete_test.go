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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

var _ = Describe("Deleting without leaking", func() {
	ctx := context.Background()
	var acct *fakeAccount

	BeforeEach(func() { acct = newFakeAccount(ctx) })

	gone := func(key types.NamespacedName) bool {
		return apierrors.IsNotFound(k8sClient.Get(ctx, key, &hypv1alpha1.GPURental{}))
	}
	deleteSecret := func() {
		GinkgoHelper()
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "hyperbolic-key", Namespace: acct.namespace}}
		Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
	}
	deleteRental := func(key types.NamespacedName) {
		GinkgoHelper()
		gr := &hypv1alpha1.GPURental{}
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		Expect(k8sClient.Delete(ctx, gr)).To(Succeed())
	}
	// running creates a GPURental and brings its rental to Running.
	running := func(r *GPURentalReconciler, gr *hypv1alpha1.GPURental) types.NamespacedName {
		GinkgoHelper()
		key, id := acct.createRental(ctx, r, gr)
		Expect(acct.api.SetRentalStatus(id, hyperbolic.RentalStatusRunning)).To(BeTrue())
		reconcileOnce(ctx, r, key)
		return key
	}
	events := func(rec *record.FakeRecorder) []string {
		var out []string
		for {
			select {
			case e := <-rec.Events:
				out = append(out, e)
			default:
				return out
			}
		}
	}

	It("terminates with the last known key when the Secret was deleted first", func() {
		// What `kubectl delete namespace` does: the Secret has no finalizer and
		// goes away before the GPURental's cleanup runs.
		r := newRentalReconciler()
		key := running(r, acct.rental("namespace-teardown"))

		deleteSecret()
		deleteRental(key)
		reconcileOnce(ctx, r, key)

		Expect(acct.api.ActiveRentals()).To(BeEmpty())
		Expect(gone(key)).To(BeTrue())
	})

	It("keeps the object and explains the way out when no key is available", func() {
		key := running(newRentalReconciler(), acct.rental("no-key"))
		deleteSecret()
		deleteRental(key)

		restarted := newRentalReconciler() // an operator restart forgets every key
		rec := record.NewFakeRecorder(10)
		restarted.Recorder = rec
		reconcileOnce(ctx, restarted, key)

		gr := &hypv1alpha1.GPURental{}
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		Expect(gr.DeletionTimestamp).NotTo(BeNil())
		Expect(gr.Finalizers).To(ContainElement(hypv1alpha1.FinalizerTerminate))
		Expect(conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionSynced).Reason).To(Equal(hypv1alpha1.ReasonCredentialError))
		Expect(events(rec)).To(ContainElement(SatisfyAll(
			ContainSubstring(eventTerminationBlocked), ContainSubstring("deletionPolicy: Orphan"))))
		Expect(acct.api.ActiveRentals()).To(HaveLen(1))

		By("letting go once the person chooses Orphan")
		gr.Spec.DeletionPolicy = hypv1alpha1.DeletionPolicyOrphan
		Expect(k8sClient.Update(ctx, gr)).To(Succeed())
		reconcileOnce(ctx, restarted, key)
		Expect(gone(key)).To(BeTrue())
		Expect(acct.api.ActiveRentals()).To(HaveLen(1))
		Expect(events(rec)).To(ContainElement(ContainSubstring(eventOrphaned)))
	})

	It("leaves the rental running with deletionPolicy Orphan", func() {
		r := newRentalReconciler()
		gr := acct.rental("hand-over")
		gr.Spec.DeletionPolicy = hypv1alpha1.DeletionPolicyOrphan
		key := running(r, gr)

		deleteRental(key)
		reconcileOnce(ctx, r, key)

		Expect(gone(key)).To(BeTrue())
		Expect(acct.api.ActiveRentals()).To(HaveLen(1))
	})

	It("keeps the finalizer while terminating fails, then finishes", func() {
		r := newRentalReconciler()
		key := running(r, acct.rental("terminate-503"))
		acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodPost, Path: hyperbolictest.PathTerminate, Status: http.StatusServiceUnavailable})

		deleteRental(key)
		reconcileOnce(ctx, r, key)
		Expect(gone(key)).To(BeFalse())
		Expect(acct.api.ActiveRentals()).To(HaveLen(1))

		reconcileOnce(ctx, r, key)
		Expect(acct.api.ActiveRentals()).To(BeEmpty())
		Expect(gone(key)).To(BeTrue())
	})

	It("finds and terminates a rental whose create outcome was unknown", func() {
		r := newRentalReconciler()
		acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodPost, Path: hyperbolictest.PathRentals, Status: http.StatusBadGateway, AfterCommit: true})
		gr := acct.rental("deleted-mid-create")
		Expect(k8sClient.Create(ctx, gr)).To(Succeed())
		key := client.ObjectKeyFromObject(gr)
		reconcileOnce(ctx, r, key) // finalizer
		reconcileOnce(ctx, r, key) // create: 502 after commit, no id recorded
		Expect(acct.api.ActiveRentals()).To(HaveLen(1))

		deleteRental(key)
		reconcileOnce(ctx, r, key)

		Expect(acct.api.ActiveRentals()).To(BeEmpty())
		Expect(gone(key)).To(BeTrue())
	})

	It("removes a rental that never rented anything without needing credentials", func() {
		r := newRentalReconciler()
		gr := acct.rental("never-rented")
		gr.Spec.GPUType = "gb300" // not in the catalog: Blocked before anything is sent
		Expect(k8sClient.Create(ctx, gr)).To(Succeed())
		key := client.ObjectKeyFromObject(gr)
		reconcileOnce(ctx, r, key)
		reconcileOnce(ctx, r, key)
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		Expect(gr.Status.Phase).To(Equal(hypv1alpha1.PhaseBlocked))

		deleteSecret()
		deleteRental(key)
		reconcileOnce(ctx, newRentalReconciler(), key)

		Expect(gone(key)).To(BeTrue())
		Expect(acct.api.Requests(http.MethodPost, hyperbolictest.PathRentals)).To(Equal(0))
	})
})
