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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = It("accepts every sample in config/samples", func(ctx context.Context) {
	files, err := filepath.Glob(filepath.Join("..", "..", "config", "samples", "v1alpha1_*.yaml"))
	Expect(err).NotTo(HaveOccurred())
	Expect(files).NotTo(BeEmpty())

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "samples-"}}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	decoder := serializer.NewCodecFactory(k8sClient.Scheme()).UniversalDeserializer()
	for _, f := range files {
		data, err := os.ReadFile(f)
		Expect(err).NotTo(HaveOccurred())
		obj, _, err := decoder.Decode(data, nil, nil)
		Expect(err).NotTo(HaveOccurred(), f)
		o := obj.(client.Object)
		if o.GetNamespace() != "" {
			o.SetNamespace(ns.Name)
		}
		Expect(k8sClient.Create(ctx, o)).To(Succeed(), "the API server rejected %s", f)
	}
})

// Artifact Hub shows the chart's CRDs and examples from Chart.yaml
// annotations. They must cover every CRD and stay valid as the CRDs change.
var _ = It("lists every CRD in the chart's Artifact Hub annotations, with examples the API server accepts", func(ctx context.Context) {
	data, err := os.ReadFile(filepath.Join("..", "..", "charts", "rental-operator", "Chart.yaml"))
	Expect(err).NotTo(HaveOccurred())
	var chart struct {
		Annotations map[string]string `json:"annotations"`
	}
	Expect(yaml.Unmarshal(data, &chart)).To(Succeed())

	var crds []struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	Expect(yaml.Unmarshal([]byte(chart.Annotations["artifacthub.io/crds"]), &crds)).To(Succeed())
	bases, err := filepath.Glob(filepath.Join("..", "..", "config", "crd", "bases", "*.yaml"))
	Expect(err).NotTo(HaveOccurred())
	var want, listed []string
	for _, f := range bases {
		// compute.hyperbolic.ai_gpurentals.yaml is gpurentals.compute.hyperbolic.ai.
		group, plural, _ := strings.Cut(strings.TrimSuffix(filepath.Base(f), ".yaml"), "_")
		want = append(want, plural+"."+group)
	}
	kinds := map[string]bool{}
	for _, c := range crds {
		listed = append(listed, c.Name)
		kinds[c.Kind] = true
	}
	Expect(listed).To(ConsistOf(want))

	var examples []map[string]any
	Expect(yaml.Unmarshal([]byte(chart.Annotations["artifacthub.io/crdsExamples"]), &examples)).To(Succeed())
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "chart-examples-"}}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	decoder := serializer.NewCodecFactory(k8sClient.Scheme()).UniversalDeserializer()
	examined := map[string]bool{}
	for _, example := range examples {
		raw, err := json.Marshal(example)
		Expect(err).NotTo(HaveOccurred())
		obj, gvk, err := decoder.Decode(raw, nil, nil)
		Expect(err).NotTo(HaveOccurred(), string(raw))
		examined[gvk.Kind] = true
		o := obj.(client.Object)
		namespaced, err := k8sClient.IsObjectNamespaced(o)
		Expect(err).NotTo(HaveOccurred())
		if namespaced {
			o.SetNamespace(ns.Name)
		} else {
			// Cluster-scoped names are shared with the other specs.
			o.SetGenerateName(o.GetName() + "-")
			o.SetName("")
		}
		Expect(k8sClient.Create(ctx, o)).To(Succeed(), "the API server rejected the %s example", gvk.Kind)
	}
	Expect(examined).To(Equal(kinds), "every listed CRD has one example")
})
