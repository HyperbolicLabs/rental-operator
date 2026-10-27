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

// Creating a rental is not idempotent and the API has no idempotency key, so
// the operator makes creates safe itself:
//
//  1. Every rental carries a recognizable label (rentalLabel).
//  2. Before sending a create, the operator records create-pending on the
//     object. If that write fails, nothing is sent.
//  3. After a successful create, the rental id is recorded (and create-pending
//     cleared) before status is written.
//  4. Whenever create-pending is present without a rental id (the operator
//     crashed, a write failed, or the create's outcome was unknown), the
//     operator looks the rental up by label before doing anything else:
//     one match is adopted; none means wait until the request could no longer
//     land, then create again; several means stop and let a person decide.
//     Renting twice is worse than waiting.

import (
	"context"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/account"
)

const (
	// createRequestTimeout bounds how long a create request can be in flight:
	// the API client's HTTP timeout.
	createRequestTimeout = 30 * time.Second
	// createSettleGrace is how long the operator keeps looking for a rental
	// after a create with an unknown outcome could have finished, before it
	// concludes nothing was created and tries again.
	createSettleGrace = 30 * time.Second
)

// maxLabelLength is the longest rental label the API accepts. The spec does
// not document it, and 40 was observed against the live API.
const maxLabelLength = 40

// rentalLabel is the label the rental carries in Hyperbolic: spec.displayName,
// or "<metadata.name>-<first 6 characters of the UID>" cut to the API's
// 40-character limit. The UID part keeps it unique across objects that reuse
// a name.
func rentalLabel(gr *hypv1alpha1.GPURental) string {
	if gr.Spec.DisplayName != "" {
		return gr.Spec.DisplayName
	}
	uid := strings.ReplaceAll(string(gr.UID), "-", "")
	if len(uid) > 6 {
		uid = uid[:6]
	}
	name := gr.Name
	if limit := maxLabelLength - len(uid) - 1; len(name) > limit {
		name = strings.TrimRight(name[:limit], "-.")
	}
	return name + "-" + uid
}

// createPendingSince reports when the object's last create was sent, if its
// outcome has not been recorded yet.
func createPendingSince(obj metav1.Object) (time.Time, bool) {
	v, ok := obj.GetAnnotations()[hypv1alpha1.AnnotationCreatePending]
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		// Unreadable: treat it as just written, which only makes the operator
		// wait longer before creating again.
		return time.Now(), true
	}
	return t, true
}

// findByLabel returns the active rentals that carry the object's label and
// match its shape. The shape check keeps a stranger's rental with the same
// dashboard name from being adopted.
func (r *GPURentalReconciler) findByLabel(ctx context.Context, hc *hyperbolic.Client, gr *hypv1alpha1.GPURental) ([]hyperbolic.RentalWithStatus, error) {
	rentals, err := account.RentalsWithLabel(ctx, hc, rentalLabel(gr))
	if err != nil {
		return nil, err
	}
	matches := make([]hyperbolic.RentalWithStatus, 0, len(rentals))
	for _, vm := range rentals {
		if string(vm.GPUType) != gr.Spec.GPUType || vm.GPUCount != int(gr.Spec.GPUCount) {
			continue
		}
		if gr.Spec.Region != "" && vm.Region != gr.Spec.Region {
			continue
		}
		matches = append(matches, vm)
	}
	return matches, nil
}

// resolvePendingCreate settles a create whose outcome was not recorded.
func (r *GPURentalReconciler) resolvePendingCreate(ctx context.Context, gr *hypv1alpha1.GPURental, hc *hyperbolic.Client, creds account.Credentials, since time.Time) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	matches, err := r.findByLabel(ctx, hc, gr)
	if err != nil {
		r.setSynced(gr, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "list rentals: "+err.Error())
		return r.finish(ctx, gr, apiRetry)
	}
	r.setSynced(gr, metav1.ConditionTrue, hypv1alpha1.ReasonReconcileSuccess, "")

	switch len(matches) {
	case 1:
		log.Info("found the rental of a create whose outcome was unknown", "rentalId", matches[0].RentalID)
		return r.adopt(ctx, gr, &matches[0])
	case 0:
		if wait := since.Add(createRequestTimeout + createSettleGrace).Sub(r.Clock.Now()); wait > 0 {
			gr.Status.Phase = hypv1alpha1.PhaseProvisioning
			r.setReady(gr, metav1.ConditionFalse, hypv1alpha1.ReasonProvisioning,
				"Confirming whether the last create request took effect before trying again")
			return r.finish(ctx, gr, wait)
		}
		log.Info("the last create did not take effect; creating again")
		if err := patchAnnotations(ctx, r.Client, gr, nil, hypv1alpha1.AnnotationCreatePending); err != nil {
			return ctrl.Result{}, err
		}
		return r.preflightAndCreate(ctx, gr, hc, creds)
	default:
		ids := make([]string, 0, len(matches))
		for _, vm := range matches {
			ids = append(ids, vm.RentalID)
		}
		gr.Status.Phase = hypv1alpha1.PhaseUnknown
		r.setReady(gr, metav1.ConditionUnknown, hypv1alpha1.ReasonCreateOutcomeUnknown, fmt.Sprintf(
			"%d active rentals carry label %q (%s) and the operator cannot tell which one this object created. "+
				"Terminate the extra ones in the Hyperbolic dashboard; the remaining one is then adopted.",
			len(matches), rentalLabel(gr), strings.Join(ids, ", ")))
		return r.finish(ctx, gr, blockedRetry)
	}
}

