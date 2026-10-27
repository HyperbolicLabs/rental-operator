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
	"math"
	"strconv"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/billing"
)

// rentalPhase maps the API's rental status to the GPURental phase, renaming
// the API's pre-Running "Pending" to "Provisioning".
func rentalPhase(apiStatus hyperbolic.RentalStatus) string {
	switch apiStatus {
	case hyperbolic.RentalStatusPending:
		return hypv1alpha1.PhaseProvisioning
	case hyperbolic.RentalStatusRunning:
		return hypv1alpha1.PhaseRunning
	case hyperbolic.RentalStatusRebooting:
		return hypv1alpha1.PhaseRebooting
	case hyperbolic.RentalStatusTerminating:
		return hypv1alpha1.PhaseTerminating
	case hyperbolic.RentalStatusTerminated:
		return hypv1alpha1.PhaseTerminated
	case hyperbolic.RentalStatusFailed:
		return hypv1alpha1.PhaseFailed
	default:
		return hypv1alpha1.PhaseUnknown
	}
}

// applyRentalToStatus maps a live rental onto CR status + conditions.
func (r *GPURentalReconciler) applyRentalToStatus(gr *hypv1alpha1.GPURental, vm *hyperbolic.RentalWithStatus) {
	gr.Status.Phase = rentalPhase(vm.Status)
	gr.Status.RentalID = vm.RentalID
	if name, err := vm.Name.Get(); err == nil {
		gr.Status.Name = name
	}
	if label, err := vm.Label.Get(); err == nil {
		gr.Status.Label = label
	}
	if vm.Region != "" {
		gr.Status.Region = vm.Region
	}
	if vm.ConnectionType != "" {
		gr.Status.NetworkType = string(vm.ConnectionType)
	}
	if cost := billing.CostPerHour(vm.CurrentTerm); cost > 0 {
		// currentTerm.costPerHour is already in cents (verified live: 3192 == $31.92/hr).
		gr.Status.CostCentsPerHour = ptr(int64(math.Round(cost)))
	}
	gr.Status.OpenPorts = intsToInt32s(vm.ExposedPorts)
	gr.Status.Instances = instancesFromRental(vm)

	switch vm.Status {
	case hyperbolic.RentalStatusRunning:
		r.setReady(gr, metav1.ConditionTrue, hypv1alpha1.ReasonRunning,
			fmt.Sprintf("%d/%d instances running", runningCount(gr.Status.Instances), len(gr.Status.Instances)))
	case hyperbolic.RentalStatusPending:
		r.setReady(gr, metav1.ConditionFalse, hypv1alpha1.ReasonProvisioning, "Rental is provisioning")
	case hyperbolic.RentalStatusRebooting:
		r.setReady(gr, metav1.ConditionFalse, hypv1alpha1.ReasonRebooting, "Rental is rebooting")
	case hyperbolic.RentalStatusTerminating:
		r.setReady(gr, metav1.ConditionFalse, hypv1alpha1.ReasonTerminating, "Rental is terminating")
	case hyperbolic.RentalStatusTerminated:
		r.setReady(gr, metav1.ConditionFalse, hypv1alpha1.ReasonTerminated, "Rental is terminated")
	case hyperbolic.RentalStatusFailed:
		r.setReady(gr, metav1.ConditionFalse, hypv1alpha1.ReasonFailed, "Rental failed")
	default:
		r.setReady(gr, metav1.ConditionUnknown, hypv1alpha1.ReasonUnknown, "Rental status unknown")
	}
}

// markTerminated records that the rental is over. The object becomes a
// read-only record: it is never provisioned again.
func (r *GPURentalReconciler) markTerminated(gr *hypv1alpha1.GPURental, reason, msg string) {
	now := metav1.Now()
	gr.Status.TerminatedAt = &now
	gr.Status.Phase = hypv1alpha1.PhaseTerminated
	gr.Status.Instances = nil
	r.setReady(gr, metav1.ConditionFalse, reason, msg)
}

// markTerminatedExternally records a rental that ended without the operator
// asking. It is never rented again automatically: doing so could repeat
// whatever ended it (an empty balance) and spend money nobody asked for.
func (r *GPURentalReconciler) markTerminatedExternally(gr *hypv1alpha1.GPURental) {
	r.markTerminated(gr, hypv1alpha1.ReasonTerminatedExternally,
		"The rental is no longer active and was not terminated by this operator "+
			"(for example the balance ran out or it was ended in the dashboard). "+
			"It will not be rented again; create a new GPURental to rent again.")
}

