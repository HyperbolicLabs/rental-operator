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
	"errors"
	"fmt"
	"slices"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/account"
)

// configView adapts a ProviderConfig or ClusterProviderConfig for the
// reconcile logic both share.
type configView struct {
	obj    client.Object
	spec   *hypv1alpha1.ProviderConfigSpec
	status *hypv1alpha1.ProviderConfigStatus
	// ref is how GPURentals refer to this config.
	ref hypv1alpha1.ProviderConfigReference
	// secretNamespace is where the config's credentials Secret lives.
	secretNamespace string
}

// reconcileConfig validates a config's credentials and reports the in-use
// count, and the account's balance and active rentals as the API reports them.
// It holds the config with the in-use finalizer while GPURentals use it.
func reconcileConfig(ctx context.Context, c client.Client, creds account.Resolver, newClient account.ClientFactory, v configView) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !v.obj.GetDeletionTimestamp().IsZero() {
		return reconcileConfigDelete(ctx, c, v)
	}
	if !controllerutil.ContainsFinalizer(v.obj, hypv1alpha1.FinalizerInUse) {
		controllerutil.AddFinalizer(v.obj, hypv1alpha1.FinalizerInUse)
		if err := c.Update(ctx, v.obj); err != nil {
			return ctrl.Result{}, err
		}
	}
	users, err := rentalsOnConfig(ctx, c, v.obj.GetNamespace(), v.ref)
	if err != nil {
		return ctrl.Result{}, err
	}
	v.status.ManagedRentals = int32(len(users))

	rc, err := creds.FromSpec(ctx, v.spec, v.secretNamespace)
	if err != nil {
		log.Info("credential not resolvable", "error", err.Error())
		reason := "SecretError"
		if errors.Is(err, account.ErrSecretNotLabeled) {
			reason = "SecretNotLabeled"
		}
		return finishConfigNotReady(ctx, c, v, reason, err.Error())
	}

	hc, err := rc.Client(newClient)
	if err != nil {
		log.Info("cannot build an API client", "error", err.Error())
		return finishConfigNotReady(ctx, c, v, "InvalidEndpoint", err.Error())
	}

	bal, err := account.GetBalance(ctx, hc)
	var active *hyperbolic.ActiveRentalSummary
	if err == nil {
		active, err = hc.Rentals.ActiveSummary(ctx)
	}
	if err != nil {
		reason := hypv1alpha1.ReasonAPIError
		if hyperbolic.IsUnauthorized(err) {
			reason = "Unauthenticated"
		}
		log.Info("reading the account failed", "reason", reason, "error", err.Error())
		return finishConfigNotReady(ctx, c, v, reason, err.Error())
	}

	now := metav1.Now()
	gen := v.obj.GetGeneration()
	v.status.BalanceCents = ptr(bal.BalanceCents)
	v.status.MaxOverdraftCents = ptr(bal.MaxOverdraftCents)
	v.status.ActiveRentals = ptr(int32(active.Count))
	v.status.CostCentsPerHour = ptr(int64(active.CostPerHourCents))
	v.status.LastChecked = &now
	v.status.ObservedGeneration = gen
	apimeta.SetStatusCondition(&v.status.Conditions, metav1.Condition{
		Type: hypv1alpha1.ConditionAuthenticated, Status: metav1.ConditionTrue,
		Reason: "Authenticated", Message: "API key authenticated", ObservedGeneration: gen,
	})
	apimeta.SetStatusCondition(&v.status.Conditions, metav1.Condition{
		Type: hypv1alpha1.ConditionReady, Status: metav1.ConditionTrue,
		Reason: "Ready", Message: "Credential valid", ObservedGeneration: gen,
	})
	apimeta.SetStatusCondition(&v.status.Conditions, metav1.Condition{
		Type: hypv1alpha1.ConditionSynced, Status: metav1.ConditionTrue,
		Reason: hypv1alpha1.ReasonReconcileSuccess, Message: "Last reconcile reached the Hyperbolic API",
		ObservedGeneration: gen,
	})
	if err := c.Status().Update(ctx, v.obj); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: providerConfigRecheck}, nil
}

// finishConfigNotReady records a failed validation and retries later.
func finishConfigNotReady(ctx context.Context, c client.Client, v configView, reason, msg string) (ctrl.Result, error) {
	gen := v.obj.GetGeneration()
	for _, t := range []string{hypv1alpha1.ConditionAuthenticated, hypv1alpha1.ConditionReady, hypv1alpha1.ConditionSynced} {
		apimeta.SetStatusCondition(&v.status.Conditions, metav1.Condition{
			Type: t, Status: metav1.ConditionFalse, Reason: reason, Message: msg, ObservedGeneration: gen,
		})
	}
	v.status.ObservedGeneration = gen
	if err := c.Status().Update(ctx, v.obj); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: providerConfigRetry}, nil
}

