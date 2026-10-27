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
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// The other specs call Reconcile directly. This one runs the controller in a
// manager, so the watch sees the reconcile's own status writes.
var _ = Describe("ProviderConfig in a running manager", func() {
	It("reads the balance once per reconcile, not again after its own status write", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		acct := newFakeAccount(ctx)

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:                 k8sClient.Scheme(),
			Metrics:                metricsserver.Options{BindAddress: "0"},
			HealthProbeBindAddress: "0",
			Controller:             config.Controller{SkipNameValidation: ptr(true)},
			Cache:                  cache.Options{DefaultNamespaces: map[string]cache.Config{acct.namespace: {}}},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect((&ProviderConfigReconciler{
			Client:    mgr.GetClient(),
			Scheme:    mgr.GetScheme(),
			NewClient: testClientFactory,
			APIReader: mgr.GetAPIReader(),
		}).SetupWithManager(mgr)).To(Succeed())
		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(ctx)).To(Succeed())
		}()

		balanceReads := func() int { return acct.api.Requests(http.MethodGet, hyperbolictest.PathBalance) }
		// Startup reconciles once per watched kind's first event. Let it settle.
		Eventually(balanceReads, 10*time.Second, 50*time.Millisecond).ShouldNot(BeZero())
		var settled int
		Eventually(func() bool {
			n := balanceReads()
			stable := n == settled
			settled = n
			return stable
		}, 10*time.Second, 1500*time.Millisecond).Should(BeTrue())

		// One Secret change is one reconcile. Its status write records a new
		// lastChecked (a second has passed), so without the filter the write
		// itself would trigger a second reconcile and a second read.
		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: acct.namespace, Name: "hyperbolic-key"}, secret)).To(Succeed())
		base := secret.DeepCopy()
		secret.Annotations = map[string]string{"test/touched": time.Now().String()}
		Expect(k8sClient.Patch(ctx, secret, client.MergeFrom(base))).To(Succeed())

		Eventually(balanceReads, 10*time.Second, 50*time.Millisecond).Should(Equal(settled + 1))
		Consistently(balanceReads, 3*time.Second, 100*time.Millisecond).Should(Equal(settled + 1))
	})
})
