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
	"sort"
	"strconv"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/hyperbolic"
)

const (
	machineTypeVM = "virtual-machine"

	pendingPoll = 10 * time.Second
	runningPoll = 60 * time.Second
	// blockedRetry is how often a Blocked rental (no availability, insufficient
	// balance, disabled region, unsupported ports, missing key) is retried.
	// These states clear on their own (stock returns, balance tops up) or on a
	// spec change, so a gentle 5-minute cadence avoids hammering the API.
	blockedRetry = 5 * time.Minute
	apiRetry     = 30 * time.Second

	// reasonAPIError is the condition reason for a transient API failure.
	reasonAPIError = "APIError"

	// phaseProvisioning is the CR phase shown while the API status is Pending
	// (clearer than surfacing the raw "Pending").
	phaseProvisioning = "Provisioning"

	// phaseBlocked is the CR phase when the rental can't be created yet — no
	// availability, insufficient balance, disabled region, unsupported ports,
	// missing SSH key, or a bad credential. The specific reason is on the
	// Ready condition, and the operator retries without hot-looping.
	phaseBlocked = "Blocked"
)

// rentalPhase maps the raw API status to the CR's phase, renaming the API's
// pre-Running "Pending" to "Provisioning".
func rentalPhase(apiStatus string) string {
	if apiStatus == hyperbolic.StatusPending {
		return phaseProvisioning
	}
	return apiStatus
}

// Known API error codes we translate into conditions.
const (
	codeCustomPortsUnsupported = "CUSTOM_PORTS_NOT_SUPPORTED_FOR_REGION"
	codeNoPublicKey            = "NO_PUBLIC_KEY"
	codeRegionDisabled         = "REGION_DISABLED"
	codeCostCalcFailed         = "FAILED_TO_CALCULATE_COST_PER_HOUR"
	codeInsufficientBalance    = "INSUFFICIENT_BALANCE"
)

// GPURentalReconciler reconciles a GPURental object.
type GPURentalReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Catalog is the shared catalogue cache; preflight reads it instead of
	// calling rental-options directly. Optional — nil falls back to a live call.
	Catalog *CatalogCache
}

// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=gpurentals,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=gpurentals/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=gpurentals/finalizers,verbs=update
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=providerconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile drives one GPURental toward its desired state.
func (r *GPURentalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var gr hypv1alpha1.GPURental
	if err := r.Get(ctx, req.NamespacedName, &gr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	creds, credErr := resolveRentalCreds(ctx, r.Client, &gr)

	// Deletion.
	if !gr.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &gr, creds, credErr)
	}

	// Ensure finalizer.
	if !controllerutil.ContainsFinalizer(&gr, hypv1alpha1.FinalizerTerminate) {
		controllerutil.AddFinalizer(&gr, hypv1alpha1.FinalizerTerminate)
		if err := r.Update(ctx, &gr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Tombstone: once explicitly terminated the CR is read-only and never
	// re-provisions, regardless of the spec (needs no credentials).
	if gr.Status.Terminated {
		return ctrl.Result{}, nil
	}

	if credErr != nil {
		log.Info("credentials not resolvable", "error", credErr.Error())
		r.setBlocked(&gr, "CredentialError", credErr.Error())
		return r.finish(ctx, &gr, apiRetry)
	}
	hc := creds.newClient()

	// Explicit stop: terminate but keep the object (resume by setting it false).
	if gr.Spec.Terminated {
		return r.reconcileStop(ctx, &gr, hc)
	}

	// Ensure the rental exists (create or adopt).
	if gr.Status.NumericID == nil {
		return r.reconcileEnsureCreated(ctx, &gr, hc, creds)
	}

	// Poll and reconcile live state.
	return r.reconcilePoll(ctx, &gr, hc)
}

// reconcileDelete terminates the rental (best-effort) and removes the finalizer.
func (r *GPURentalReconciler) reconcileDelete(ctx context.Context, gr *hypv1alpha1.GPURental, creds resolvedCreds, credErr error) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(gr, hypv1alpha1.FinalizerTerminate) {
		return ctrl.Result{}, nil
	}
	numericID := r.numericID(gr)
	if numericID != 0 && credErr == nil {
		if err := creds.newClient().TerminateVMRental(ctx, numericID, "GPURental deleted"); err != nil {
			// Already-terminated / not-found is success; otherwise retry.
			if !isGoneError(err) {
				log.Info("terminate on delete failed; will retry", "error", err.Error())
				return ctrl.Result{RequeueAfter: apiRetry}, nil
			}
		}
	} else if credErr != nil {
		// Can't reach the API to terminate; don't block deletion forever.
		log.Info("cannot terminate on delete (credentials unavailable); removing finalizer", "error", credErr.Error())
	}
	controllerutil.RemoveFinalizer(gr, hypv1alpha1.FinalizerTerminate)
	if err := r.Update(ctx, gr); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// reconcileStop terminates the rental for spec.terminated=true and marks the CR
// as a permanent read-only tombstone. One-way: the ids are kept for the record
// and the rental is never re-provisioned.
func (r *GPURentalReconciler) reconcileStop(ctx context.Context, gr *hypv1alpha1.GPURental, hc *hyperbolic.Client) (ctrl.Result, error) {
	numericID := r.numericID(gr)
	if numericID != 0 {
		if err := hc.TerminateVMRental(ctx, numericID, "spec.terminated=true"); err != nil && !isGoneError(err) {
			r.setReady(gr, metav1.ConditionFalse, reasonAPIError, err.Error())
			return r.finish(ctx, gr, apiRetry)
		}
	}
	gr.Status.Terminated = true
	gr.Status.Phase = hyperbolic.StatusTerminated
	r.setReady(gr, metav1.ConditionFalse, "Terminated", "Rental terminated (spec.terminated=true); this GPURental is now read-only")
	return r.finish(ctx, gr, 0)
}

// reconcileEnsureCreated adopts an orphaned rental or creates a new one.
func (r *GPURentalReconciler) reconcileEnsureCreated(ctx context.Context, gr *hypv1alpha1.GPURental, hc *hyperbolic.Client, creds resolvedCreds) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Recovery path 1: an annotation numeric id from a prior create whose status
	// write didn't land. The annotation is only written after a successful
	// create, so a rental exists — never fall through to create from here (that
	// would double-rent). On a transient list error, retry; if the rental is
	// genuinely gone it was terminated externally, so stop (we don't re-rent).
	if idStr := gr.Annotations[hypv1alpha1.AnnotationRentalID]; idStr != "" {
		if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
			vm, err := r.findByNumericID(ctx, hc, id)
			if err != nil {
				r.setReady(gr, metav1.ConditionUnknown, reasonAPIError, "list rentals: "+err.Error())
				return r.finish(ctx, gr, apiRetry)
			}
			if vm != nil {
				r.applyRentalToStatus(gr, vm)
				return r.finish(ctx, gr, pollInterval(vm.Status))
			}
			gr.Status.Phase = hyperbolic.StatusTerminated
			r.setReady(gr, metav1.ConditionFalse, "Terminated", "Rental no longer active (terminated externally)")
			return r.finish(ctx, gr, 0)
		}
	}

	// Recovery path 2: adopt by label, only when a displayName was set (unique-ish).
	if gr.Spec.DisplayName != "" {
		vm, err := r.adoptByLabel(ctx, hc, gr)
		if err != nil {
			r.setReady(gr, metav1.ConditionUnknown, reasonAPIError, "list rentals: "+err.Error())
			return r.finish(ctx, gr, apiRetry)
		}
		if vm != nil {
			log.Info("adopted existing rental by label", "rentalId", vm.RentalID)
			r.stampNumericID(ctx, gr, vm.ID)
			r.applyRentalToStatus(gr, vm)
			return r.finish(ctx, gr, pollInterval(vm.Status))
		}
	}

	// Preflight availability + balance, then create.
	return r.preflightAndCreate(ctx, gr, hc, creds)
}

