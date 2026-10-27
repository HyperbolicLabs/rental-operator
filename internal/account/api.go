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

package account

import (
	"context"
	"fmt"
	"math"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	"github.com/HyperbolicLabs/rental-operator/internal/billing"
)

// rentalPageSize and maxRentalPages bound how the active rentals are read: a
// page of 100, at most 1000 pages.
const (
	rentalPageSize = 100
	maxRentalPages = 1000
)

// ListRentals returns every active virtual machine rental on the account, reading
// every page. A rental that moves between pages while they are read is
// listed once.
func ListRentals(ctx context.Context, hc *hyperbolic.Client) ([]hyperbolic.RentalWithStatus, error) {
	params := hyperbolic.ListActiveRentalsParams{
		PageSize:   hyperbolic.Ptr(rentalPageSize),
		RentalType: hyperbolic.Ptr(hyperbolic.RentalTypeVirtualMachine),
	}
	var out []hyperbolic.RentalWithStatus
	seen := map[string]bool{}
	for page := 1; page <= maxRentalPages; page++ {
		params.Page = &page
		p, err := hc.Rentals.ListActive(ctx, params)
		if err != nil {
			return nil, err
		}
		for _, r := range p.Items {
			if !seen[r.RentalID] {
				seen[r.RentalID] = true
				out = append(out, r)
			}
		}
		if page >= p.TotalPages || len(p.Items) == 0 {
			return out, nil
		}
	}
	return nil, fmt.Errorf("listing rentals: more than %d pages", maxRentalPages)
}

// RentalsWithLabel returns the active rentals that carry label and are not
// ending: the candidates to adopt after a create whose outcome was unknown.
func RentalsWithLabel(ctx context.Context, hc *hyperbolic.Client, label string) ([]hyperbolic.RentalWithStatus, error) {
	rentals, err := ListRentals(ctx, hc)
	if err != nil {
		return nil, err
	}
	var out []hyperbolic.RentalWithStatus
	for _, r := range rentals {
		if l, err := r.Label.Get(); err == nil && l == label &&
			r.Status != hyperbolic.RentalStatusTerminated && r.Status != hyperbolic.RentalStatusTerminating {
			out = append(out, r)
		}
	}
	return out, nil
}

// TerminateRental ends a rental. One that is already gone counts as
// terminated, so terminating twice succeeds.
func TerminateRental(ctx context.Context, hc *hyperbolic.Client, rentalID, reason string) error {
	params := hyperbolic.TerminateRentalParams{RentalID: rentalID}
	if reason != "" {
		params.Reason = &reason
	}
	_, err := hc.Rentals.Terminate(ctx, params)
	if hyperbolic.IsNotFound(err) || hyperbolic.HasCode(err, hyperbolic.CodeAlreadyTerminated) {
		return nil
	}
	return err
}

// GetBalance reads the account's balance.
func GetBalance(ctx context.Context, hc *hyperbolic.Client) (*billing.Balance, error) {
	b, err := hc.Balances.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &billing.Balance{
		BalanceCents:      int64(math.Round(b.BalanceCents)),
		MaxOverdraftCents: int64(math.Round(b.MaxOverdraftCents)),
	}, nil
}
