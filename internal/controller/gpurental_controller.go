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
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
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

// GPURentalReconciler reconciles a GPURental object.
type GPURentalReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Catalog is the shared catalogue cache; preflight reads it instead of
	// calling rental-options directly. Optional — nil falls back to a live call.
	Catalog *CatalogCache
	// NewClient builds Hyperbolic API clients. Optional, and nil uses
	// NewClientFactory with the operator's version.
	NewClient ClientFactory
	// Clock tells the time. Optional, and nil uses the real clock.
	Clock clock.PassiveClock
	// Credentials remembers the last API key per ProviderConfig so rentals can
	// still be terminated after their Secret is deleted. Optional.
	Credentials *CredentialMemory
	// Recorder emits events on GPURentals. Optional.
	Recorder record.EventRecorder
	// APIReader reads around the cache, to explain a Secret the cache does
	// not hold (it is not labeled). Optional.
	APIReader client.Reader
}

func (r *GPURentalReconciler) now() time.Time {
	if r.Clock != nil {
		return r.Clock.Now()
	}
	return time.Now()
}

// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=gpurentals,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=gpurentals/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=gpurentals/finalizers,verbs=update
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=providerconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile drives one GPURental toward its desired state.
func (r *GPURentalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var gr hypv1alpha1.GPURental
	if err := r.Get(ctx, req.NamespacedName, &gr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	creds, credErr := resolveRentalCreds(ctx, r.Client, r.APIReader, &gr)
	var hc *hyperbolic.Client
	if credErr == nil {
		hc, credErr = creds.newClient(r.NewClient)
	}
	if credErr == nil {
		r.Credentials.remember(configKey(&gr), creds)
	}

	// Deletion.
	if !gr.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &gr, hc, credErr)
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
		r.setSynced(&gr, metav1.ConditionFalse, hypv1alpha1.ReasonCredentialError, credErr.Error())
		if gr.Status.RentalID == "" {
			r.setBlocked(&gr, hypv1alpha1.ReasonCredentialError, credErr.Error())
		}
		return r.finish(ctx, &gr, apiRetry)
	}

	// Explicit stop: terminate but keep the object (resume by setting it false).
	if gr.Spec.Terminated {
		return r.reconcileStop(ctx, &gr, hc)
	}

	// Ensure the rental exists (create or adopt).
	if gr.Status.RentalID == "" {
		return r.reconcileEnsureCreated(ctx, &gr, hc, creds)
	}

	// Poll and reconcile live state.
	return r.reconcilePoll(ctx, &gr, hc)
}

