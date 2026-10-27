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
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	"github.com/HyperbolicLabs/hyperbolic-go/hyperbolictest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/account"
)

var _ = DescribeTable("rentalLabel",
	func(name, uid, displayName, want string) {
		gr := &hypv1alpha1.GPURental{
			ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(uid)},
			Spec:       hypv1alpha1.GPURentalSpec{DisplayName: displayName},
		}
		got := rentalLabel(gr)
		Expect(got).To(Equal(want))
		Expect(len(got)).To(BeNumerically("<=", maxLabelLength))
	},
	Entry("uses the display name when set", "train", "0f4d2a9c-aaaa", "llama-ft", "llama-ft"),
	Entry("otherwise the name and 6 characters of the UID", "train", "0f4d2a9c-aaaa", "", "train-0f4d2a"),
	Entry("cuts long names to fit 40 characters", strings.Repeat("a", 60), "0f4d2a9c-aaaa", "", strings.Repeat("a", 33)+"-0f4d2a"),
	Entry("does not leave a separator at the cut", strings.Repeat("a", 32)+"-b-c", "0f4d2a9c-aaaa", "", strings.Repeat("a", 32)+"-0f4d2a"),
)

// shortTimeouts builds clients that give up on a request after d, so tests
// can make a create time out after the fake has applied it.
func shortTimeouts(d time.Duration) account.ClientFactory {
	return func(endpoint, apiKey string) (*hyperbolic.Client, error) {
		return hyperbolic.NewClient(apiKey,
			hyperbolic.WithBaseURL(endpoint),
			hyperbolic.WithHTTPClient(&http.Client{Timeout: d}),
			hyperbolic.WithRetryPolicy(hyperbolic.NoRetries()),
		)
	}
}

// interceptedClient returns an envtest client whose calls go through funcs,
// to inject failures into the operator's writes.
func interceptedClient(funcs interceptor.Funcs) client.Client {
	GinkgoHelper()
	c, err := client.NewWithWatch(cfg, client.Options{Scheme: k8sClient.Scheme()})
	Expect(err).NotTo(HaveOccurred())
	return interceptor.NewClient(c, funcs)
}

// labeled returns the active rentals in the fake that carry label.
func labeled(api *hyperbolictest.Server, label string) []hyperbolic.RentalWithStatus {
	var out []hyperbolic.RentalWithStatus
	for _, vm := range api.ActiveRentals() {
		if l, err := vm.Label.Get(); err == nil && l == label {
			out = append(out, vm)
		}
	}
	return out
}

