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

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/account"
)

// findRental looks the rental up in the account's shared list. A rental
// missing from that list is looked up once more in a fresh one before it is
// reported absent, so a cached list can never make a live rental look gone.
func (r *GPURentalReconciler) findRental(ctx context.Context, hc *hyperbolic.Client, key account.Key, id string) (*hyperbolic.RentalWithStatus, error) {
	rentals, err := r.Rentals.List(ctx, hc, key)
	if err != nil {
		return nil, err
	}
	if vm := rentalByID(rentals, id); vm != nil {
		return vm, nil
	}
	r.Rentals.Invalidate(key)
	return r.findByRentalID(ctx, hc, id)
}

// findByRentalID lists the account's rentals without the cache. It returns
// (rental, nil) if found, (nil, nil) if the list succeeded but the id isn't
// present, or (nil, err) on a list failure.
func (r *GPURentalReconciler) findByRentalID(ctx context.Context, hc *hyperbolic.Client, id string) (*hyperbolic.RentalWithStatus, error) {
	rentals, err := account.ListRentals(ctx, hc)
	if err != nil {
		return nil, err
	}
	return rentalByID(rentals, id), nil
}

func rentalByID(rentals []hyperbolic.RentalWithStatus, id string) *hyperbolic.RentalWithStatus {
	for i := range rentals {
		if rentals[i].RentalID == id {
			return &rentals[i]
		}
	}
	return nil
}

// rentalID is the id of the object's rental: from status, or from the
// annotation written before status when a status write was lost. Empty means
// no rental was recorded.
func (r *GPURentalReconciler) rentalID(gr *hypv1alpha1.GPURental) string {
	if gr.Status.RentalID != "" {
		return gr.Status.RentalID
	}
	return gr.Annotations[hypv1alpha1.AnnotationRentalID]
}