// adopt records vm as this object's rental and shows it on status.
func (r *GPURentalReconciler) adopt(ctx context.Context, gr *hypv1alpha1.GPURental, vm *hyperbolic.RentalWithStatus) (ctrl.Result, error) {
	if err := r.recordRentalID(ctx, gr, vm.RentalID); err != nil {
		return ctrl.Result{}, err
	}
	r.applyRentalToStatus(gr, vm)
	return r.finish(ctx, gr, pollInterval(vm.Status))
}

// recordRentalID stores the rental's id on the object and clears
// create-pending in one write, before status is updated: an annotation
// survives a failed status write.
func (r *GPURentalReconciler) recordRentalID(ctx context.Context, gr *hypv1alpha1.GPURental, id string) error {
	err := patchAnnotations(ctx, r.Client, gr,
		map[string]string{hypv1alpha1.AnnotationRentalID: id},
		hypv1alpha1.AnnotationCreatePending)
	if err != nil {
		return fmt.Errorf("record rental id %s: %w", id, err)
	}
	return nil
}

// reconcileEnsureCreated adopts an orphaned rental or creates a new one.
func (r *GPURentalReconciler) reconcileEnsureCreated(ctx context.Context, gr *hypv1alpha1.GPURental, hc *hyperbolic.Client, creds account.Credentials) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Recovery path 1: an annotation id from a prior create whose status
	// write didn't land. The annotation is only written after a successful
	// create, so a rental exists — never fall through to create from here (that
	// would double-rent). On a transient list error, retry; if the rental is
	// genuinely gone it was terminated externally, so stop (we don't re-rent).
	if id := gr.Annotations[hypv1alpha1.AnnotationRentalID]; id != "" {
		vm, err := r.findRental(ctx, hc, creds.Key(), id)
		if err != nil {
			r.setSynced(gr, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "list rentals: "+err.Error())
			return r.finish(ctx, gr, apiRetry)
		}
		r.setSynced(gr, metav1.ConditionTrue, hypv1alpha1.ReasonReconcileSuccess, "")
		if vm != nil {
			r.applyRentalToStatus(gr, vm)
			return r.finish(ctx, gr, pollInterval(vm.Status))
		}
		r.markTerminatedExternally(gr)
		return r.finish(ctx, gr, 0)
	}

	// Recovery path 2: a create was sent but its outcome was never recorded.
	// Find the rental by label before anything else. Never create blindly.
	if since, pending := createPendingSince(gr); pending {
		return r.resolvePendingCreate(ctx, gr, hc, creds, since)
	}

	// Recovery path 3: a user-chosen display name may already name a rental
	// (for example one left running by a GPURental deleted with
	// deletionPolicy Orphan). Adopt it if exactly one matches. A generated
	// label contains this object's UID, so no other rental can carry it.
	if gr.Spec.DisplayName != "" {
		matches, err := r.findByLabel(ctx, hc, gr)
		if err != nil {
			r.setSynced(gr, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "list rentals: "+err.Error())
			return r.finish(ctx, gr, apiRetry)
		}
		if len(matches) == 1 {
			log.Info("adopted existing rental by label", "rentalId", matches[0].RentalID)
			r.setSynced(gr, metav1.ConditionTrue, hypv1alpha1.ReasonReconcileSuccess, "")
			return r.adopt(ctx, gr, &matches[0])
		}
	}

	// Preflight availability + balance, then create.
	return r.preflightAndCreate(ctx, gr, hc, creds)
}

