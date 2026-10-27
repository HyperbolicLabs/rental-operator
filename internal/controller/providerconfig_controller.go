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

const (
	// providerConfigRecheck is how often a healthy credential is re-validated.
	providerConfigRecheck = 10 * time.Minute
	// providerConfigRetry is the backoff after a validation failure.
	providerConfigRetry = 2 * time.Minute
)

// ProviderConfigReconciler reconciles a ProviderConfig object.
//
// Every field is required unless its comment says otherwise.
// SetupWithManager rejects a reconciler that is missing one.
type ProviderConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// NewClient builds Hyperbolic API clients.
	NewClient account.ClientFactory
	// APIReader reads around the cache, to explain a Secret the cache does
	// not hold because it is not labeled. Optional.
	APIReader client.Reader
}

// validate checks the reconciler's required dependencies.
func (r *ProviderConfigReconciler) validate() error {
	return checkDependencies("ProviderConfig", apiDependencies(r.Client, r.Scheme, r.NewClient)...)
}

// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=providerconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=providerconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=providerconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=gpurentals,verbs=get;list;watch
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=sshkeys,verbs=get;list;watch

// Reconcile validates the credential and reports balance/readiness on status.
func (r *ProviderConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var pc hypv1alpha1.ProviderConfig
	if err := r.Get(ctx, req.NamespacedName, &pc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return reconcileConfig(ctx, r.Client, account.Resolver{Client: r.Client, Uncached: r.APIReader}, r.NewClient, configView{
		obj:             &pc,
		spec:            &pc.Spec,
		status:          &pc.Status,
		ref:             hypv1alpha1.ProviderConfigReference{Kind: hypv1alpha1.ProviderConfigKind, Name: pc.Name},
		secretNamespace: pc.Namespace,
	})
}

// SetupWithManager sets up the controller with the Manager. It also watches
// referenced Secrets, so a key rotation re-validates the config promptly, and
// GPURentals and SSHKeys, so the in-use count and deletion follow them.
func (r *ProviderConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := r.validate(); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&hypv1alpha1.ProviderConfig{}, builder.WithPredicates(configChanged)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.providerConfigsForSecret)).
		Watches(&hypv1alpha1.GPURental{}, handler.EnqueueRequestsFromMapFunc(configOf(false)),
			builder.WithPredicates(rentalUsageChanged)).
		Watches(&hypv1alpha1.SSHKey{}, handler.EnqueueRequestsFromMapFunc(configOf(false)),
			builder.WithPredicates(sshKeyUsageChanged)).
		Named("providerconfig").
		Complete(r)
}

// providerConfigsForSecret maps a Secret change to the ProviderConfigs that use it.
func (r *ProviderConfigReconciler) providerConfigsForSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	var list hypv1alpha1.ProviderConfigList
	if err := r.List(ctx, &list, client.InNamespace(obj.GetNamespace())); err != nil {
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
