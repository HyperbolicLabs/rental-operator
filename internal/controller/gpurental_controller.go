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
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/account"
	"github.com/HyperbolicLabs/rental-operator/internal/metrics"
)

const (
	pendingPoll = 10 * time.Second
	runningPoll = 60 * time.Second
	// blockedRetry is how often a Blocked rental (no availability, insufficient
	// balance, disabled region, unsupported ports, missing key) is retried.
	// These states clear on their own (stock returns, balance tops up) or on a
	// spec change, so a gentle 5-minute cadence avoids hammering the API.
	blockedRetry = 5 * time.Minute
	apiRetry     = 30 * time.Second
)

// GPURentalReconciler reconciles a GPURental object.
//
// Every field is required unless its comment says otherwise.
// SetupWithManager rejects a reconciler that is missing one.
type GPURentalReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// NewClient builds Hyperbolic API clients.
	NewClient account.ClientFactory
	// RentalOptions is the shared per-account rental-options cache that
	// preflight reads.
	RentalOptions *account.RentalOptionsCache
	// Rentals shares one list of active rentals per account among all
	// GPURentals.
	Rentals *account.RentalCache
	// Credentials remembers the last API key per config, so rentals can
	// still be terminated after their Secret is deleted.
	Credentials *account.CredentialsMemory
	// Clock tells the time.
	Clock clock.PassiveClock
	// Recorder emits events on GPURentals.
	Recorder record.EventRecorder
	// Metrics records create and terminate outcomes. Optional: nil records
	// nothing.
	Metrics *metrics.Recorder
	// APIReader reads around the cache, to explain a Secret the cache does
	// not hold because it is not labeled. Optional.
	APIReader client.Reader
	// ClusterResourceNamespace is the operator's own namespace, where
	// ClusterProviderConfig Secrets live. Optional: without it,
	// ClusterProviderConfigs report that they cannot read their Secret.
	ClusterResourceNamespace string
}

// validate checks the reconciler's required dependencies.
func (r *GPURentalReconciler) validate() error {
	deps := append(apiDependencies(r.Client, r.Scheme, r.NewClient),
		dependency{"RentalOptions", r.RentalOptions == nil},
		dependency{"Rentals", r.Rentals == nil},
	)
	return checkDependencies("GPURental", append(deps, createDeleteDependencies(r.Credentials, r.Clock, r.Recorder)...)...)
}

// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=gpurentals,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=gpurentals/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=gpurentals/finalizers,verbs=update
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=providerconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=clusterproviderconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=sshkeys,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile drives one GPURental toward its desired state.
func (r *GPURentalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var gr hypv1alpha1.GPURental
	if err := r.Get(ctx, req.NamespacedName, &gr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	creds, credErr := account.Resolver{Client: r.Client, Uncached: r.APIReader, ClusterNamespace: r.ClusterResourceNamespace}.
		ForReference(ctx, gr.Namespace, gr.Spec.ProviderConfigRef)
	var hc *hyperbolic.Client
	if credErr == nil {
		hc, credErr = creds.Client(r.NewClient)
	}
	if credErr == nil {
		r.Credentials.Remember(configKey(gr.Namespace, gr.Spec.ProviderConfigRef), creds)
	}

	// Deletion.
	if !gr.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &gr, hc, creds, credErr)
	}

	// Ensure finalizer.
	if !controllerutil.ContainsFinalizer(&gr, hypv1alpha1.FinalizerTerminate) {
		controllerutil.AddFinalizer(&gr, hypv1alpha1.FinalizerTerminate)
		if err := r.Update(ctx, &gr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Record: once the rental is terminated (by us or externally) the object is
	// read-only and never provisions again, whatever the spec says.
	if gr.Status.TerminatedAt != nil {
		return ctrl.Result{}, nil
	}

	if credErr != nil {
		log.Info("credentials not resolvable", "error", credErr.Error())
		reason := credentialErrorReason(credErr)
		r.setSynced(&gr, metav1.ConditionFalse, reason, credErr.Error())
		if gr.Status.RentalID == "" {
			r.setBlocked(&gr, reason, credErr.Error())
		}
		return r.finish(ctx, &gr, apiRetry)
	}

	// Explicit stop: terminate but keep the object as a record.
	if gr.Spec.Terminated {
		return r.reconcileStop(ctx, &gr, hc, creds.Key())
	}

	// Ensure the rental exists (create or adopt).
	if gr.Status.RentalID == "" {
		return r.reconcileEnsureCreated(ctx, &gr, hc, creds)
	}

	// Poll and reconcile live state.
	return r.reconcilePoll(ctx, &gr, hc, creds.Key())
}

// reconcilePoll refreshes live status and applies requested reboots.
func (r *GPURentalReconciler) reconcilePoll(ctx context.Context, gr *hypv1alpha1.GPURental, hc *hyperbolic.Client, key account.Key) (ctrl.Result, error) {
	vm, err := r.findRental(ctx, hc, key, gr.Status.RentalID)
	if err != nil {
		// Transient list error: keep the last-known phase and Ready, report it
		// on Synced, and retry. Never flip a live rental to Terminated because
		// a poll failed.
		r.setSynced(gr, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "list rentals: "+err.Error())
		return r.finish(ctx, gr, apiRetry)
	}
	r.setSynced(gr, metav1.ConditionTrue, hypv1alpha1.ReasonReconcileSuccess, "")
	if vm == nil {
		// The list succeeded and the rental is not in it: it ended without us.
		r.markTerminatedExternally(gr)
		return r.finish(ctx, gr, 0)
	}

	// Reboot if requested and possible.
	if gr.Spec.RebootGeneration > appliedRebootGen(gr) && vm.Status == hyperbolic.RentalStatusRunning {
		r.rebootInstances(ctx, hc, gr, vm)
	}

	// Keys can be attached and detached while the rental starts or runs.
	if vm.Status == hyperbolic.RentalStatusPending || vm.Status == hyperbolic.RentalStatusRunning {
		if err := r.applySSHKeys(ctx, gr, hc, vm.RentalID); err != nil {
			return ctrl.Result{}, err
		}
	}

	r.applyRentalToStatus(gr, vm)
	return r.finish(ctx, gr, pollInterval(vm.Status))
}

// rebootInstances reboots every node that supports rebooting.
func (r *GPURentalReconciler) rebootInstances(ctx context.Context, hc *hyperbolic.Client, gr *hypv1alpha1.GPURental, vm *hyperbolic.RentalWithStatus) {
	log := logf.FromContext(ctx)
	rebooted := false
	for _, inst := range rentalInstances(vm) {
		if !inst.SupportsReboot {
			continue
		}
		if _, err := hc.Rentals.RebootInstance(ctx, hyperbolic.RebootRentalInstanceParams{RentalID: vm.RentalID, InstanceRef: inst.InstanceRef}); err != nil {
			log.Info("reboot instance failed", "instanceRef", inst.InstanceRef, "error", err.Error())
			continue
		}
		rebooted = true
	}
	if rebooted {
		gen := map[string]string{hypv1alpha1.AnnotationRebootedGeneration: strconv.Itoa(int(gr.Spec.RebootGeneration))}
		if err := patchAnnotations(ctx, r.Client, gr, gen); err != nil {
			log.Info("failed to record the applied reboot generation", "error", err.Error())
		}
	}
}

// SetupWithManager sets up the controller with the Manager. It also watches
// SSHKeys, so a rental waiting for a key, or one whose keys change, is
// reconciled promptly.
func (r *GPURentalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := r.validate(); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&hypv1alpha1.GPURental{}).
		Watches(&hypv1alpha1.SSHKey{}, handler.EnqueueRequestsFromMapFunc(r.rentalsForSSHKey),
			builder.WithPredicates(sshKeyUsableChanged)).
		Named("gpurental").
		Complete(r)
}

func pollInterval(status hyperbolic.RentalStatus) time.Duration {
	switch status {
	case hyperbolic.RentalStatusRunning:
		return runningPoll
	case hyperbolic.RentalStatusTerminated, hyperbolic.RentalStatusFailed:
		return 0
	default:
		return pendingPoll
	}
}
