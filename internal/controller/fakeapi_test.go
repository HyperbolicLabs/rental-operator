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
	"time"

	"github.com/HyperbolicLabs/hyperbolic-go/hyperbolictest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

// testClientFactory builds API clients the way the manager does.
var testClientFactory = NewClientFactory("rental-operator/test")

// fakeAccount is a namespace wired to an in-memory Hyperbolic API: a Secret
// holding the fake's API key and a ProviderConfig pointing at the fake.
type fakeAccount struct {
	api       *hyperbolictest.Server
	namespace string
	config    hypv1alpha1.LocalRef
}

// newFakeAccount starts a fake API (closed when the spec ends) and creates a
// fresh namespace with a Secret and a ProviderConfig named "default" for it.
func newFakeAccount(ctx context.Context) *fakeAccount {
	api := hyperbolictest.NewServer(GinkgoTB())

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "fake-account-"}}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "hyperbolic-key", Namespace: ns.Name},
		StringData: map[string]string{"api-key": api.APIKey},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())

	pc := &hypv1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: ns.Name},
		Spec: hypv1alpha1.ProviderConfigSpec{
			Credentials: hypv1alpha1.Credentials{SecretRef: hypv1alpha1.SecretKeySelector{Name: secret.Name}},
			Endpoint:    api.URL,
		},
	}
	Expect(k8sClient.Create(ctx, pc)).To(Succeed())

	return &fakeAccount{api: api, namespace: ns.Name, config: hypv1alpha1.LocalRef{Name: pc.Name}}
}

// rental returns an unsaved GPURental for one H100 (the cheapest offering in
// the fake's default catalog) in the account's namespace, using the
// account's ProviderConfig. Change its spec for other shapes.
func (a *fakeAccount) rental(name string) *hypv1alpha1.GPURental {
	return &hypv1alpha1.GPURental{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: a.namespace},
		Spec: hypv1alpha1.GPURentalSpec{
			ProviderConfigRef: a.config,
			GPUType:           "h100",
			GPUCount:          1,
		},
	}
}

// newRentalReconciler returns a GPURentalReconciler wired to envtest and to
// whichever fake API a ProviderConfig points at.
func newRentalReconciler() *GPURentalReconciler {
	return &GPURentalReconciler{
		Client:      k8sClient,
		Scheme:      k8sClient.Scheme(),
		Catalog:     NewCatalogCache(time.Minute),
		NewClient:   testClientFactory,
		Credentials: NewCredentialMemory(),
	}
}

// createRental creates gr and reconciles it until the fake API holds its
// rental (finalizer, then create). It returns the object's key and the
// rental's id.
func (a *fakeAccount) createRental(ctx context.Context, r reconcile.Reconciler, gr *hypv1alpha1.GPURental) (types.NamespacedName, string) {
	GinkgoHelper()
	Expect(k8sClient.Create(ctx, gr)).To(Succeed())
	key := types.NamespacedName{Namespace: gr.Namespace, Name: gr.Name}
	reconcileOnce(ctx, r, key)
	reconcileOnce(ctx, r, key)
	Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
	Expect(gr.Status.RentalID).NotTo(BeEmpty(), "the rental was not created: %+v", gr.Status.Conditions)
	return key, gr.Status.RentalID
}

// reconcileOnce runs one reconcile of key and fails the spec on error.
func reconcileOnce(ctx context.Context, r reconcile.Reconciler, key types.NamespacedName) ctrl.Result {
	GinkgoHelper()
	res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
	Expect(err).NotTo(HaveOccurred())
	return res
}
