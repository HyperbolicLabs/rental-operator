/*
Copyright 2026.

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
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypv1alpha1 "github.com/HyperbolicLabs/hyperbolic-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/hyperbolic-operator/internal/hyperbolic"
)

// catalogRetry is the requeue delay while the catalogue can't be fetched.
const catalogRetry = 15 * time.Second

// GPUAvailabilityReconciler projects the account's catalogue onto a
// GPUAvailability's status, filtered by its spec. It reads through the shared
// per-credential CatalogCache, so N objects on one account cost one API call
// per refresh interval.
type GPUAvailabilityReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	Catalog         *CatalogCache
	RefreshInterval time.Duration
}

// +kubebuilder:rbac:groups=hyperbolic.ai,resources=gpuavailabilities,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hyperbolic.ai,resources=gpuavailabilities/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hyperbolic.ai,resources=providerconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile filters the account catalogue into the object's status.
func (r *GPUAvailabilityReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var ga hypv1alpha1.GPUAvailability
	if err := r.Get(ctx, req.NamespacedName, &ga); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	creds, err := resolveCreds(ctx, r.Client, ga.Namespace, ga.Spec.ProviderConfigRef)
	if err != nil {
		r.setReady(&ga, metav1.ConditionFalse, "CredentialError", err.Error())
		return r.finish(ctx, &ga, apiRetry)
	}

	options, fetchedAt, fetchErr := r.Catalog.Get(ctx, creds.newClient(), creds.key())
	if fetchErr != nil && len(options) == 0 {
		r.setReady(&ga, metav1.ConditionFalse, "CatalogUnavailable", "rental-options: "+fetchErr.Error())
		return r.finish(ctx, &ga, catalogRetry)
	}

	offerings := filterOfferings(options, ga.Spec)
	ga.Status.Offerings = offerings
	ga.Status.OfferingCount = int32(len(offerings))
	if !fetchedAt.IsZero() {
		t := metav1.NewTime(fetchedAt)
		ga.Status.LastRefreshTime = &t
	}
	r.setReady(&ga, metav1.ConditionTrue, "Refreshed", fmt.Sprintf("%d offering(s) match", len(offerings)))
	return r.finish(ctx, &ga, r.refresh())
}

func (r *GPUAvailabilityReconciler) refresh() time.Duration {
	if r.RefreshInterval > 0 {
		return r.RefreshInterval
	}
	return 5 * time.Minute
}

func (r *GPUAvailabilityReconciler) setReady(ga *hypv1alpha1.GPUAvailability, status metav1.ConditionStatus, reason, msg string) {
	apimeta.SetStatusCondition(&ga.Status.Conditions, metav1.Condition{
		Type: hypv1alpha1.ConditionReady, Status: status, Reason: reason, Message: msg,
		ObservedGeneration: ga.Generation,
	})
	ga.Status.ObservedGeneration = ga.Generation
}

func (r *GPUAvailabilityReconciler) finish(ctx context.Context, ga *hypv1alpha1.GPUAvailability, requeue time.Duration) (ctrl.Result, error) {
	if err := r.Status().Update(ctx, ga); err != nil {
		return ctrl.Result{}, err
	}
	if requeue > 0 {
		return ctrl.Result{RequeueAfter: requeue}, nil
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GPUAvailabilityReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hypv1alpha1.GPUAvailability{}).
		Named("gpuavailability").
		Complete(r)
}

// filterOfferings applies a GPUAvailability spec filter to the catalogue and
// returns matching VM offerings, cheapest first. Pure and unit-testable.
func filterOfferings(options []hyperbolic.RentalOption, spec hypv1alpha1.GPUAvailabilitySpec) []hypv1alpha1.Offering {
	out := make([]hypv1alpha1.Offering, 0, len(options))
	for _, o := range options {
		if o.MachineType != machineTypeVM {
			continue
		}
		if spec.GPUType != "" && o.GPUType != spec.GPUType {
			continue
		}
		if spec.GPUCount != nil && o.GPUCount != int(*spec.GPUCount) {
			continue
		}
		if spec.Region != "" && o.Region != spec.Region {
			continue
		}
		if spec.NetworkType != "" && o.ConnectionType != spec.NetworkType {
			continue
		}
		if spec.MaxPriceCentsPerHour != nil && o.CostPerHourCents > float64(*spec.MaxPriceCentsPerHour) {
			continue
		}
		if spec.OnlyAvailable {
			if !o.Enabled || o.TotalAvailable == nil || *o.TotalAvailable <= 0 {
				continue
			}
		}
		var avail *int32
		if o.TotalAvailable != nil {
			v := int32(*o.TotalAvailable)
			avail = &v
		}
		variant := ""
		if o.EthernetVariant != nil {
			variant = *o.EthernetVariant
		}
		out = append(out, hypv1alpha1.Offering{
			GPUType:          o.GPUType,
			GPUCount:         int32(o.GPUCount),
			Region:           o.Region,
			NetworkType:      o.ConnectionType,
			EthernetVariant:  variant,
			CostCentsPerHour: int64(math.Round(o.CostPerHourCents)),
			Available:        avail,
			Enabled:          o.Enabled,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CostCentsPerHour < out[j].CostCentsPerHour
	})
	return out
}
