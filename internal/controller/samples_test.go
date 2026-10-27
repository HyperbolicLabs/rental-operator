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
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
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
