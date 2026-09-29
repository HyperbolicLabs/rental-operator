/*
Copyright 2026.

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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"

	hypv1alpha1 "github.com/HyperbolicLabs/hyperbolic-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/hyperbolic-operator/internal/hyperbolic"
)

const (
	// providerConfigRecheck is how often a healthy credential is re-validated.
	providerConfigRecheck = 10 * time.Minute
	// providerConfigRetry is the backoff after a validation failure.
	providerConfigRetry = 2 * time.Minute
)

// ProviderConfigReconciler reconciles a ProviderConfig object.
type ProviderConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=hyperbolic.ai,resources=providerconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hyperbolic.ai,resources=providerconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hyperbolic.ai,resources=providerconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile validates the credential and reports balance/readiness on status.
func (r *ProviderConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var pc hypv1alpha1.ProviderConfig
	if err := r.Get(ctx, req.NamespacedName, &pc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	creds, err := resolveProviderConfigCreds(ctx, r.Client, &pc)
	if err != nil {
		log.Info("credential not resolvable", "error", err.Error())
		r.setNotReady(&pc, "SecretError", err.Error())
		if statusErr := r.Status().Update(ctx, &pc); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: providerConfigRetry}, nil
	}

	bal, err := creds.newClient().GetBalance(ctx)
	if err != nil {
		reason := "APIError"
		if code := hyperbolic.StatusCodeOf(err); code == 401 || code == 403 {
			reason = "Unauthenticated"
		}
		log.Info("credential validation failed", "reason", reason, "error", err.Error())
		r.setNotReady(&pc, reason, err.Error())
		if statusErr := r.Status().Update(ctx, &pc); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: providerConfigRetry}, nil
	}

	now := metav1.Now()
	pc.Status.BalanceCents = ptr(bal.BalanceCents)
	pc.Status.MaxOverdraftCents = ptr(bal.MaxOverdraftCents)
	pc.Status.LastChecked = &now
	pc.Status.ObservedGeneration = pc.Generation
	apimeta.SetStatusCondition(&pc.Status.Conditions, metav1.Condition{
		Type: hypv1alpha1.ConditionAuthenticated, Status: metav1.ConditionTrue,
		Reason: "Authenticated", Message: "API key authenticated", ObservedGeneration: pc.Generation,
	})
	apimeta.SetStatusCondition(&pc.Status.Conditions, metav1.Condition{
		Type: hypv1alpha1.ConditionReady, Status: metav1.ConditionTrue,
		Reason: "Ready", Message: "Credential valid", ObservedGeneration: pc.Generation,
	})
	if err := r.Status().Update(ctx, &pc); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: providerConfigRecheck}, nil
}

func (r *ProviderConfigReconciler) setNotReady(pc *hypv1alpha1.ProviderConfig, reason, msg string) {
	apimeta.SetStatusCondition(&pc.Status.Conditions, metav1.Condition{
		Type: hypv1alpha1.ConditionAuthenticated, Status: metav1.ConditionFalse,
		Reason: reason, Message: msg, ObservedGeneration: pc.Generation,
	})
	apimeta.SetStatusCondition(&pc.Status.Conditions, metav1.Condition{
		Type: hypv1alpha1.ConditionReady, Status: metav1.ConditionFalse,
		Reason: reason, Message: msg, ObservedGeneration: pc.Generation,
	})
	pc.Status.ObservedGeneration = pc.Generation
}

// SetupWithManager sets up the controller with the Manager, also watching
// referenced Secrets so a key rotation re-validates the config promptly.
func (r *ProviderConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hypv1alpha1.ProviderConfig{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.providerConfigsForSecret)).
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
		if list.Items[i].Spec.APIKeySecretRef.Name == obj.GetName() {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
		}
	}
	return reqs
}
