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

// Package billing reads Hyperbolic billing amounts, all in cents: an
// account's balance and what a rental's term costs per hour.
package billing

import (
	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
)

// Balance is an account's balance, in whole cents.
type Balance struct {
	BalanceCents, MaxOverdraftCents int64
}

// CostPerHour returns what a rental's current term costs per hour, in cents,
// or 0 when the API did not say.
func CostPerHour(t hyperbolic.RentalTerm) float64 {
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
