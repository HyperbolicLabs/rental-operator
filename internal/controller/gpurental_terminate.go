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

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/account"
)

// Event reasons emitted on GPURentals.
const (
	eventTerminated         = "Terminated"
	eventTerminateFailed    = "TerminateFailed"
	eventTerminationBlocked = "TerminationBlocked"
	eventOrphaned           = "Orphaned"
)

// configKey identifies the config a rental uses, for account.CredentialsMemory.
func configKey(gr *hypv1alpha1.GPURental) string {
	if gr.Spec.ProviderConfigRef.IsCluster() {
		return hypv1alpha1.ClusterProviderConfigKind + "/" + gr.Spec.ProviderConfigRef.Name
	}
	return gr.Namespace + "/" + gr.Spec.ProviderConfigRef.Name
}

// reconcileDelete terminates the rental and removes the finalizer only once
// the rental is confirmed gone, or when deletionPolicy is Orphan, or when no
// rental was ever created. It never gives up silently: a stuck finalizer is
// far cheaper than a GPU that keeps billing with nothing pointing at it.
func (r *GPURentalReconciler) reconcileDelete(ctx context.Context, gr *hypv1alpha1.GPURental, hc *hyperbolic.Client, creds account.Credentials, credErr error) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(gr, hypv1alpha1.FinalizerTerminate) {
		return ctrl.Result{}, nil
	}

	if gr.Spec.DeletionPolicy == hypv1alpha1.DeletionPolicyOrphan {
		if id := r.rentalID(gr); id != "" && gr.Status.TerminatedAt == nil {
			log.Info("deleting without terminating the rental (deletionPolicy Orphan)", "rentalId", id)
			r.Recorder.Event(gr, corev1.EventTypeNormal, eventOrphaned,
				fmt.Sprintf("Rental %s was left running (deletionPolicy: Orphan); terminate it in the Hyperbolic dashboard when you are done with it", id))
		}
		return r.removeFinalizer(ctx, gr)
	}

	if credErr != nil {
		rc, ok := r.Credentials.Recall(configKey(gr))
		if ok {
			creds = rc
			hc, credErr = rc.Client(r.NewClient)
		}
	}
	if credErr != nil && r.mayBeBilling(gr) {
		msg := fmt.Sprintf("Cannot terminate the rental before deleting this GPURental: %v. "+
			"Restore the ProviderConfig and its Secret, or set spec.deletionPolicy: Orphan "+
			"and terminate the rental in the Hyperbolic dashboard.", credErr)
		log.Info("deletion is waiting for credentials", "error", credErr.Error())
		r.Recorder.Event(gr, corev1.EventTypeWarning, eventTerminationBlocked, msg)
		r.setSynced(gr, metav1.ConditionFalse, hypv1alpha1.ReasonCredentialError, msg)
		return r.finish(ctx, gr, apiRetry)
	}

	done, res, err := r.terminate(ctx, gr, hc, creds.Key(), "GPURental deleted")
	if !done {
		return res, err
	}
	return r.removeFinalizer(ctx, gr)
}

// reconcileStop terminates the rental for spec.terminated=true and, once it
// is confirmed gone, turns the object into a read-only record.
func (r *GPURentalReconciler) reconcileStop(ctx context.Context, gr *hypv1alpha1.GPURental, hc *hyperbolic.Client, key account.Key) (ctrl.Result, error) {
	done, res, err := r.terminate(ctx, gr, hc, key, "spec.terminated=true")
	if !done {
		return res, err
	}
	r.markTerminated(gr, hypv1alpha1.ReasonTerminated, "Rental terminated (spec.terminated=true); this GPURental is now a read-only record")
	return r.finish(ctx, gr, 0)
}

// mayBeBilling reports whether a rental may exist for this object: an id was
// recorded, or a create was sent whose outcome is not known yet.
func (r *GPURentalReconciler) mayBeBilling(gr *hypv1alpha1.GPURental) bool {
	if gr.Status.TerminatedAt != nil {
		return false
	}
	_, pending := createPendingSince(gr)
	return r.rentalID(gr) != "" || pending
}

