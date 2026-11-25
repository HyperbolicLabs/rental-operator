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

	"github.com/HyperbolicLabs/hyperbolic-go/hyperbolictest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// These run the controllers in a manager, so the watches see the reconciles'
// own writes. A create that the API refuses must wait for its retry, not be
// sent again by the write that records the failure.
var _ = Describe("Controllers in a running manager", func() {
	var (
		ctx  context.Context
		acct *fakeAccount
	)

	BeforeEach(func() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
		acct = newFakeAccount(ctx)
		startManager(ctx, acct.namespace, func(mgr ctrl.Manager) error {
			rentals := newRentalReconciler()
			rentals.Client, rentals.APIReader = mgr.GetClient(), mgr.GetAPIReader()
			if err := rentals.SetupWithManager(mgr); err != nil {
				return err
			}
			keys := newSSHKeyReconciler()
			keys.Client, keys.APIReader = mgr.GetClient(), mgr.GetAPIReader()
			return keys.SetupWithManager(mgr)
		})
	})

	It("creates a rental", func() {
		gr := acct.rental("created")
		Expect(k8sClient.Create(ctx, gr)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gr), gr)).To(Succeed())
			g.Expect(gr.Status.RentalID).NotTo(BeEmpty())
		}, 10*time.Second, 50*time.Millisecond).Should(Succeed())
		Expect(acct.api.ActiveRentals()).To(HaveLen(1))
	})

	It("sends a refused rental create once until the retry", func() {
		acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodPost, Path: hyperbolictest.PathRentals,
			Status: http.StatusBadRequest, Message: "Input validation failed", Times: 100})
		Expect(k8sClient.Create(ctx, acct.rental("refused"))).To(Succeed())

		creates := func() int { return acct.api.Requests(http.MethodPost, hyperbolictest.PathRentals) }
		Eventually(creates, 10*time.Second, 50*time.Millisecond).Should(Equal(1))
		Consistently(creates, 3*time.Second, 100*time.Millisecond).Should(Equal(1))
	})

	It("saves a key", func() {
		key := acct.sshKey("saved", "saved")
		Expect(k8sClient.Create(ctx, key)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(key), key)).To(Succeed())
			g.Expect(key.Status.ID).NotTo(BeZero())
		}, 10*time.Second, 50*time.Millisecond).Should(Succeed())
	})

	It("sends a refused key save once until the retry", func() {
		acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodPost, Path: hyperbolictest.PathSSHKeys,
			Status: http.StatusBadGateway, Times: 100})
		Expect(k8sClient.Create(ctx, acct.sshKey("refused", "refused"))).To(Succeed())

		saves := func() int { return acct.api.Requests(http.MethodPost, hyperbolictest.PathSSHKeys) }
		Eventually(saves, 10*time.Second, 50*time.Millisecond).Should(Equal(1))
		Consistently(saves, 3*time.Second, 100*time.Millisecond).Should(Equal(1))
	})
})
