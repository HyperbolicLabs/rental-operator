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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = DescribeTable("ParseWatchNamespaces",
	func(flag string, want []string) {
		Expect(ParseWatchNamespaces(flag)).To(Equal(want))
	},
	Entry("empty watches everything", "", nil),
	Entry("one namespace", "ml", []string{"ml"}),
	Entry("trims, drops blanks and duplicates", " ml, research,,ml ", []string{"ml", "research"}),
)

var _ = Describe("A namespaced install", func() {
	ctx := context.Background()

	It("only sees the namespaces it watches", func() {
		watched, other := newFakeAccount(ctx), newFakeAccount(ctx) // both have a labeled Secret
		opts := ManagerCacheOptions([]string{watched.namespace})
		Expect(opts.DefaultNamespaces).To(HaveKey(watched.namespace))
		opts.Scheme = k8sClient.Scheme()
		informers, err := cache.New(cfg, opts)
		Expect(err).NotTo(HaveOccurred())
		cacheCtx, stop := context.WithCancel(ctx)
		DeferCleanup(stop)
		go func() { _ = informers.Start(cacheCtx) }()

		var list corev1.SecretList
		// Reads fail until the goroutine above has started the cache.
		Eventually(func() error { return informers.List(ctx, &list) }).Should(Succeed())
		for _, s := range list.Items {
			Expect(s.Namespace).To(Equal(watched.namespace))
		}
		Expect(list.Items).NotTo(BeEmpty())

		err = informers.Get(ctx, client.ObjectKey{Namespace: other.namespace, Name: "hyperbolic-key"}, &corev1.Secret{})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsNotFound(err)).To(BeFalse(), "a namespaced cache refuses other namespaces outright")
	})
})
