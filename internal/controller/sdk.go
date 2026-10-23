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

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
)

// What the operator needs from the Hyperbolic API beyond the generated SDK:
// its policies for listing, adopting and terminating rentals, and the
// shapes its status reports.

// rentalPageSize and maxRentalPages bound how the active rentals are read: a
// page of 100, at most 1000 pages.
const (
	rentalPageSize = 100
	maxRentalPages = 1000
)

// listRentals returns every active virtual machine rental on the account, reading
// every page. A rental that moves between pages while they are read is
// listed once.
func listRentals(ctx context.Context, hc *hyperbolic.Client) ([]hyperbolic.RentalWithStatus, error) {
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

// terminateRental ends a rental. One that is already gone counts as
// terminated, so terminating twice succeeds.
func terminateRental(ctx context.Context, hc *hyperbolic.Client, rentalID, reason string) error {
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

// vmOptions returns the virtual machine options among an account's rental
// options.
func vmOptions(options []hyperbolic.RentalOption) []hyperbolic.VirtualMachineOption {
	out := make([]hyperbolic.VirtualMachineOption, 0, len(options))
	for _, o := range options {
		vm, err := o.AsVirtualMachineOption()
		if err == nil && vm.MachineType == hyperbolic.VirtualMachineOptionMachineTypeVirtualMachine {
			out = append(out, vm)
		}
	}
	return out
}

// costPerHour returns what a rental's current term costs per hour, in cents,
// or 0 when the API did not say.
func costPerHour(t hyperbolic.RentalTerm) float64 {
	kind, err := t.Discriminator()
	if err != nil {
		return 0
	}
	switch kind {
	case string(hyperbolic.ReservedTermTermTypeReserved):
		if r, err := t.AsReservedTerm(); err == nil {
			return r.CostPerHour
		}
	case string(hyperbolic.PrivateCloudTermTermTypePrivateCloud):
		if p, err := t.AsPrivateCloudTerm(); err == nil {
			return p.CostPerHour
		}
	default:
		if o, err := t.AsOnDemandTerm(); err == nil {
			return o.CostPerHour
		}
	}
	return 0
}

// accountBalance is an account's balance, in whole cents.
type accountBalance struct {
	BalanceCents, MaxOverdraftCents int64
}

// getBalance reads the account's balance.
func getBalance(ctx context.Context, hc *hyperbolic.Client) (*accountBalance, error) {
	b, err := hc.Balances.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &accountBalance{
		BalanceCents:      int64(math.Round(b.BalanceCents)),
		MaxOverdraftCents: int64(math.Round(b.MaxOverdraftCents)),
	}, nil
}
