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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

var _ = Describe("GPURental admission", func() {
	ctx := context.Background()
	n := 0

	newRental := func(spec hypv1alpha1.GPURentalSpec) *hypv1alpha1.GPURental {
		n++
		spec.ProviderConfigRef = hypv1alpha1.LocalRef{Name: "default"}
		return &hypv1alpha1.GPURental{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("admission-%d", n), Namespace: "default"},
			Spec:       spec,
		}
	}
	create := func(gr *hypv1alpha1.GPURental) {
		Expect(k8sClient.Create(ctx, gr)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, gr)).To(Succeed()) })
	}

	DescribeTable("the rental's shape cannot change after creation",
		func(spec hypv1alpha1.GPURentalSpec, edit func(*hypv1alpha1.GPURentalSpec), message string) {
			gr := newRental(spec)
			create(gr)
			edit(&gr.Spec)
			Expect(k8sClient.Update(ctx, gr)).To(MatchError(ContainSubstring(message)))
		},
		Entry("gpuType",
			hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8},
			func(s *hypv1alpha1.GPURentalSpec) { s.GPUType = "h200" },
			"gpuType and gpuCount are immutable"),
		Entry("gpuCount",
			hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8},
			func(s *hypv1alpha1.GPURentalSpec) { s.GPUCount = 16 },
			"gpuType and gpuCount are immutable"),
		Entry("setting a region",
			hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8},
			func(s *hypv1alpha1.GPURentalSpec) { s.Region = "us-central-1" },
			"region is immutable"),
		Entry("changing the region",
			hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8, Region: "us-central-1"},
			func(s *hypv1alpha1.GPURentalSpec) { s.Region = "ca-east-1" },
			"region is immutable"),
		Entry("clearing the network type",
			hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8, NetworkType: "infiniband"},
			func(s *hypv1alpha1.GPURentalSpec) { s.NetworkType = "" },
			"networkType is immutable"),
		Entry("adding a port",
			hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8, Ports: []int32{8080}},
			func(s *hypv1alpha1.GPURentalSpec) { s.Ports = []int32{8080, 8443} },
			"ports are immutable"),
		Entry("the display name",
			hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8, DisplayName: "train-42"},
			func(s *hypv1alpha1.GPURentalSpec) { s.DisplayName = "train-43" },
			"displayName is immutable"),
		Entry("the config",
			hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8},
			func(s *hypv1alpha1.GPURentalSpec) { s.ProviderConfigRef.Name = "other" },
			"providerConfigRef is immutable"),
		Entry("terminated back to false",
			hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8, Terminated: true},
			func(s *hypv1alpha1.GPURentalSpec) { s.Terminated = false },
			"spec.terminated is one-way"),
	)

	It("accepts edits that do not change the rental's shape", func() {
		gr := newRental(hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8, Ports: []int32{8080, 8443}})
		create(gr)

		gr.Spec.MaxPriceCentsPerHour = ptr(int64(3000))
		gr.Spec.RebootGeneration = 1
		gr.Spec.Ports = []int32{8443, 8080} // same set, different order
		Expect(k8sClient.Update(ctx, gr)).To(Succeed())
	})

	It("accepts GPU types it has never heard of", func() {
		create(newRental(hypv1alpha1.GPURentalSpec{GPUType: "gb300", GPUCount: 8}))
	})

	DescribeTable("rejects malformed specs",
		func(spec hypv1alpha1.GPURentalSpec) {
			Expect(k8sClient.Create(ctx, newRental(spec))).NotTo(Succeed())
		},
		Entry("an upper-case GPU type", hypv1alpha1.GPURentalSpec{GPUType: "H100", GPUCount: 8}),
		Entry("a port out of range", hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8, Ports: []int32{70000}}),
		Entry("a duplicate port", hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8, Ports: []int32{8080, 8080}}),
		Entry("a zero price ceiling", hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8, MaxPriceCentsPerHour: ptr(int64(0))}),
	)
})