func (r *GPURentalReconciler) setReady(gr *hypv1alpha1.GPURental, status metav1.ConditionStatus, reason, msg string) {
	apimeta.SetStatusCondition(&gr.Status.Conditions, metav1.Condition{
		Type: hypv1alpha1.ConditionReady, Status: status, Reason: reason, Message: msg,
		ObservedGeneration: gr.Generation,
	})
	gr.Status.ObservedGeneration = gr.Generation
}

// setSynced records whether this reconcile could read its credentials and
// reach the API. It never changes Ready: a failed poll says nothing about
// whether the rental is still running.
func (r *GPURentalReconciler) setSynced(gr *hypv1alpha1.GPURental, status metav1.ConditionStatus, reason, msg string) {
	if status == metav1.ConditionTrue && msg == "" {
		msg = "Last reconcile reached the Hyperbolic API"
	}
	apimeta.SetStatusCondition(&gr.Status.Conditions, metav1.Condition{
		Type: hypv1alpha1.ConditionSynced, Status: status, Reason: reason, Message: msg,
		ObservedGeneration: gr.Generation,
	})
}

// setBlocked marks the rental as unable to be created yet (phase Blocked) with
// the specific reason on the Ready condition.
func (r *GPURentalReconciler) setBlocked(gr *hypv1alpha1.GPURental, reason, msg string) {
	gr.Status.Phase = hypv1alpha1.PhaseBlocked
	r.setReady(gr, metav1.ConditionFalse, reason, msg)
}

// finish writes status and returns a requeue result.
func (r *GPURentalReconciler) finish(ctx context.Context, gr *hypv1alpha1.GPURental, requeue time.Duration) (ctrl.Result, error) {
	now := metav1.Now()
	gr.Status.LastSyncTime = &now
	if err := r.Status().Update(ctx, gr); err != nil {
		return ctrl.Result{}, err
	}
	if requeue > 0 {
		return ctrl.Result{RequeueAfter: requeue}, nil
	}
	return ctrl.Result{}, nil
}

func appliedRebootGen(gr *hypv1alpha1.GPURental) int32 {
	if v := gr.Annotations[hypv1alpha1.AnnotationRebootedGeneration]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return int32(n)
		}
	}
	return 0
}

// instancesFromRental maps the rental's nodes onto status. The SDK's
// Endpoints falls back to rental-level SSH networking for providers that do
// not report per-instance data.
func instancesFromRental(vm *hyperbolic.RentalWithStatus) []hypv1alpha1.InstanceStatus {
	endpoints := rentalEndpoints(vm)
	if len(endpoints) == 0 {
		return nil
	}
	out := make([]hypv1alpha1.InstanceStatus, 0, len(endpoints))
	for _, e := range endpoints {
		out = append(out, hypv1alpha1.InstanceStatus{
			InstanceRef: e.InstanceRef, Phase: e.Phase, SSH: e.SSH, InternalIP: e.InternalIP,
		})
	}
	return out
}

func runningCount(insts []hypv1alpha1.InstanceStatus) int {
	n := 0
	for _, i := range insts {
		if i.Phase == string(hyperbolic.RentalStatusRunning) {
			n++
		}
	}
	return n
}

// rentalInstances returns a rental's instances, or nil when the API reported
// none.
func rentalInstances(vm *hyperbolic.RentalWithStatus) []hyperbolic.RentalInstance {
	if vm.Instances == nil {
		return nil
	}
	return *vm.Instances
}

// endpoint is how to reach one node of a rental.
type endpoint struct {
	InstanceRef, Phase, SSH, InternalIP string
}

// rentalEndpoints returns one entry per node: per-instance details when the
// API reports instances, the rental's SSH endpoints otherwise.
func rentalEndpoints(vm *hyperbolic.RentalWithStatus) []endpoint {
	if instances := rentalInstances(vm); len(instances) > 0 {
		out := make([]endpoint, 0, len(instances))
		for _, i := range instances {
			ssh, _ := i.SSH.Get()
			ip, _ := i.InternalIP.Get()
			out = append(out, endpoint{InstanceRef: i.InstanceRef, Phase: i.Phase, SSH: ssh, InternalIP: ip})
		}
		return out
	}
	out := make([]endpoint, 0, len(vm.SSHEndpoints))
	for idx, s := range vm.SSHEndpoints {
		e := endpoint{
			InstanceRef: vm.RentalID + "-" + strconv.Itoa(idx),
			Phase:       string(vm.Status),
			SSH:         s.User + "@" + s.Host + ":" + strconv.Itoa(s.Port),
		}
		if idx < len(vm.NetworkInterfaces) {
			e.InternalIP = vm.NetworkInterfaces[idx].PrivateIP
		}
		out = append(out, e)
	}
	return out
}
