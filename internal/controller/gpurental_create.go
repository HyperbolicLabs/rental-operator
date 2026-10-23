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

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
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

// createPendingSince reports when the last create was sent, if its outcome
// has not been recorded yet.
func createPendingSince(gr *hypv1alpha1.GPURental) (time.Time, bool) {
	v, ok := gr.Annotations[hypv1alpha1.AnnotationCreatePending]
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
	rentals, err := rentalsWithLabel(ctx, hc, rentalLabel(gr))
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
func (r *GPURentalReconciler) resolvePendingCreate(ctx context.Context, gr *hypv1alpha1.GPURental, hc *hyperbolic.Client, creds resolvedCreds, since time.Time) (ctrl.Result, error) {
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
		if wait := since.Add(createRequestTimeout + createSettleGrace).Sub(r.now()); wait > 0 {
			gr.Status.Phase = hypv1alpha1.PhaseProvisioning
			r.setReady(gr, metav1.ConditionFalse, hypv1alpha1.ReasonProvisioning,
				"Confirming whether the last create request took effect before trying again")
			return r.finish(ctx, gr, wait)
		}
		log.Info("the last create did not take effect; creating again")
		if err := r.patchAnnotations(ctx, gr, nil, hypv1alpha1.AnnotationCreatePending); err != nil {
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
	err := r.patchAnnotations(ctx, gr,
		map[string]string{hypv1alpha1.AnnotationRentalID: id},
		hypv1alpha1.AnnotationCreatePending)
	if err != nil {
		return fmt.Errorf("record rental id %s: %w", id, err)
	}
	return nil
}

// patchAnnotations sets and removes annotations on the object with a merge
// patch.
func (r *GPURentalReconciler) patchAnnotations(ctx context.Context, gr *hypv1alpha1.GPURental, set map[string]string, remove ...string) error {
	changed := false
	for k, v := range set {
		if gr.Annotations[k] != v {
			changed = true
		}
	}
	for _, k := range remove {
		if _, ok := gr.Annotations[k]; ok {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	base := gr.DeepCopy()
	if gr.Annotations == nil {
		gr.Annotations = map[string]string{}
	}
	for k, v := range set {
		gr.Annotations[k] = v
	}
	for _, k := range remove {
		delete(gr.Annotations, k)
	}
	return r.Patch(ctx, gr, client.MergeFrom(base))
}