var _ = Describe("Creating without renting twice", func() {
	ctx := context.Background()
	var (
		acct  *fakeAccount
		r     *GPURentalReconciler
		clk   *clocktesting.FakePassiveClock
		posts func() int
	)

	BeforeEach(func() {
		acct = newFakeAccount(ctx)
		clk = clocktesting.NewFakePassiveClock(time.Now())
		r = newRentalReconciler()
		r.Clock = clk
		r.NewClient = shortTimeouts(300 * time.Millisecond)
		posts = func() int { return acct.api.Requests(http.MethodPost, hyperbolictest.PathRentals) }
	})

	// start creates gr and reconciles once to add the finalizer, so the next
	// reconcile is the one that creates.
	start := func(gr *hypv1alpha1.GPURental) types.NamespacedName {
		GinkgoHelper()
		Expect(k8sClient.Create(ctx, gr)).To(Succeed())
		key := client.ObjectKeyFromObject(gr)
		reconcileOnce(ctx, r, key)
		return key
	}
	// expectAdopted checks that exactly one rental carries the object's label
	// and that the object recorded it.
	expectAdopted := func(key types.NamespacedName) {
		GinkgoHelper()
		gr := &hypv1alpha1.GPURental{}
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		matches := labeled(acct.api, rentalLabel(gr))
		Expect(matches).To(HaveLen(1))
		Expect(gr.Status.RentalID).To(Equal(matches[0].RentalID))
		Expect(gr.Annotations).To(HaveKeyWithValue(hypv1alpha1.AnnotationRentalID, matches[0].RentalID))
		Expect(gr.Annotations).NotTo(HaveKey(hypv1alpha1.AnnotationCreatePending))
	}

	It("sends every rental with its label and records the id", func() {
		gr := acct.rental("labeled")
		key := start(gr)
		reconcileOnce(ctx, r, key)
		expectAdopted(key)
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		Expect(gr.Status.Label).To(Equal(rentalLabel(gr)))
	})

	It("adopts the rental when the create fails after the server applied it", func() {
		acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodPost, Path: hyperbolictest.PathRentals, Status: http.StatusBadGateway, AfterCommit: true})
		key := start(acct.rental("committed-502"))

		reconcileOnce(ctx, r, key) // create: 502, outcome unknown
		gr := &hypv1alpha1.GPURental{}
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		Expect(gr.Annotations).To(HaveKey(hypv1alpha1.AnnotationCreatePending))
		Expect(gr.Status.RentalID).To(BeEmpty())

		reconcileOnce(ctx, r, key) // finds it by label
		Expect(posts()).To(Equal(1))
		expectAdopted(key)
	})

	It("adopts the rental when the create times out after the server applied it", func() {
		acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodPost, Path: hyperbolictest.PathRentals, Delay: 2 * time.Second, AfterCommit: true})
		key := start(acct.rental("committed-timeout"))

		reconcileOnce(ctx, r, key)
		reconcileOnce(ctx, r, key)
		Expect(posts()).To(Equal(1))
		expectAdopted(key)
	})

	It("adopts the rental after a crash between the create and recording its id", func() {
		gr := acct.rental("crashed")
		key := start(gr)
		// What a crash leaves behind: create-pending on the object and a
		// rental with its label at Hyperbolic, but no id recorded.
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		gr.Annotations = map[string]string{hypv1alpha1.AnnotationCreatePending: clk.Now().UTC().Format(time.RFC3339)}
		Expect(k8sClient.Update(ctx, gr)).To(Succeed())
		_, err := acct.api.Client(GinkgoTB()).Rentals.Create(ctx, h100x1(rentalLabel(gr)))
		Expect(err).NotTo(HaveOccurred())

		reconcileOnce(ctx, r, key)
		Expect(posts()).To(Equal(1)) // only the create the "crashed" operator sent
		expectAdopted(key)
	})

	It("stops and names the candidates when several rentals carry the label", func() {
		gr := acct.rental("ambiguous")
		key := start(gr)
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		gr.Annotations = map[string]string{hypv1alpha1.AnnotationCreatePending: clk.Now().UTC().Format(time.RFC3339)}
		Expect(k8sClient.Update(ctx, gr)).To(Succeed())
		ids := make([]string, 0, 2)
		for range 2 {
			c, err := acct.api.Client(GinkgoTB()).Rentals.Create(ctx, h100x1(rentalLabel(gr)))
			Expect(err).NotTo(HaveOccurred())
			ids = append(ids, c.RentalID)
		}

		clk.SetTime(clk.Now().Add(10 * time.Minute)) // long past any grace period
		reconcileOnce(ctx, r, key)
		reconcileOnce(ctx, r, key)

		Expect(posts()).To(Equal(2)) // the two above; the operator sent none
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		Expect(gr.Status.RentalID).To(BeEmpty())
		ready := conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionReady)
		Expect(ready.Status).To(Equal(metav1.ConditionUnknown))
		Expect(ready.Reason).To(Equal(hypv1alpha1.ReasonCreateOutcomeUnknown))
		Expect(ready.Message).To(ContainSubstring(ids[0]))
		Expect(ready.Message).To(ContainSubstring(ids[1]))
	})

	It("clears create-pending when the create definitely failed", func() {
		acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodPost, Path: hyperbolictest.PathRentals, Status: http.StatusBadRequest, Code: hyperbolic.CodeInsufficientBalance})
		key := start(acct.rental("broke"))

		reconcileOnce(ctx, r, key)

		gr := &hypv1alpha1.GPURental{}
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		Expect(gr.Annotations).NotTo(HaveKey(hypv1alpha1.AnnotationCreatePending))
		Expect(gr.Status.Phase).To(Equal(hypv1alpha1.PhaseBlocked))
		Expect(conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionReady).Reason).To(Equal(hypv1alpha1.ReasonInsufficientBalance))
		Expect(acct.api.ActiveRentals()).To(BeEmpty())
	})

	It("waits out the grace period before creating again when nothing was found", func() {
		acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodPost, Path: hyperbolictest.PathRentals, Status: http.StatusBadGateway})
		key := start(acct.rental("not-committed"))
		reconcileOnce(ctx, r, key) // 502 before commit: outcome unknown, nothing created

		res := reconcileOnce(ctx, r, key)
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))
		Expect(posts()).To(Equal(1))
		Expect(acct.api.ActiveRentals()).To(BeEmpty())

		clk.SetTime(clk.Now().Add(createRequestTimeout + createSettleGrace + time.Second))
		reconcileOnce(ctx, r, key)
		Expect(posts()).To(Equal(2))
		expectAdopted(key)
	})

	It("sends nothing if create-pending cannot be recorded", func() {
		r.Client = interceptedClient(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if _, ok := obj.GetAnnotations()[hypv1alpha1.AnnotationCreatePending]; ok {
					return errors.New("injected: patch refused")
				}
				return c.Patch(ctx, obj, patch, opts...)
			},
		})
		gr := acct.rental("no-write-ahead")
		Expect(k8sClient.Create(ctx, gr)).To(Succeed())
		key := client.ObjectKeyFromObject(gr)
		reconcileOnce(ctx, r, key) // finalizer

		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).To(MatchError(ContainSubstring("create-pending")))
		Expect(posts()).To(Equal(0))
	})

	It("never leaves more than one rental per object under random faults", func() {
		rng := rand.New(rand.NewPCG(42, 42))
		failNextIDWrite, failNextStatusWrite := false, false
		r.Client = interceptedClient(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if _, ok := obj.GetAnnotations()[hypv1alpha1.AnnotationRentalID]; ok && failNextIDWrite {
					failNextIDWrite = false
					return errors.New("injected: crash before the id was recorded")
				}
				return c.Patch(ctx, obj, patch, opts...)
			},
			SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if failNextStatusWrite {
					failNextStatusWrite = false
					return errors.New("injected: status write lost")
				}
				return c.SubResource(sub).Update(ctx, obj, opts...)
			},
		})
		post := func(f hyperbolictest.Fault) {
			f.Method, f.Path = http.MethodPost, hyperbolictest.PathRentals
			acct.api.InjectFault(f)
		}

		const objects = 100
		for i := range objects {
			switch rng.IntN(8) {
			case 0: // no fault
			case 1:
				post(hyperbolictest.Fault{Status: http.StatusBadGateway, AfterCommit: true})
			case 2:
				post(hyperbolictest.Fault{Status: http.StatusServiceUnavailable})
			case 3:
				post(hyperbolictest.Fault{Delay: time.Second, AfterCommit: true})
			case 4:
				post(hyperbolictest.Fault{Status: http.StatusTooManyRequests})
			case 5:
				acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodGet, Path: hyperbolictest.PathActiveRentals, Status: http.StatusServiceUnavailable, Times: 1 + rng.IntN(2)})
			case 6:
				failNextIDWrite = true
			case 7:
				failNextStatusWrite = true
			}

			gr := acct.rental(fmt.Sprintf("loop-%d", i))
			Expect(k8sClient.Create(ctx, gr)).To(Succeed())
			key := client.ObjectKeyFromObject(gr)
			for range 10 {
				_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: key}) // injected failures return errors
				Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
				if gr.Status.RentalID != "" {
					break
				}
				clk.SetTime(clk.Now().Add(2 * time.Minute)) // let any grace period pass
			}
			failNextIDWrite, failNextStatusWrite = false, false
			Expect(gr.Status.RentalID).NotTo(BeEmpty(), "object %d never got its rental: %+v", i, gr.Status.Conditions)
			Expect(labeled(acct.api, rentalLabel(gr))).To(HaveLen(1), "object %d", i)
		}
		Expect(acct.api.ActiveRentals()).To(HaveLen(objects))
	})
})