// reconcileConfigDelete removes the in-use finalizer once no GPURental that
// may still be billing uses this config.
func reconcileConfigDelete(ctx context.Context, c client.Client, v configView) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(v.obj, hypv1alpha1.FinalizerInUse) {
		return ctrl.Result{}, nil
	}
	users, err := rentalsOnConfig(ctx, c, v.obj.GetNamespace(), v.ref)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(users) > 0 {
		shown := rentalNames(users, v.ref.IsCluster())
		if len(shown) > 5 {
			shown = append(shown[:5:5], fmt.Sprintf("and %d more", len(users)-5))
		}
		msg := fmt.Sprintf("Deletion waits until the %d GPURental(s) using this config are gone: %s",
			len(users), strings.Join(shown, ", "))
		v.status.ManagedRentals = int32(len(users))
		apimeta.SetStatusCondition(&v.status.Conditions, metav1.Condition{
			Type: hypv1alpha1.ConditionReady, Status: metav1.ConditionFalse,
			Reason: "InUse", Message: msg, ObservedGeneration: v.obj.GetGeneration(),
		})
		if err := c.Status().Update(ctx, v.obj); err != nil {
			return ctrl.Result{}, err
		}
		// GPURental changes requeue this config. The interval is a backstop.
		return ctrl.Result{RequeueAfter: providerConfigRetry}, nil
	}
	controllerutil.RemoveFinalizer(v.obj, hypv1alpha1.FinalizerInUse)
	if err := c.Update(ctx, v.obj); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// rentalsOnConfig returns the GPURentals that use the config ref (in
// namespace, unless it is a cluster config) and may still be billing (or
// start to): every one that is not a terminated record.
func rentalsOnConfig(ctx context.Context, c client.Client, namespace string, ref hypv1alpha1.ProviderConfigReference) ([]hypv1alpha1.GPURental, error) {
	var list hypv1alpha1.GPURentalList
	var opts []client.ListOption
	if !ref.IsCluster() {
		opts = append(opts, client.InNamespace(namespace))
	}
	if err := c.List(ctx, &list, opts...); err != nil {
		return nil, err
	}
	var out []hypv1alpha1.GPURental
	for _, gr := range list.Items {
		if refersTo(gr.Spec.ProviderConfigRef, ref) && gr.Status.TerminatedAt == nil {
			out = append(out, gr)
		}
	}
	return out, nil
}

// rentalNames names rentals for people: namespace/name for a cluster config.
func rentalNames(rentals []hypv1alpha1.GPURental, cluster bool) []string {
	names := make([]string, 0, len(rentals))
	for _, gr := range rentals {
		if cluster {
			names = append(names, gr.Namespace+"/"+gr.Name)
		} else {
			names = append(names, gr.Name)
		}
	}
	slices.Sort(names)
	return names
}

// refersTo reports whether ref names config (an empty kind means ProviderConfig).
func refersTo(ref, config hypv1alpha1.ProviderConfigReference) bool {
	return ref.Name == config.Name && ref.IsCluster() == config.IsCluster()
}

// configOfRental maps a GPURental to the config it uses, when that config is
// of the kind the calling controller reconciles.
func configOfRental(cluster bool) handler.MapFunc {
	return func(_ context.Context, obj client.Object) []reconcile.Request {
		gr, ok := obj.(*hypv1alpha1.GPURental)
		if !ok || gr.Spec.ProviderConfigRef.Name == "" || gr.Spec.ProviderConfigRef.IsCluster() != cluster {
			return nil
		}
		key := client.ObjectKey{Name: gr.Spec.ProviderConfigRef.Name}
		if !cluster {
			key.Namespace = gr.Namespace
		}
		return []reconcile.Request{{NamespacedName: key}}
	}
}

// configChanged passes every config event except an update that leaves the
// spec alone and does not start a deletion. The reconcile writes status after
// every read of the account. Without this filter that write would trigger
// another reconcile, and so another read, until two passes landed in the same
// second.
var configChanged = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() ||
			e.ObjectOld.GetDeletionTimestamp().IsZero() != e.ObjectNew.GetDeletionTimestamp().IsZero()
	},
}

// rentalUsageChanged passes GPURental events that change whether it uses its
// config or what the account spends: created, deleted, being deleted, rented
// (a create succeeded) or turned into a terminated record. Each of these also
// re-reads the account's balance and active rentals. Routine status updates
// (every poll) are dropped.
var rentalUsageChanged = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldGR, okOld := e.ObjectOld.(*hypv1alpha1.GPURental)
		newGR, okNew := e.ObjectNew.(*hypv1alpha1.GPURental)
		if !okOld || !okNew {
			return false
		}
		return oldGR.DeletionTimestamp.IsZero() != newGR.DeletionTimestamp.IsZero() ||
			(oldGR.Status.TerminatedAt == nil) != (newGR.Status.TerminatedAt == nil) ||
			(oldGR.Status.RentalID == "") != (newGR.Status.RentalID == "")
	},
}
