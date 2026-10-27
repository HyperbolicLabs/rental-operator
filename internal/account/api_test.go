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
	"testing"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	"github.com/HyperbolicLabs/hyperbolic-go/hyperbolictest"
)

func rent(t *testing.T, c *hyperbolic.Client, label string) string {
	t.Helper()
	r, err := c.Rentals.Create(context.Background(), hyperbolic.CreateRentalParams{
		RentalType: hyperbolic.RentalTypeVirtualMachine,
		GPUType:    hyperbolic.Ptr(hyperbolic.GPUTypeH100),
		GPUCount:   1,
		Region:     hyperbolic.Ptr("us-central-1"),
		Label:      &label,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r.RentalID
}

func TestListRentalsReadsEveryPage(t *testing.T) {
	api := hyperbolictest.NewServer(t)
	api.SetMaxPageSize(2)
	c := api.Client(t)
	for range 5 {
		rent(t, c, "train")
	}
	rentals, err := ListRentals(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range rentals {
		seen[r.RentalID] = true
	}
	if len(rentals) != 5 || len(seen) != 5 {
		t.Fatalf("listed %d rentals (%d distinct), want each of the 5 once", len(rentals), len(seen))
	}
}

func TestRentalsWithLabelSkipsOthersAndEndingRentals(t *testing.T) {
	api := hyperbolictest.NewServer(t)
	c := api.Client(t)
	keep := rent(t, c, "train")
	ending := rent(t, c, "train")
	rent(t, c, "other")
	api.SetRentalStatus(ending, hyperbolic.RentalStatusTerminating)

	found, err := RentalsWithLabel(context.Background(), c, "train")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].RentalID != keep {
		t.Fatalf("found %+v, want only %s", found, keep)
	}
}

func TestTerminateRentalCountsAGoneRentalAsTerminated(t *testing.T) {
	api := hyperbolictest.NewServer(t)
	c := api.Client(t)
	id := rent(t, c, "train")
	for i := range 2 {
		if err := TerminateRental(context.Background(), c, id, "test"); err != nil {
			t.Fatalf("terminate #%d: %v", i+1, err)
		}
	}
	if err := TerminateRental(context.Background(), c, "rnt_unknown", ""); err != nil {
		t.Fatalf("terminate an unknown rental: %v", err)
	}
}

func TestGetBalanceIsInWholeCents(t *testing.T) {
	api := hyperbolictest.NewServer(t)
	api.SetBalance(49992, 1000)
	b, err := GetBalance(context.Background(), api.Client(t))
	if err != nil {
		t.Fatal(err)
	}
	if b.BalanceCents != 49992 || b.MaxOverdraftCents != 1000 {
		t.Fatalf("balance = %+v", b)
	}
	wrongKey, err := hyperbolic.NewClient("sk_wrong", hyperbolic.WithBaseURL(api.URL), hyperbolic.WithRetryPolicy(hyperbolic.NoRetries()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetBalance(context.Background(), wrongKey); !hyperbolic.IsUnauthorized(err) {
		t.Fatalf("err = %v, want unauthorized", err)
	}
}
