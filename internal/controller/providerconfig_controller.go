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
	"slices"
	"strings"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
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
	// NewClient builds Hyperbolic API clients. Optional, and nil uses
	// NewClientFactory with the operator's version.
	NewClient ClientFactory
}

// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=providerconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=providerconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=providerconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=gpurentals,verbs=get;list;watch

// Reconcile validates the credential and reports balance/readiness on status.
func (r *ProviderConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var pc hypv1alpha1.ProviderConfig
	if err := r.Get(ctx, req.NamespacedName, &pc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !pc.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &pc)
	}
	if !controllerutil.ContainsFinalizer(&pc, hypv1alpha1.FinalizerInUse) {
		controllerutil.AddFinalizer(&pc, hypv1alpha1.FinalizerInUse)
		if err := r.Update(ctx, &pc); err != nil {
			return ctrl.Result{}, err
		}
	}
	users, err := r.rentalsUsing(ctx, &pc)
	if err != nil {
		return ctrl.Result{}, err
	}
	pc.Status.ManagedRentals = int32(len(users))

	creds, err := resolveProviderConfigCreds(ctx, r.Client, &pc)
	if err != nil {
		log.Info("credential not resolvable", "error", err.Error())
		r.setNotReady(&pc, "SecretError", err.Error())
		if statusErr := r.Status().Update(ctx, &pc); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: providerConfigRetry}, nil
	}

	hc, err := creds.newClient(r.NewClient)
	if err != nil {
		log.Info("cannot build an API client", "error", err.Error())
		r.setNotReady(&pc, "InvalidEndpoint", err.Error())
		if statusErr := r.Status().Update(ctx, &pc); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: providerConfigRetry}, nil
	}

	bal, err := getBalance(ctx, hc)
	if err != nil {
		reason := hypv1alpha1.ReasonAPIError
		if hyperbolic.IsUnauthorized(err) {
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
	apimeta.SetStatusCondition(&pc.Status.Conditions, metav1.Condition{
		Type: hypv1alpha1.ConditionSynced, Status: metav1.ConditionTrue,
		Reason: hypv1alpha1.ReasonReconcileSuccess, Message: "Last reconcile reached the Hyperbolic API",
		ObservedGeneration: pc.Generation,
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
	apimeta.SetStatusCondition(&pc.Status.Conditions, metav1.Condition{
		Type: hypv1alpha1.ConditionSynced, Status: metav1.ConditionFalse,
		Reason: reason, Message: msg, ObservedGeneration: pc.Generation,
	})
	pc.Status.ObservedGeneration = pc.Generation
}

// reconcileDelete removes the in-use finalizer once no GPURental that may
// still be billing uses this config.
func (r *ProviderConfigReconciler) reconcileDelete(ctx context.Context, pc *hypv1alpha1.ProviderConfig) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(pc, hypv1alpha1.FinalizerInUse) {
		return ctrl.Result{}, nil
	}
	users, err := r.rentalsUsing(ctx, pc)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(users) > 0 {
		shown := users
		if len(shown) > 5 {
			shown = append(shown[:5:5], fmt.Sprintf("and %d more", len(users)-5))
		}
		msg := fmt.Sprintf("Deletion waits until the %d GPURental(s) using this config are gone: %s",
			len(users), strings.Join(shown, ", "))
		pc.Status.ManagedRentals = int32(len(users))
		apimeta.SetStatusCondition(&pc.Status.Conditions, metav1.Condition{
			Type: hypv1alpha1.ConditionReady, Status: metav1.ConditionFalse,
			Reason: "InUse", Message: msg, ObservedGeneration: pc.Generation,
		})
		if err := r.Status().Update(ctx, pc); err != nil {
			return ctrl.Result{}, err
		}
		// GPURental changes requeue this config. The interval is a backstop.
		return ctrl.Result{RequeueAfter: providerConfigRetry}, nil
	}
	controllerutil.RemoveFinalizer(pc, hypv1alpha1.FinalizerInUse)
	if err := r.Update(ctx, pc); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// rentalsUsing returns the names of the GPURentals that use pc and may still
// be billing (or start to): every one that is not a terminated record.
func (r *ProviderConfigReconciler) rentalsUsing(ctx context.Context, pc *hypv1alpha1.ProviderConfig) ([]string, error) {
	var list hypv1alpha1.GPURentalList
	if err := r.List(ctx, &list, client.InNamespace(pc.Namespace)); err != nil {
		return nil, err
	}
	var names []string
	for i := range list.Items {
		gr := &list.Items[i]
		if gr.Spec.ProviderConfigRef.Name == pc.Name && gr.Status.TerminatedAt == nil {
			names = append(names, gr.Name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// SetupWithManager sets up the controller with the Manager. It also watches
// referenced Secrets, so a key rotation re-validates the config promptly, and
// GPURentals, so the in-use count and deletion follow them.
func (r *ProviderConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hypv1alpha1.ProviderConfig{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.providerConfigsForSecret)).
		Watches(&hypv1alpha1.GPURental{}, handler.EnqueueRequestsFromMapFunc(providerConfigOfRental),
			builder.WithPredicates(rentalUsageChanged)).
		Named("providerconfig").
		Complete(r)
}

// providerConfigOfRental maps a GPURental to the ProviderConfig it uses.
func providerConfigOfRental(_ context.Context, obj client.Object) []reconcile.Request {
	gr, ok := obj.(*hypv1alpha1.GPURental)
	if !ok || gr.Spec.ProviderConfigRef.Name == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: gr.Namespace, Name: gr.Spec.ProviderConfigRef.Name}}}
}

// rentalUsageChanged passes GPURental events that change whether it uses its
// config: created, deleted, being deleted, or turned into a terminated
// record. Routine status updates (every poll) are dropped, because each
// config reconcile also reads the balance.
var rentalUsageChanged = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldGR, okOld := e.ObjectOld.(*hypv1alpha1.GPURental)
		newGR, okNew := e.ObjectNew.(*hypv1alpha1.GPURental)
		if !okOld || !okNew {
			return false
		}
		return oldGR.DeletionTimestamp.IsZero() != newGR.DeletionTimestamp.IsZero() ||
			(oldGR.Status.TerminatedAt == nil) != (newGR.Status.TerminatedAt == nil)
	},
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