// reconcileEnsureCreated adopts an orphaned rental or creates a new one.
func (r *GPURentalReconciler) reconcileEnsureCreated(ctx context.Context, gr *hypv1alpha1.GPURental, hc *hyperbolic.Client, creds resolvedCreds) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Recovery path 1: an annotation id from a prior create whose status
	// write didn't land. The annotation is only written after a successful
	// create, so a rental exists — never fall through to create from here (that
	// would double-rent). On a transient list error, retry; if the rental is
	// genuinely gone it was terminated externally, so stop (we don't re-rent).
	if id := gr.Annotations[hypv1alpha1.AnnotationRentalID]; id != "" {
		vm, err := r.findByRentalID(ctx, hc, id)
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
func (r *GPURentalReconciler) preflightAndCreate(ctx context.Context, gr *hypv1alpha1.GPURental, hc *hyperbolic.Client, creds resolvedCreds) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Read the account catalogue through the shared per-credential cache (one
	// API call per account per interval); fall back to a live call if no cache.
	var options []hyperbolic.RentalOption
	if r.Catalog != nil {
		opts, _, err := r.Catalog.Get(ctx, hc, creds.key())
		if err != nil && len(opts) == 0 {
			r.setBlocked(gr, hypv1alpha1.ReasonAPIError, "rental-options: "+err.Error())
			r.setSynced(gr, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "rental-options: "+err.Error())
			return r.finish(ctx, gr, apiRetry)
		}
		options = opts
	} else {
		var err error
		options, err = hc.Rentals.Options(ctx)
		if err != nil {
			r.setBlocked(gr, hypv1alpha1.ReasonAPIError, "rental-options: "+err.Error())
			r.setSynced(gr, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "rental-options: "+err.Error())
			return r.finish(ctx, gr, apiRetry)
		}
	}
	r.setSynced(gr, metav1.ConditionTrue, hypv1alpha1.ReasonReconcileSuccess, "")

	chosen, reason, msg := selectOption(options, gr.Spec)
	if chosen == nil {
		r.setBlocked(gr, reason, msg)
		return r.finish(ctx, gr, blockedRetry)
	}

	// Balance preflight (~1h of runway including overdraft).
	if bal, err := getBalance(ctx, hc); err == nil {
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

	// Write ahead: if this annotation cannot be recorded, send nothing.
	pending := map[string]string{hypv1alpha1.AnnotationCreatePending: r.now().UTC().Format(time.RFC3339)}
	if err := r.patchAnnotations(ctx, gr, pending); err != nil {
		return ctrl.Result{}, fmt.Errorf("record create-pending: %w", err)
	}

	row, err := hc.Rentals.Create(ctx, req)
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
		if cerr := r.patchAnnotations(ctx, gr, nil, hypv1alpha1.AnnotationCreatePending); cerr != nil {
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
	r.setReady(gr, metav1.ConditionFalse, hypv1alpha1.ReasonProvisioning, "Rental created; provisioning")
	log.Info("created rental", "rentalId", row.RentalID, "region", chosen.Region)
	return r.finish(ctx, gr, pendingPoll)
}

// reconcilePoll refreshes live status and applies requested reboots.
func (r *GPURentalReconciler) reconcilePoll(ctx context.Context, gr *hypv1alpha1.GPURental, hc *hyperbolic.Client) (ctrl.Result, error) {
	vm, err := r.findByRentalID(ctx, hc, gr.Status.RentalID)
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
		if err := r.patchAnnotations(ctx, gr, gen); err != nil {
			log.Info("failed to record the applied reboot generation", "error", err.Error())
		}
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
	if cost := costPerHour(vm.CurrentTerm); cost > 0 {
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

// ---- lookups -------------------------------------------------------------

// findByRentalID returns (rental, nil) if found, (nil, nil) if the list
// succeeded but the id isn't present, or (nil, err) on a list failure.
func (r *GPURentalReconciler) findByRentalID(ctx context.Context, hc *hyperbolic.Client, id string) (*hyperbolic.RentalWithStatus, error) {
	rentals, err := listRentals(ctx, hc)
	if err != nil {
		return nil, err
	}
	for i := range rentals {
		if rentals[i].RentalID == id {
			return &rentals[i], nil
		}
	}
	return nil, nil
}

// ---- small helpers -------------------------------------------------------

// rentalID is the id of the object's rental: from status, or from the
// annotation written before status when a status write was lost. Empty means
// no rental was recorded.
func (r *GPURentalReconciler) rentalID(gr *hypv1alpha1.GPURental) string {
	if gr.Status.RentalID != "" {
		return gr.Status.RentalID
	}
	return gr.Annotations[hypv1alpha1.AnnotationRentalID]
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

// SetupWithManager sets up the controller with the Manager.
func (r *GPURentalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hypv1alpha1.GPURental{}).
		Named("gpurental").
		Complete(r)
}

// ---- pure functions (unit-testable) -------------------------------------

// selectOption picks the cheapest enabled, available VM option matching the spec.
func selectOption(options []hyperbolic.RentalOption, spec hypv1alpha1.GPURentalSpec) (chosen *hyperbolic.VirtualMachineOption, reason, msg string) {
	candidates := make([]hyperbolic.VirtualMachineOption, 0, len(options))
	for _, o := range vmOptions(options) {
		if !o.Enabled {
			continue
		}
		if string(o.GPUType) != spec.GPUType || o.GPUCount != int(spec.GPUCount) {
			continue
		}
		if spec.Region != "" && o.Region != spec.Region {
			continue
		}
		if spec.NetworkType != "" && string(o.ConnectionType) != spec.NetworkType {
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
		return nil, hypv1alpha1.ReasonAvailabilityUnavailable, fmt.Sprintf(
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
	case hyperbolic.HasCode(err, hyperbolic.CodeCustomPortsNotSupportedForRegion):
		return hypv1alpha1.ReasonPortsUnsupportedInRegion, "custom ports are not supported in the selected region; use a forge region or remove spec.ports"
	case hyperbolic.HasCode(err, hyperbolic.CodeNoPublicKey):
		return hypv1alpha1.ReasonNoSSHKey, "the account has no SSH public key registered; add one in the Hyperbolic dashboard before renting"
	case hyperbolic.HasCode(err, hyperbolic.CodeRegionDisabled):
		return hypv1alpha1.ReasonRegionDisabled, "the selected region is not enabled for this account: " + err.Error()
	case hyperbolic.HasCode(err, hyperbolic.CodeInsufficientBalance):
		return hypv1alpha1.ReasonInsufficientBalance, err.Error()
	case hyperbolic.HasCode(err, hyperbolic.CodeFailedToCalculateCostPerHour):
		return hypv1alpha1.ReasonAvailabilityUnavailable, "no matching offering (cost calculation failed) for the requested config/fabric/region"
	default:
		return hypv1alpha1.ReasonAPIError, err.Error()
	}
}

// createErrorRetry chooses a backoff based on whether the reason is
// terminal-until-spec-change (slow) or transient (faster).
func createErrorRetry(reason string) time.Duration {
	switch reason {
	case hypv1alpha1.ReasonAPIError:
		return apiRetry
	default:
		return blockedRetry
	}
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