// preflightAndCreate selects an option and creates the rental.
func (r *GPURentalReconciler) preflightAndCreate(ctx context.Context, gr *hypv1alpha1.GPURental, hc *hyperbolic.Client, creds account.Credentials) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// The keys to authorize must be saved first. SSHKey changes requeue the
	// rental, so the interval is a backstop.
	keyIDs, notReady, err := r.sshKeyIDs(ctx, gr)
	if err != nil {
		return ctrl.Result{}, err
	}
	if notReady != "" {
		r.setBlocked(gr, hypv1alpha1.ReasonSSHKeyNotReady, notReady)
		return r.finish(ctx, gr, blockedRetry)
	}

	// Read the account's rental options through the shared cache: one API
	// call per account per interval, whatever the number of objects.
	options, _, err := r.RentalOptions.Get(ctx, hc, creds.Key())
	if err != nil && len(options) == 0 {
		r.setBlocked(gr, hypv1alpha1.ReasonAPIError, "rental-options: "+err.Error())
		r.setSynced(gr, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "rental-options: "+err.Error())
		return r.finish(ctx, gr, apiRetry)
	}
	r.setSynced(gr, metav1.ConditionTrue, hypv1alpha1.ReasonReconcileSuccess, "")

	chosen, reason, msg := selectOption(options, gr.Spec)
	if chosen == nil {
		r.setBlocked(gr, reason, msg)
		return r.finish(ctx, gr, blockedRetry)
	}

	// Balance preflight (~1h of runway including overdraft).
	if bal, err := account.GetBalance(ctx, hc); err == nil {
		if float64(bal.BalanceCents+bal.MaxOverdraftCents) < chosen.CostPerHourCents {
			r.setBlocked(gr, hypv1alpha1.ReasonInsufficientBalance,
				fmt.Sprintf("balance %d + overdraft %d cents < ~1h cost %.0f cents",
					bal.BalanceCents, bal.MaxOverdraftCents, chosen.CostPerHourCents))
			return r.finish(ctx, gr, blockedRetry)
		}
	} // a balance read error is non-fatal for create; the create call will reject if truly broke.

	// Resolve the fabric explicitly (pin the customer's choice, else the option's).
	networkType := gr.Spec.NetworkType
	if networkType == "" {
		networkType = string(chosen.ConnectionType)
	}
	req := hyperbolic.CreateRentalParams{
		RentalType:  hyperbolic.RentalTypeVirtualMachine,
		GPUCount:    int(gr.Spec.GPUCount),
		GPUType:     hyperbolic.Ptr(hyperbolic.GPUType(gr.Spec.GPUType)),
		Region:      hyperbolic.Ptr(chosen.Region),
		TermType:    hyperbolic.Ptr(hyperbolic.TermTypeOnDemand),
		NetworkType: hyperbolic.Ptr(hyperbolic.CreateRentalParamsNetworkType(networkType)),
	}
	label := rentalLabel(gr)
	req.Label = &label
	if len(gr.Spec.Ports) > 0 {
		ports := int32sToInts(gr.Spec.Ports)
		req.CustomPorts = &ports
	}
	if len(keyIDs) > 0 {
		ids := make([]int, len(keyIDs))
		for i, id := range keyIDs {
			ids[i] = int(id)
		}
		req.SSHPublicKeyIds = &ids
	}

	// Write ahead: if this annotation cannot be recorded, send nothing.
	pending := map[string]string{hypv1alpha1.AnnotationCreatePending: r.Clock.Now().UTC().Format(time.RFC3339)}
	if err := patchAnnotations(ctx, r.Client, gr, pending); err != nil {
		return ctrl.Result{}, fmt.Errorf("record create-pending: %w", err)
	}

	row, err := hc.Rentals.Create(ctx, req)
	r.Rentals.Invalidate(creds.Key()) // whatever happened, the account's list may have changed
	r.Metrics.RentalCreated(err)
	if hyperbolic.IsOutcomeUnknown(err) {
		// The rental may exist. Keep create-pending and look it up by label on
		// the next reconcile instead of retrying the create.
		log.Info("create outcome unknown; will look the rental up by label", "label", label, "error", err.Error())
		gr.Status.Phase = hypv1alpha1.PhaseProvisioning
		r.setReady(gr, metav1.ConditionFalse, hypv1alpha1.ReasonProvisioning,
			"Confirming whether the create request took effect before trying again")
		r.setSynced(gr, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "create: "+err.Error())
		return r.finish(ctx, gr, pendingPoll)
	}
	if err != nil {
		// A definite failure: nothing was created.
		if cerr := patchAnnotations(ctx, r.Client, gr, nil, hypv1alpha1.AnnotationCreatePending); cerr != nil {
			return ctrl.Result{}, fmt.Errorf("clear create-pending: %w", cerr)
		}
		reason, msg := classifyCreateError(err)
		log.Info("create failed", "reason", reason, "error", err.Error())
		r.setBlocked(gr, reason, msg)
		if reason == hypv1alpha1.ReasonAPIError {
			r.setSynced(gr, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "create: "+err.Error())
		}
		return r.finish(ctx, gr, createErrorRetry(reason))
	}

	// Persist the id before anything else: annotation first (survives a
	// status write failure), then status. If this write fails, the next
	// reconcile finds the rental by label.
	if err := r.recordRentalID(ctx, gr, row.RentalID); err != nil {
		return ctrl.Result{}, err
	}
	gr.Status.RentalID = row.RentalID
	if name, err := row.Name.Get(); err == nil {
		gr.Status.Name = name
	}
	if row.Region != "" {
		gr.Status.Region = row.Region
	}
	if label, err := row.Label.Get(); err == nil {
		gr.Status.Label = label
	}
	gr.Status.NetworkType = networkType
	gr.Status.Phase = hypv1alpha1.PhaseProvisioning
	if len(gr.Spec.Ports) > 0 {
		gr.Status.OpenPorts = gr.Spec.Ports
	}
	gr.Status.SSHKeyIDs = keyIDs
	r.setReady(gr, metav1.ConditionFalse, hypv1alpha1.ReasonProvisioning, "Rental created; provisioning")
	log.Info("created rental", "rentalId", row.RentalID, "region", chosen.Region)
	return r.finish(ctx, gr, pendingPoll)
}
