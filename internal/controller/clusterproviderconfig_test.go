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

	"github.com/HyperbolicLabs/hyperbolic-go/hyperbolictest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/scope"
)

// clusterAccount is a ClusterProviderConfig whose Secret lives in an
// "operator" namespace, used from a separate tenant namespace.
type clusterAccount struct {
	api        *hyperbolictest.Server
	operatorNS string
	tenantNS   string
	config     hypv1alpha1.ProviderConfigReference
}

// newClusterAccount sets up a fake API, the operator namespace with its
// labeled Secret (unless withSecret is false), an unlabeled tenant namespace,
// and a ClusterProviderConfig restricted by allowed.
func newClusterAccount(ctx context.Context, withSecret bool, allowed *metav1.LabelSelector) *clusterAccount {
	GinkgoHelper()
	api := hyperbolictest.NewServer(GinkgoTB())
	operatorNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "operator-"}}
	tenantNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "tenant-"}}
	Expect(k8sClient.Create(ctx, operatorNS)).To(Succeed())
	Expect(k8sClient.Create(ctx, tenantNS)).To(Succeed())
	if withSecret {
		Expect(k8sClient.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "shared-key", Namespace: operatorNS.Name,
				Labels: map[string]string{hypv1alpha1.LabelCredentials: "true"},
			},
			StringData: map[string]string{"api-key": api.APIKey},
		})).To(Succeed())
	}
	cpc := &hypv1alpha1.ClusterProviderConfig{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "shared-"},
		Spec: hypv1alpha1.ClusterProviderConfigSpec{
			ProviderConfigSpec: hypv1alpha1.ProviderConfigSpec{
				Credentials: hypv1alpha1.Credentials{SecretRef: hypv1alpha1.SecretKeySelector{Name: "shared-key"}},
				Endpoint:    api.URL,
			},
			AllowedNamespaces: allowed,
		},
	}
	Expect(k8sClient.Create(ctx, cpc)).To(Succeed())
	return &clusterAccount{
		api: api, operatorNS: operatorNS.Name, tenantNS: tenantNS.Name,
		config: hypv1alpha1.ProviderConfigReference{Kind: hypv1alpha1.ClusterProviderConfigKind, Name: cpc.Name},
	}
}

func (a *clusterAccount) rentalReconciler() *GPURentalReconciler {
	r := newRentalReconciler()
	r.ClusterResourceNamespace = a.operatorNS
	return r
}

func (a *clusterAccount) configReconciler() *ClusterProviderConfigReconciler {
	return &ClusterProviderConfigReconciler{
		Client: k8sClient, Scheme: k8sClient.Scheme(), NewClient: testClientFactory,
		ClusterResourceNamespace: a.operatorNS,
	}
}

// rentIn creates a GPURental in namespace using the cluster config and
// reconciles it twice (finalizer, then create).
func (a *clusterAccount) rentIn(ctx context.Context, r *GPURentalReconciler, namespace, name string) *hypv1alpha1.GPURental {
	GinkgoHelper()
	gr := &hypv1alpha1.GPURental{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       hypv1alpha1.GPURentalSpec{ProviderConfigRef: a.config, GPUType: "h100", GPUCount: 1},
	}
	Expect(k8sClient.Create(ctx, gr)).To(Succeed())
	reconcileOnce(ctx, r, client.ObjectKeyFromObject(gr))
	reconcileOnce(ctx, r, client.ObjectKeyFromObject(gr))
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gr), gr)).To(Succeed())
	return gr
}