// preflightAndCreate selects an option and creates the rental.
func (r *GPURentalReconciler) preflightAndCreate(ctx context.Context, gr *hypv1alpha1.GPURental, hc *hyperbolic.Client, creds resolvedCreds) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Read the account catalogue through the shared per-credential cache (one
	// API call per account per interval); fall back to a live call if no cache.
	var options []hyperbolic.RentalOption
	if r.Catalog != nil {
		opts, _, err := r.Catalog.Get(ctx, hc, creds.key())
		if err != nil && len(opts) == 0 {
			r.setBlocked(gr, reasonAPIError, "rental-options: "+err.Error())
			return r.finish(ctx, gr, apiRetry)
		}
		options = opts
	} else {
		var err error
		options, err = hc.GetRentalOptions(ctx)
		if err != nil {
			r.setBlocked(gr, reasonAPIError, "rental-options: "+err.Error())
			return r.finish(ctx, gr, apiRetry)
		}
	}

	chosen, reason, msg := selectOption(options, gr.Spec)
	if chosen == nil {
		r.setBlocked(gr, reason, msg)
		return r.finish(ctx, gr, blockedRetry)
	}

	// Balance preflight (~1h of runway including overdraft).
	if bal, err := hc.GetBalance(ctx); err == nil {
		if float64(bal.BalanceCents+bal.MaxOverdraftCents) < chosen.CostPerHourCents {
			r.setBlocked(gr, "InsufficientBalance",
				fmt.Sprintf("balance %d + overdraft %d cents < ~1h cost %.0f cents",
					bal.BalanceCents, bal.MaxOverdraftCents, chosen.CostPerHourCents))
			return r.finish(ctx, gr, blockedRetry)
		}
	} // a balance read error is non-fatal for create; the create call will reject if truly broke.

	req := hyperbolic.CreateVMRentalRequest{
		GPUCount: int(gr.Spec.GPUCount),
		GPUType:  gr.Spec.GPUType,
		Region:   chosen.Region,
		TermType: "on-demand",
	}
	// Resolve the fabric explicitly (pin the customer's choice, else the option's).
	if gr.Spec.NetworkType != "" {
		req.NetworkType = gr.Spec.NetworkType
	} else {
		req.NetworkType = chosen.ConnectionType
	}
	if gr.Spec.DisplayName != "" {
		req.Label = gr.Spec.DisplayName
	}
	if len(gr.Spec.Ports) > 0 {
		req.CustomPorts = int32sToInts(gr.Spec.Ports)
	}

	row, err := hc.CreateVMRental(ctx, req)
	if err != nil {
		reason, msg := classifyCreateError(err)
		log.Info("create failed", "reason", reason, "error", err.Error())
		r.setBlocked(gr, reason, msg)
		return r.finish(ctx, gr, createErrorRetry(reason))
	}

	// Persist ids before anything else: annotation first (survives a status
	// write failure), then status.
	r.stampNumericID(ctx, gr, row.ID)
	gr.Status.NumericID = ptr(row.ID)
	gr.Status.RentalID = row.RentalID
	if row.Name != nil {
		gr.Status.Name = *row.Name
	}
	if row.Region != nil {
		gr.Status.Region = *row.Region
	}
	gr.Status.Phase = phaseProvisioning
	if len(gr.Spec.Ports) > 0 {
		gr.Status.OpenPorts = gr.Spec.Ports
	}
	r.setReady(gr, metav1.ConditionFalse, "Provisioning", "Rental created; provisioning")
	log.Info("created rental", "rentalId", row.RentalID, "numericId", row.ID, "region", req.Region)
	return r.finish(ctx, gr, pendingPoll)
}

// reconcilePoll refreshes live status and handles reboot/ports drift.
func (r *GPURentalReconciler) reconcilePoll(ctx context.Context, gr *hypv1alpha1.GPURental, hc *hyperbolic.Client) (ctrl.Result, error) {
	vm, err := r.findByNumericID(ctx, hc, *gr.Status.NumericID)
	if err != nil {
		// Transient list error — keep the last-known phase and retry; never
		// flip a live rental to Terminated because a poll failed.
		return ctrl.Result{RequeueAfter: apiRetry}, nil
	}
	if vm == nil {
		// Genuinely no longer in the active list → it's gone.
		gr.Status.Phase = hyperbolic.StatusTerminated
		r.setReady(gr, metav1.ConditionFalse, "Terminated", "Rental no longer active")
		return r.finish(ctx, gr, 0)
	}

	// Ports are immutable on a live rental — surface drift, don't act on it.
	if portsDrifted(gr) {
		apimeta.SetStatusCondition(&gr.Status.Conditions, metav1.Condition{
			Type: "PortsImmutable", Status: metav1.ConditionTrue, Reason: "PortsImmutable",
			Message:            "spec.ports differs from the rental's ports; ports cannot be changed on a live rental — recreate to change them",
			ObservedGeneration: gr.Generation,
		})
	} else {
		apimeta.RemoveStatusCondition(&gr.Status.Conditions, "PortsImmutable")
	}

	// Reboot if requested and possible.
	if gr.Spec.RebootGeneration > appliedRebootGen(gr) && vm.Status == hyperbolic.StatusRunning {
		r.rebootInstances(ctx, hc, gr, vm)
	}

	r.applyRentalToStatus(gr, vm)
	return r.finish(ctx, gr, pollInterval(vm.Status))
}

