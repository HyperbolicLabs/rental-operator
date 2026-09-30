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
	"testing"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/hyperbolic"
)

func TestFilterOfferings(t *testing.T) {
	opts := []hyperbolic.RentalOption{
		{MachineType: "virtual-machine", Enabled: true, GPUType: "h100", GPUCount: 8, Region: "us-east-1", ConnectionType: "ethernet", CostPerHourCents: 2500, TotalAvailable: avail(4)},
		{MachineType: "virtual-machine", Enabled: true, GPUType: "h100", GPUCount: 8, Region: "us-central-1", ConnectionType: "ethernet", CostPerHourCents: 2390.4, TotalAvailable: avail(0)},
		{MachineType: "virtual-machine", Enabled: false, GPUType: "h100", GPUCount: 8, Region: "eu-north-2", ConnectionType: "infiniband", CostPerHourCents: 2100},
		{MachineType: "bare-metal", Enabled: true, GPUType: "h100", GPUCount: 8, Region: "us-central-1", ConnectionType: "ethernet", CostPerHourCents: 1},
		{MachineType: "virtual-machine", Enabled: true, GPUType: "h200", GPUCount: 8, Region: "us-central-1", ConnectionType: "ethernet", CostPerHourCents: 4000, TotalAvailable: avail(2)},
	}

	t.Run("gpuType filter excludes bare-metal and other models, cheapest first", func(t *testing.T) {
		got := filterOfferings(opts, hypv1alpha1.GPUAvailabilitySpec{GPUType: "h100"})
		if len(got) != 3 { // us-east-1, us-central-1, eu-north-2 (all VM h100)
			t.Fatalf("want 3, got %d: %+v", len(got), got)
		}
		if got[0].CostCentsPerHour != 2100 {
			t.Fatalf("expected cheapest first (2100), got %d", got[0].CostCentsPerHour)
		}
		for _, o := range got {
			if o.GPUType != "h100" {
				t.Fatalf("leaked non-h100: %+v", o)
			}
		}
	})

	t.Run("cost rounds to nearest cent", func(t *testing.T) {
		got := filterOfferings(opts, hypv1alpha1.GPUAvailabilitySpec{Region: "us-central-1", GPUType: "h100"})
		if len(got) != 1 || got[0].CostCentsPerHour != 2390 {
			t.Fatalf("want one h100 at 2390 cents, got %+v", got)
		}
	})

	t.Run("onlyAvailable drops disabled, zero, and unknown stock", func(t *testing.T) {
		got := filterOfferings(opts, hypv1alpha1.GPUAvailabilitySpec{GPUType: "h100", OnlyAvailable: true})
		// only us-east-1 (avail 4). us-central-1 is 0, eu-north-2 disabled+unknown.
		if len(got) != 1 || got[0].Region != "us-east-1" {
			t.Fatalf("want only us-east-1, got %+v", got)
		}
	})

	t.Run("price ceiling", func(t *testing.T) {
		got := filterOfferings(opts, hypv1alpha1.GPUAvailabilitySpec{GPUType: "h100", MaxPriceCentsPerHour: ptr(int64(2200))})
		if len(got) != 1 || got[0].Region != "eu-north-2" {
			t.Fatalf("want only eu-north-2 (2100), got %+v", got)
		}
	})

	t.Run("unknown stock stays nil (tri-state)", func(t *testing.T) {
		got := filterOfferings(opts, hypv1alpha1.GPUAvailabilitySpec{Region: "eu-north-2"})
		if len(got) != 1 || got[0].Available != nil {
			t.Fatalf("want one offering with nil Available, got %+v", got)
		}
	})
}
