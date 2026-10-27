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
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/metrics"
)

// testRegistry holds testMetrics, which the test client factory and
// newRentalReconciler use, so specs can read what the operator recorded.
var (
	testRegistry = prometheus.NewRegistry()
	testMetrics  = func() *metrics.Recorder {
		rec := metrics.NewRecorder()
		if err := rec.Register(testRegistry); err != nil {
			panic(err)
		}
		return rec
	}()
)

// metricValue returns the value of the series name{labels} in testRegistry,
// or 0 if it does not exist.
func metricValue(name string, labels map[string]string) float64 {
	GinkgoHelper()
	families, err := testRegistry.Gather()
	Expect(err).NotTo(HaveOccurred())
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
	series:
		for _, m := range f.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			for k, v := range labels {
				if got[k] != v {
					continue series
				}
			}
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

var _ = Describe("Rental operation metrics", func() {
	ctx := context.Background()
	var (
		acct *fakeAccount
		r    *GPURentalReconciler
	)

	BeforeEach(func() {
		acct = newFakeAccount(ctx)
		r = newRentalReconciler()
	})

	count := func(op, result string) float64 {
		return metricValue("rental_operator_rental_operations_total", map[string]string{"operation": op, "result": result})
	}
	// counted runs f and returns how much the (op, result) counter grew.
	counted := func(op, result string, f func()) float64 {
		before := count(op, result)
		f()
		return count(op, result) - before
	}

	It("counts creates by outcome", func() {
		Expect(counted(metrics.OpCreate, metrics.ResultSuccess, func() {
			acct.createRental(ctx, r, acct.rental("ok"))
		})).To(Equal(1.0))

		Expect(counted(metrics.OpCreate, metrics.ResultFailed, func() {
			acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodPost, Path: hyperbolictest.PathRentals,
				Status: http.StatusBadRequest, Code: hyperbolic.CodeInsufficientBalance})
			gr := acct.rental("refused")
			Expect(k8sClient.Create(ctx, gr)).To(Succeed())
			key := client.ObjectKeyFromObject(gr)
			reconcileOnce(ctx, r, key) // finalizer
			reconcileOnce(ctx, r, key)
		})).To(Equal(1.0))

		Expect(counted(metrics.OpCreate, metrics.ResultUnknown, func() {
			acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodPost, Path: hyperbolictest.PathRentals,
				Status: http.StatusBadGateway, AfterCommit: true})
			gr := acct.rental("maybe")
			Expect(k8sClient.Create(ctx, gr)).To(Succeed())
			key := client.ObjectKeyFromObject(gr)
			reconcileOnce(ctx, r, key)
			reconcileOnce(ctx, r, key)
		})).To(Equal(1.0))
	})

	It("counts terminations by outcome", func() {
		key, _ := acct.createRental(ctx, r, acct.rental("leaving"))
		gr := &hypv1alpha1.GPURental{}
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		Expect(k8sClient.Delete(ctx, gr)).To(Succeed())

		Expect(counted(metrics.OpTerminate, metrics.ResultFailed, func() {
			acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodPost, Path: hyperbolictest.PathTerminate,
				Status: http.StatusInternalServerError})
			reconcileOnce(ctx, r, key)
		})).To(Equal(1.0))

		Expect(counted(metrics.OpTerminate, metrics.ResultSuccess, func() {
			reconcileOnce(ctx, r, key)
		})).To(Equal(1.0))
		Expect(acct.api.ActiveRentals()).To(BeEmpty())
	})

	It("counts every API request by path and status", func() {
		requests := func() float64 {
			return metricValue("rental_operator_api_requests_total",
				map[string]string{"method": http.MethodPost, "path": hyperbolictest.PathRentals, "code": "200"})
		}
		before := requests()
		acct.createRental(ctx, r, acct.rental("counted"))
		Expect(requests() - before).To(Equal(1.0))
	})
})