var _ = Describe("ClusterProviderConfig", func() {
	ctx := context.Background()
	mlOnly := &metav1.LabelSelector{MatchLabels: map[string]string{"team": "ml"}}

	It("rents for a tenant namespace with the key from the operator's namespace", func() {
		acct := newClusterAccount(ctx, true, nil)
		gr := acct.rentIn(ctx, acct.rentalReconciler(), acct.tenantNS, "shared")

		Expect(gr.Status.RentalID).NotTo(BeEmpty())
		Expect(acct.api.ActiveRentals()).To(HaveLen(1))
	})

	It("blocks namespaces its selector does not allow, then allows them once labeled", func() {
		acct := newClusterAccount(ctx, true, mlOnly)
		r := acct.rentalReconciler()
		gr := acct.rentIn(ctx, r, acct.tenantNS, "outsider")

		Expect(gr.Status.Phase).To(Equal(hypv1alpha1.PhaseBlocked))
		Expect(conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionReady).Reason).To(Equal(hypv1alpha1.ReasonNamespaceNotAllowed))
		Expect(acct.api.Requests(http.MethodPost, hyperbolictest.PathRentals)).To(BeZero())

		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: acct.tenantNS}, ns)).To(Succeed())
		ns.Labels = map[string]string{"team": "ml"}
		Expect(k8sClient.Update(ctx, ns)).To(Succeed())
		reconcileOnce(ctx, r, client.ObjectKeyFromObject(gr))
		Expect(acct.api.ActiveRentals()).To(HaveLen(1))
	})

	It("never reads a Secret outside the operator's namespace", func() {
		acct := newClusterAccount(ctx, false, nil)
		// A Secret with the right name and a working key, but in the tenant's
		// namespace: creating a ClusterProviderConfig must not reach it.
		Expect(k8sClient.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "shared-key", Namespace: acct.tenantNS,
				Labels: map[string]string{hypv1alpha1.LabelCredentials: "true"},
			},
			StringData: map[string]string{"api-key": acct.api.APIKey},
		})).To(Succeed())

		gr := acct.rentIn(ctx, acct.rentalReconciler(), acct.tenantNS, "borrower")

		ready := conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionReady)
		Expect(ready.Reason).To(Equal(hypv1alpha1.ReasonCredentialError))
		Expect(ready.Message).To(ContainSubstring(acct.operatorNS + "/shared-key"))
		Expect(acct.api.Requests(http.MethodPost, hyperbolictest.PathRentals)).To(BeZero())
	})

	It("validates its key and is held while rentals in any namespace use it", func() {
		acct := newClusterAccount(ctx, true, nil)
		r := acct.rentalReconciler()
		gr := acct.rentIn(ctx, r, acct.tenantNS, "holder")
		cr := acct.configReconciler()
		key := client.ObjectKey{Name: acct.config.Name}

		reconcileOnce(ctx, cr, key)
		cpc := &hypv1alpha1.ClusterProviderConfig{}
		Expect(k8sClient.Get(ctx, key, cpc)).To(Succeed())
		Expect(conditionOf(cpc.Status.Conditions, hypv1alpha1.ConditionReady).Status).To(Equal(metav1.ConditionTrue))
		Expect(cpc.Status.BalanceCents).NotTo(BeNil())
		Expect(cpc.Status.ManagedRentals).To(Equal(int32(1)))

		Expect(k8sClient.Delete(ctx, cpc)).To(Succeed())
		reconcileOnce(ctx, cr, key)
		Expect(k8sClient.Get(ctx, key, cpc)).To(Succeed())
		ready := conditionOf(cpc.Status.Conditions, hypv1alpha1.ConditionReady)
		Expect(ready.Reason).To(Equal("InUse"))
		Expect(ready.Message).To(ContainSubstring(acct.tenantNS + "/holder"))

		Expect(k8sClient.Delete(ctx, gr)).To(Succeed())
		reconcileOnce(ctx, r, client.ObjectKeyFromObject(gr))
		reconcileOnce(ctx, cr, key)
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, cpc))).To(BeTrue())
		Expect(acct.api.ActiveRentals()).To(BeEmpty())
	})

	It("serves the catalog to a GPUAvailability", func() {
		acct := newClusterAccount(ctx, true, nil)
		ga := &hypv1alpha1.GPUAvailability{
			ObjectMeta: metav1.ObjectMeta{Name: "catalog", Namespace: acct.tenantNS},
			Spec:       hypv1alpha1.GPUAvailabilitySpec{ProviderConfigRef: acct.config},
		}
		Expect(k8sClient.Create(ctx, ga)).To(Succeed())
		r := newAvailabilityReconciler(0)
		r.ClusterResourceNamespace = acct.operatorNS
		reconcileOnce(ctx, r, client.ObjectKeyFromObject(ga))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ga), ga)).To(Succeed())
		Expect(ga.Status.Offerings).NotTo(BeEmpty())
	})
})

var _ = It("caches ClusterProviderConfig Secrets in the operator's namespace in a namespaced install", func() {
	opts := scope.CacheOptions([]string{"ml"}, "rental-operator-system")
	for obj, byObject := range opts.ByObject {
		if _, isSecret := obj.(*corev1.Secret); isSecret {
			Expect(byObject.Namespaces).To(HaveKey("ml"))
			Expect(byObject.Namespaces).To(HaveKey("rental-operator-system"))
		}
	}
	Expect(opts.DefaultNamespaces).NotTo(HaveKey("rental-operator-system"))
})
