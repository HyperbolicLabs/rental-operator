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
	"time"

	corev1 "k8s.io/api/core/v1"
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
	// APIReader reads around the cache, to explain a Secret the cache does
	// not hold (it is not labeled). Optional.
	APIReader client.Reader
}

// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=providerconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=providerconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=providerconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=gpurentals,verbs=get;list;watch

// Reconcile validates the credential and reports balance/readiness on status.
func (r *ProviderConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var pc hypv1alpha1.ProviderConfig
	if err := r.Get(ctx, req.NamespacedName, &pc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return reconcileConfig(ctx, r.Client, credentialSource{client: r.Client, uncached: r.APIReader}, r.NewClient, configView{
		obj:             &pc,
		spec:            &pc.Spec,
		status:          &pc.Status,
		ref:             hypv1alpha1.ProviderConfigReference{Kind: hypv1alpha1.ProviderConfigKind, Name: pc.Name},
		secretNamespace: pc.Namespace,
	})
}

// SetupWithManager sets up the controller with the Manager. It also watches
// referenced Secrets, so a key rotation re-validates the config promptly, and
// GPURentals, so the in-use count and deletion follow them.
func (r *ProviderConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hypv1alpha1.ProviderConfig{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.providerConfigsForSecret)).
		Watches(&hypv1alpha1.GPURental{}, handler.EnqueueRequestsFromMapFunc(configOfRental(false)),
			builder.WithPredicates(rentalUsageChanged)).
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

// reconcileConfig validates a config's credentials and reports its balance
// and in-use count. It holds the config with the in-use finalizer while
// GPURentals use it.
func reconcileConfig(ctx context.Context, c client.Client, creds credentialSource, newClient ClientFactory, v configView) (ctrl.Result, error) {
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
	users, err := rentalsUsing(ctx, c, v)
	if err != nil {
		return ctrl.Result{}, err
	}
	v.status.ManagedRentals = int32(len(users))

	rc, err := creds.fromSpec(ctx, v.spec, v.secretNamespace)
	if err != nil {
		log.Info("credential not resolvable", "error", err.Error())
		reason := "SecretError"
		if errors.Is(err, errSecretNotLabeled) {
			reason = "SecretNotLabeled"
		}
		return finishConfigNotReady(ctx, c, v, reason, err.Error())
	}

	hc, err := rc.newClient(newClient)
	if err != nil {
		log.Info("cannot build an API client", "error", err.Error())
		return finishConfigNotReady(ctx, c, v, "InvalidEndpoint", err.Error())
	}

	bal, err := getBalance(ctx, hc)
	if err != nil {
		reason := hypv1alpha1.ReasonAPIError
		if hyperbolic.IsUnauthorized(err) {
			reason = "Unauthenticated"
		}
		log.Info("credential validation failed", "reason", reason, "error", err.Error())
		return finishConfigNotReady(ctx, c, v, reason, err.Error())
	}

	now := metav1.Now()
	gen := v.obj.GetGeneration()
	v.status.BalanceCents = ptr(bal.BalanceCents)
	v.status.MaxOverdraftCents = ptr(bal.MaxOverdraftCents)
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
	users, err := rentalsUsing(ctx, c, v)
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

// rentalsUsing returns the GPURentals (as namespace/name for a cluster
// config, name otherwise) that use the config and may still be billing (or
// start to): every one that is not a terminated record.
func rentalsUsing(ctx context.Context, c client.Client, v configView) ([]string, error) {
	var list hypv1alpha1.GPURentalList
	var opts []client.ListOption
	if !v.ref.IsCluster() {
		opts = append(opts, client.InNamespace(v.obj.GetNamespace()))
	}
	if err := c.List(ctx, &list, opts...); err != nil {
		return nil, err
	}
	var names []string
	for i := range list.Items {
		gr := &list.Items[i]
		if !refersTo(gr.Spec.ProviderConfigRef, v.ref) || gr.Status.TerminatedAt != nil {
			continue
		}
		if v.ref.IsCluster() {
			names = append(names, gr.Namespace+"/"+gr.Name)
		} else {
			names = append(names, gr.Name)
		}
	}
	slices.Sort(names)
	return names, nil
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