// rebootInstances reboots every node that supports guest-reboot.
func (r *GPURentalReconciler) rebootInstances(ctx context.Context, hc *hyperbolic.Client, gr *hypv1alpha1.GPURental, vm *hyperbolic.VMRental) {
	log := logf.FromContext(ctx)
	rebooted := false
	for _, inst := range vm.Instances {
		if !inst.SupportsGuestReboot() {
			continue
		}
		if err := hc.RebootInstance(ctx, vm.ID, inst.InstanceRef); err != nil {
			log.Info("reboot instance failed", "instanceRef", inst.InstanceRef, "error", err.Error())
			continue
		}
		rebooted = true
	}
	if rebooted {
		r.setAnnotation(ctx, gr, hypv1alpha1.AnnotationRebootedGeneration, strconv.Itoa(int(gr.Spec.RebootGeneration)))
	}
}

// applyRentalToStatus maps a live rental onto CR status + conditions.
func (r *GPURentalReconciler) applyRentalToStatus(gr *hypv1alpha1.GPURental, vm *hyperbolic.VMRental) {
	gr.Status.Phase = rentalPhase(vm.Status)
	gr.Status.NumericID = ptr(vm.ID)
	gr.Status.RentalID = vm.RentalID
	if vm.Name != nil {
		gr.Status.Name = *vm.Name
	}
	if vm.Region != "" {
		gr.Status.Region = vm.Region
	}
	if vm.CurrentTerm.CostPerHour > 0 {
		// currentTerm.costPerHour is already in cents (verified live: 3192 == $31.92/hr).
		gr.Status.CostCentsPerHour = ptr(int64(math.Round(vm.CurrentTerm.CostPerHour)))
	}
	gr.Status.OpenPorts = intsToInt32s(vm.ProviderData.CustomPorts)
	gr.Status.Instances = instancesFromRental(vm)

	switch vm.Status {
	case hyperbolic.StatusRunning:
		r.setReady(gr, metav1.ConditionTrue, "Running",
			fmt.Sprintf("%d/%d instances running", runningCount(gr.Status.Instances), len(gr.Status.Instances)))
	case hyperbolic.StatusPending:
		r.setReady(gr, metav1.ConditionFalse, "Provisioning", "Rental is provisioning")
	case hyperbolic.StatusRebooting:
		r.setReady(gr, metav1.ConditionFalse, "Rebooting", "Rental is rebooting")
	case hyperbolic.StatusTerminating, hyperbolic.StatusTerminated:
		r.setReady(gr, metav1.ConditionFalse, "Terminated", "Rental is terminating/terminated")
	case hyperbolic.StatusFailed:
		r.setReady(gr, metav1.ConditionFalse, "Failed", "Rental failed")
	default:
		r.setReady(gr, metav1.ConditionUnknown, "Unknown", "Rental status unknown")
	}
}

// ---- lookups -------------------------------------------------------------

// findByNumericID returns (rental, nil) if found, (nil, nil) if the list
// succeeded but the id isn't present, or (nil, err) on a list failure.
func (r *GPURentalReconciler) findByNumericID(ctx context.Context, hc *hyperbolic.Client, id int64) (*hyperbolic.VMRental, error) {
	rentals, err := hc.ListVMRentals(ctx)
	if err != nil {
		return nil, err
	}
	for i := range rentals {
		if rentals[i].ID == id {
			return &rentals[i], nil
		}
	}
	return nil, nil
}

