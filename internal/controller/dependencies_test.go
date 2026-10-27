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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"
)

// reconciler is what every reconciler in this package implements.
type reconciler interface {
	validate() error
	SetupWithManager(ctrl.Manager) error
}

var _ = Describe("Reconciler dependencies", func() {
	// Entries build their reconcilers lazily: k8sClient is only set once the
	// suite has started.
	DescribeTable("accepts a fully wired reconciler",
		func(build func() reconciler) {
			Expect(build().validate()).To(Succeed())
		},
		Entry("GPURental", func() reconciler { return newRentalReconciler() }),
		Entry("ProviderConfig", func() reconciler { return newConfigReconciler() }),
		Entry("ClusterProviderConfig", func() reconciler {
			return &ClusterProviderConfigReconciler{
				Client: k8sClient, Scheme: k8sClient.Scheme(), NewClient: testClientFactory,
			}
		}),
		Entry("GPUAvailability", func() reconciler { return newAvailabilityReconciler(0) }),
		Entry("SSHKey", func() reconciler { return newSSHKeyReconciler() }),
		Entry("GPURental without the optional Metrics and APIReader", func() reconciler {
			r := newRentalReconciler()
			r.Metrics, r.APIReader = nil, nil
			return r
		}),
	)

	DescribeTable("refuses to start without its dependencies, naming each one",
		func(r reconciler, want string) {
			// A nil manager shows the check runs before the manager is used.
			Expect(r.SetupWithManager(nil)).To(MatchError(want))
		},
		Entry("GPURental", &GPURentalReconciler{},
			"GPURental reconciler is missing Client, Scheme, NewClient, RentalOptions, Rentals, Credentials, Clock, Recorder"),
		Entry("ProviderConfig", &ProviderConfigReconciler{},
			"ProviderConfig reconciler is missing Client, Scheme, NewClient"),
		Entry("ClusterProviderConfig", &ClusterProviderConfigReconciler{},
			"ClusterProviderConfig reconciler is missing Client, Scheme, NewClient"),
		Entry("GPUAvailability", &GPUAvailabilityReconciler{},
			"GPUAvailability reconciler is missing Client, Scheme, NewClient, RentalOptions"),
		Entry("SSHKey", &SSHKeyReconciler{},
			"SSHKey reconciler is missing Client, Scheme, NewClient, Credentials, Clock, Recorder"),
	)

	It("names only what is missing", func() {
		r := newRentalReconciler()
		r.Clock = nil
		Expect(r.validate()).To(MatchError("GPURental reconciler is missing Clock"))
	})
})