// terminate ends the object's rental and reports done once it is confirmed
// gone (or nothing was ever created). When it returns done=false, the
// returned result and error are the reconcile's outcome.
func (r *GPURentalReconciler) terminate(ctx context.Context, gr *hypv1alpha1.GPURental, hc *hyperbolic.Client, key account.Key, why string) (bool, ctrl.Result, error) {
	if !r.mayBeBilling(gr) {
		return true, ctrl.Result{}, nil
	}

	id := r.rentalID(gr)
	if id == "" {
		// A create was sent but its outcome is unknown: find it by label.
		since, _ := createPendingSince(gr)
		matches, err := r.findByLabel(ctx, hc, gr)
		if err != nil {
			r.setSynced(gr, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "list rentals: "+err.Error())
			res, err := r.finish(ctx, gr, apiRetry)
			return false, res, err
		}
		switch len(matches) {
		case 0:
			if wait := since.Add(createRequestTimeout + createSettleGrace).Sub(r.Clock.Now()); wait > 0 {
				gr.Status.Phase = hypv1alpha1.PhaseTerminating
				r.setReady(gr, metav1.ConditionFalse, hypv1alpha1.ReasonTerminating,
					"Confirming whether the last create request took effect before finishing")
				res, err := r.finish(ctx, gr, wait)
				return false, res, err
			}
			return true, ctrl.Result{}, nil // nothing was created
		case 1:
			id = matches[0].RentalID
		default:
			msg := fmt.Sprintf("%d active rentals carry label %q and the operator cannot tell which one to terminate. "+
				"Terminate them in the Hyperbolic dashboard, or set spec.deletionPolicy: Orphan.", len(matches), rentalLabel(gr))
			r.Recorder.Event(gr, corev1.EventTypeWarning, eventTerminationBlocked, msg)
			r.setReady(gr, metav1.ConditionUnknown, hypv1alpha1.ReasonCreateOutcomeUnknown, msg)
			res, err := r.finish(ctx, gr, blockedRetry)
			return false, res, err
		}
	}

	err := account.TerminateRental(ctx, hc, id, why)
	r.Rentals.Invalidate(key)
	r.Metrics.RentalTerminated(err)
	if err != nil {
		r.Recorder.Event(gr, corev1.EventTypeWarning, eventTerminateFailed, fmt.Sprintf("Terminating rental %s failed, retrying: %v", id, err))
		r.setSynced(gr, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "terminate: "+err.Error())
		res, err := r.finish(ctx, gr, apiRetry)
		return false, res, err
	}
	vm, err := r.findByRentalID(ctx, hc, id)
	if err != nil {
		r.setSynced(gr, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "list rentals: "+err.Error())
		res, err := r.finish(ctx, gr, apiRetry)
		return false, res, err
	}
	r.setSynced(gr, metav1.ConditionTrue, hypv1alpha1.ReasonReconcileSuccess, "")
	if vm != nil && vm.Status != hyperbolic.RentalStatusTerminated {
		gr.Status.Phase = hypv1alpha1.PhaseTerminating
		r.setReady(gr, metav1.ConditionFalse, hypv1alpha1.ReasonTerminating, "Waiting for the rental to finish terminating")
		res, err := r.finish(ctx, gr, pendingPoll)
		return false, res, err
	}
	r.Recorder.Event(gr, corev1.EventTypeNormal, eventTerminated, fmt.Sprintf("Rental %s terminated (%s)", id, why))
	return true, ctrl.Result{}, nil
}

func (r *GPURentalReconciler) removeFinalizer(ctx context.Context, gr *hypv1alpha1.GPURental) (ctrl.Result, error) {
	controllerutil.RemoveFinalizer(gr, hypv1alpha1.FinalizerTerminate)
	if err := r.Update(ctx, gr); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}
