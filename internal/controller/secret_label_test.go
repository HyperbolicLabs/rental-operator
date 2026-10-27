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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/scope"
)

var _ = Describe("Credential Secrets must be labeled", func() {
	ctx := context.Background()
	var (
		acct   *fakeAccount
		secret *corev1.Secret
		pcKey  client.ObjectKey
	)

	BeforeEach(func() {
		acct = newFakeAccount(ctx)
		pcKey = client.ObjectKey{Namespace: acct.namespace, Name: acct.config.Name}
		secret = &corev1.Secret{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: acct.namespace, Name: "hyperbolic-key"}, secret)).To(Succeed())
		delete(secret.Labels, hypv1alpha1.LabelCredentials)
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())
	})

	readyOf := func(r *ProviderConfigReconciler) metav1.Condition {
		GinkgoHelper()
		reconcileOnce(ctx, r, pcKey)
		pc := &hypv1alpha1.ProviderConfig{}
		Expect(k8sClient.Get(ctx, pcKey, pc)).To(Succeed())
		return conditionOf(pc.Status.Conditions, hypv1alpha1.ConditionReady)
	}

	It("tells the person to label an unlabeled Secret, then accepts it", func() {
		r := newConfigReconciler()
		ready := readyOf(r)
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Reason).To(Equal("SecretNotLabeled"))
		Expect(ready.Message).To(ContainSubstring("kubectl label secret -n " + acct.namespace + " hyperbolic-key " + hypv1alpha1.LabelCredentials + "=true"))

		secret.Labels = map[string]string{hypv1alpha1.LabelCredentials: "true"}
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())
		Expect(readyOf(r).Status).To(Equal(metav1.ConditionTrue))
	})

	It("blocks a GPURental whose Secret is not labeled", func() {
		r := newRentalReconciler()
		gr := acct.rental("unlabeled")
		Expect(k8sClient.Create(ctx, gr)).To(Succeed())
		key := client.ObjectKeyFromObject(gr)
		reconcileOnce(ctx, r, key)
		reconcileOnce(ctx, r, key)

		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		ready := conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionReady)
		Expect(ready.Reason).To(Equal(hypv1alpha1.ReasonCredentialError))
		Expect(ready.Message).To(ContainSubstring(hypv1alpha1.LabelCredentials))
		Expect(acct.api.ActiveRentals()).To(BeEmpty())
	})

	Context("through the manager's cache", func() {
		var informers cache.Cache

		BeforeEach(func() {
			opts := scope.CacheOptions(nil, "")
			opts.Scheme = k8sClient.Scheme()
			var err error
			informers, err = cache.New(cfg, opts)
			Expect(err).NotTo(HaveOccurred())
			cacheCtx, stop := context.WithCancel(ctx)
			DeferCleanup(stop)
			go func() { _ = informers.Start(cacheCtx) }()
			// Reading a Secret starts its informer. Wait for it to sync.
			Expect(informers.Get(ctx, client.ObjectKeyFromObject(secret), &corev1.Secret{})).NotTo(Succeed())
			Expect(informers.WaitForCacheSync(ctx)).To(BeTrue())
		})

		It("holds only labeled Secrets", func() {
			unlabeled := &corev1.Secret{}
			err := informers.Get(ctx, client.ObjectKeyFromObject(secret), unlabeled)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())

			secret.Labels = map[string]string{hypv1alpha1.LabelCredentials: "true"}
			Expect(k8sClient.Update(ctx, secret)).To(Succeed())
			Eventually(func() error {
				return informers.Get(ctx, client.ObjectKeyFromObject(secret), &corev1.Secret{})
			}).Should(Succeed())
		})

		It("still says the Secret needs the label, not that it is missing", func() {
			cached, err := client.New(cfg, client.Options{Scheme: k8sClient.Scheme(), Cache: &client.CacheOptions{Reader: informers}})
			Expect(err).NotTo(HaveOccurred())
			r := newConfigReconciler()
			r.Client, r.APIReader = cached, k8sClient

			Expect(readyOf(r).Reason).To(Equal("SecretNotLabeled"))
		})
	})
})

var _ = It("configures the cache to hold only labeled Secrets", func() {
	var selectors []string
	for obj, byObject := range scope.CacheOptions(nil, "").ByObject {
		if _, isSecret := obj.(*corev1.Secret); isSecret {
			selectors = append(selectors, byObject.Label.String())
		}
	}
	Expect(selectors).To(ConsistOf(hypv1alpha1.LabelCredentials + "=true"))
})