// adoptByLabel returns a single non-terminated rental whose label matches the
// display name, (nil, nil) if there are zero or multiple matches, or (nil, err)
// on a list failure.
func (r *GPURentalReconciler) adoptByLabel(ctx context.Context, hc *hyperbolic.Client, gr *hypv1alpha1.GPURental) (*hyperbolic.VMRental, error) {
	rentals, err := hc.ListVMRentals(ctx)
	if err != nil {
		return nil, err
	}
	matches := make([]*hyperbolic.VMRental, 0, len(rentals))
	for i := range rentals {
		vm := &rentals[i]
		if vm.Label == nil || *vm.Label != gr.Spec.DisplayName {
			continue
		}
		if vm.Status == hyperbolic.StatusTerminated || vm.Status == hyperbolic.StatusTerminating {
			continue
		}
		if vm.GPUType != gr.Spec.GPUType || vm.GPUCount != int(gr.Spec.GPUCount) {
			continue
		}
		matches = append(matches, vm)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return nil, nil // 0 or ambiguous → create a fresh one
}

// ---- small helpers -------------------------------------------------------

func (r *GPURentalReconciler) numericID(gr *hypv1alpha1.GPURental) int64 {
	if gr.Status.NumericID != nil {
		return *gr.Status.NumericID
	}
	if idStr := gr.Annotations[hypv1alpha1.AnnotationRentalID]; idStr != "" {
		if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
			return id
		}
	}
	return 0
}

func (r *GPURentalReconciler) stampNumericID(ctx context.Context, gr *hypv1alpha1.GPURental, id int64) {
	r.setAnnotation(ctx, gr, hypv1alpha1.AnnotationRentalID, strconv.FormatInt(id, 10))
}

func (r *GPURentalReconciler) setAnnotation(ctx context.Context, gr *hypv1alpha1.GPURental, k, v string) {
	if gr.Annotations != nil && gr.Annotations[k] == v {
		return
	}
	base := gr.DeepCopy()
	if gr.Annotations == nil {
		gr.Annotations = map[string]string{}
	}
	gr.Annotations[k] = v
	if err := r.Patch(ctx, gr, client.MergeFrom(base)); err != nil {
		logf.FromContext(ctx).Info("failed to stamp annotation", "key", k, "error", err.Error())
	}
}

func (r *GPURentalReconciler) setReady(gr *hypv1alpha1.GPURental, status metav1.ConditionStatus, reason, msg string) {
	apimeta.SetStatusCondition(&gr.Status.Conditions, metav1.Condition{
		Type: hypv1alpha1.ConditionReady, Status: status, Reason: reason, Message: msg,
		ObservedGeneration: gr.Generation,
	})
	gr.Status.ObservedGeneration = gr.Generation
}

// setBlocked marks the rental as unable to be created yet (phase Blocked) with
// the specific reason on the Ready condition.
func (r *GPURentalReconciler) setBlocked(gr *hypv1alpha1.GPURental, reason, msg string) {
	gr.Status.Phase = phaseBlocked
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

func portsDrifted(gr *hypv1alpha1.GPURental) bool {
	desired := int32sToInts(gr.Spec.Ports)
	actual := int32sToInts(gr.Status.OpenPorts)
	if len(desired) != len(actual) {
		return len(gr.Spec.Ports) > 0 && !sameIntSet(desired, actual)
	}
	return !sameIntSet(desired, actual)
}

// SetupWithManager sets up the controller with the Manager.
func (r *GPURentalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hypv1alpha1.GPURental{}).
		Named("gpurental").
		Complete(r)
}

// ---- pure functions (unit-testable) -------------------------------------

// selectOption picks the cheapest enabled, available VM option matching the spec.
func selectOption(options []hyperbolic.RentalOption, spec hypv1alpha1.GPURentalSpec) (chosen *hyperbolic.RentalOption, reason, msg string) {
	candidates := make([]hyperbolic.RentalOption, 0, len(options))
	for _, o := range options {
		if o.MachineType != machineTypeVM {
			continue
		}
		if !o.Enabled {
			continue
		}
		if o.GPUType != spec.GPUType || o.GPUCount != int(spec.GPUCount) {
			continue
		}
		if spec.Region != "" && o.Region != spec.Region {
			continue
		}
		if spec.NetworkType != "" && o.ConnectionType != spec.NetworkType {
			continue
		}
		if o.TotalAvailable != nil && *o.TotalAvailable <= 0 {
			continue
		}
		if spec.MaxPriceCentsPerHour != nil && o.CostPerHourCents > float64(*spec.MaxPriceCentsPerHour) {
			continue
		}
		candidates = append(candidates, o)
	}
	if len(candidates) == 0 {
		return nil, "AvailabilityUnavailable", fmt.Sprintf(
			"no available option for %s x%d%s%s", spec.GPUType, spec.GPUCount,
			regionSuffix(spec.Region), priceSuffix(spec.MaxPriceCentsPerHour))
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].CostPerHourCents < candidates[j].CostPerHourCents
	})
	return &candidates[0], "", ""
}

