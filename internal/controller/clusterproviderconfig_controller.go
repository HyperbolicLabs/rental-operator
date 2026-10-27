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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/account"
)

// ClusterProviderConfigReconciler reconciles a ClusterProviderConfig object.
//
// Every field is required unless its comment says otherwise.
// SetupWithManager rejects a reconciler that is missing one.
type ClusterProviderConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// NewClient builds Hyperbolic API clients.
	NewClient account.ClientFactory
	// APIReader reads around the cache, to explain a Secret the cache does
	// not hold because it is not labeled. Optional.
	APIReader client.Reader
	// ClusterResourceNamespace is the operator's own namespace, where
	// ClusterProviderConfig Secrets live. Optional: without it, every
	// ClusterProviderConfig reports that it cannot read its Secret.
	ClusterResourceNamespace string
}

// validate checks the reconciler's required dependencies.
func (r *ClusterProviderConfigReconciler) validate() error {
	return checkDependencies("ClusterProviderConfig", apiDependencies(r.Client, r.Scheme, r.NewClient)...)
}

// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=clusterproviderconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=clusterproviderconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=clusterproviderconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// Reconcile validates the credential and reports balance/readiness on status.
func (r *ClusterProviderConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cpc hypv1alpha1.ClusterProviderConfig
	if err := r.Get(ctx, req.NamespacedName, &cpc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	creds := account.Resolver{Client: r.Client, Uncached: r.APIReader, ClusterNamespace: r.ClusterResourceNamespace}
	return reconcileConfig(ctx, r.Client, creds, r.NewClient, configView{
		obj:             &cpc,
		spec:            &cpc.Spec.ProviderConfigSpec,
		status:          &cpc.Status,
		ref:             hypv1alpha1.ProviderConfigReference{Kind: hypv1alpha1.ClusterProviderConfigKind, Name: cpc.Name},
		secretNamespace: r.ClusterResourceNamespace,
	})
}

// SetupWithManager sets up the controller with the Manager. It also watches
// Secrets in the operator's namespace, and GPURentals and SSHKeys in every
// namespace.
func (r *ClusterProviderConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := r.validate(); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&hypv1alpha1.ClusterProviderConfig{}, builder.WithPredicates(configChanged)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.configsForSecret)).
		Watches(&hypv1alpha1.GPURental{}, handler.EnqueueRequestsFromMapFunc(configOf(true)),
			builder.WithPredicates(rentalUsageChanged)).
		Watches(&hypv1alpha1.SSHKey{}, handler.EnqueueRequestsFromMapFunc(configOf(true)),
			builder.WithPredicates(sshKeyUsageChanged)).
		Named("clusterproviderconfig").
		Complete(r)
}

// configsForSecret maps a Secret in the operator's namespace to the
// ClusterProviderConfigs that use it.
func (r *ClusterProviderConfigReconciler) configsForSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	if obj.GetNamespace() != r.ClusterResourceNamespace {
		return nil
	}
	var list hypv1alpha1.ClusterProviderConfigList
	if err := r.List(ctx, &list); err != nil {
		return nil
	}
	var reqs []reconcile.Request
	for i := range list.Items {
		if list.Items[i].Spec.Credentials.SecretRef.Name == obj.GetName() {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
		}
	}
	return reqs
}