func classifyCreateError(err error) (reason, msg string) {
	switch {
	case hyperbolic.HasCode(err, codeCustomPortsUnsupported):
		return "PortsUnsupportedInRegion", "custom ports are not supported in the selected region; use a forge region or remove spec.ports"
	case hyperbolic.HasCode(err, codeNoPublicKey):
		return "NoSSHKey", "the account has no SSH public key registered; add one in the Hyperbolic dashboard before renting"
	case hyperbolic.HasCode(err, codeRegionDisabled):
		return "RegionDisabled", "the selected region is not enabled for this account: " + err.Error()
	case hyperbolic.HasCode(err, codeInsufficientBalance):
		return "InsufficientBalance", err.Error()
	case hyperbolic.HasCode(err, codeCostCalcFailed):
		return "AvailabilityUnavailable", "no matching offering (cost calculation failed) for the requested config/fabric/region"
	default:
		return reasonAPIError, err.Error()
	}
}

// createErrorRetry chooses a backoff based on whether the reason is
// terminal-until-spec-change (slow) or transient (faster).
func createErrorRetry(reason string) time.Duration {
	switch reason {
	case reasonAPIError:
		return apiRetry
	default:
		return blockedRetry
	}
}

func pollInterval(status string) time.Duration {
	switch status {
	case hyperbolic.StatusRunning:
		return runningPoll
	case hyperbolic.StatusTerminated, hyperbolic.StatusFailed:
		return 0
	default:
		return pendingPoll
	}
}

func instancesFromRental(vm *hyperbolic.VMRental) []hypv1alpha1.InstanceStatus {
	if len(vm.Instances) > 0 {
		out := make([]hypv1alpha1.InstanceStatus, 0, len(vm.Instances))
		for _, i := range vm.Instances {
			out = append(out, hypv1alpha1.InstanceStatus{
				InstanceRef: i.InstanceRef, Phase: i.Phase, SSH: i.SSH, InternalIP: i.InternalIP,
			})
		}
		return out
	}
	// Fall back to rental-level SSH networking (non-forge providers).
	if len(vm.ProviderData.SSHNetworking) > 0 {
		out := make([]hypv1alpha1.InstanceStatus, 0, len(vm.ProviderData.SSHNetworking))
		for idx, s := range vm.ProviderData.SSHNetworking {
			ip := ""
			if idx < len(vm.ProviderData.NodeNetworking) {
				ip = vm.ProviderData.NodeNetworking[idx].PrivateIP
			}
			out = append(out, hypv1alpha1.InstanceStatus{
				InstanceRef: fmt.Sprintf("%s-%d", vm.ExternalID, idx),
				Phase:       vm.Status,
				SSH:         fmt.Sprintf("%s@%s:%d", s.User, s.Host, s.Port),
				InternalIP:  ip,
			})
		}
		return out
	}
	return nil
}

func runningCount(insts []hypv1alpha1.InstanceStatus) int {
	n := 0
	for _, i := range insts {
		if i.Phase == hyperbolic.StatusRunning {
			n++
		}
	}
	return n
}

func isGoneError(err error) bool {
	// A terminate/reboot against an already-gone rental is success.
	if hyperbolic.StatusCodeOf(err) == 404 {
		return true
	}
	return hyperbolic.HasCode(err, "RENTAL_NOT_FOUND") || hyperbolic.HasCode(err, "ALREADY_TERMINATED")
}

func int32sToInts(in []int32) []int {
	out := make([]int, len(in))
	for i, v := range in {
		out[i] = int(v)
	}
	return out
}

func intsToInt32s(in []int) []int32 {
	if len(in) == 0 {
		return nil
	}
	out := make([]int32, len(in))
	for i, v := range in {
		out[i] = int32(v)
	}
	return out
}

func sameIntSet(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[int]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
	}
	for _, v := range seen {
		if v != 0 {
			return false
		}
	}
	return true
}

func regionSuffix(region string) string {
	if region == "" {
		return ""
	}
	return " in " + region
}

func priceSuffix(max *int64) string {
	if max == nil {
		return ""
	}
	return fmt.Sprintf(" under %d cents/hr", *max)
}
