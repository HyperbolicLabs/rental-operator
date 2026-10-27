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
	"testing"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/hyperbolic"
)

func avail(n int) *int { return &n }

func TestSelectOption(t *testing.T) {
	opts := []hyperbolic.RentalOption{
		{MachineType: "virtual-machine", Enabled: true, GPUType: "h100", GPUCount: 8, Region: "us-east-1", ConnectionType: "ethernet", CostPerHourCents: 2500, TotalAvailable: avail(4)},
		{MachineType: "virtual-machine", Enabled: true, GPUType: "h100", GPUCount: 8, Region: "us-central-1", ConnectionType: "ethernet", CostPerHourCents: 2390, TotalAvailable: avail(2)},
		{MachineType: "virtual-machine", Enabled: true, GPUType: "h100", GPUCount: 8, Region: "eu-north-2", ConnectionType: "infiniband", CostPerHourCents: 2100, TotalAvailable: avail(1)},
		{MachineType: "virtual-machine", Enabled: false, GPUType: "h100", GPUCount: 8, Region: "us-west-1", ConnectionType: "ethernet", CostPerHourCents: 1000, TotalAvailable: avail(9)},
		{MachineType: "bare-metal", Enabled: true, GPUType: "h100", GPUCount: 8, Region: "us-central-1", ConnectionType: "ethernet", CostPerHourCents: 1, TotalAvailable: avail(9)},
		{MachineType: "virtual-machine", Enabled: true, GPUType: "h200", GPUCount: 8, Region: "us-central-1", ConnectionType: "ethernet", CostPerHourCents: 1, TotalAvailable: avail(9)},
	}

	tests := []struct {
		name       string
		spec       hypv1alpha1.GPURentalSpec
		wantRegion string
		wantNil    bool
		wantReason string
	}{
		{
			name:       "cheapest available VM wins across regions",
			spec:       hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8},
			wantRegion: "eu-north-2", // 2100, cheapest enabled VM h100x8
		},
		{
			name:       "pinned region",
			spec:       hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8, Region: "us-central-1"},
			wantRegion: "us-central-1",
		},
		{
			name:       "pinned fabric filters to infiniband",
			spec:       hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8, NetworkType: "infiniband"},
			wantRegion: "eu-north-2",
		},
		{
			name:       "price ceiling excludes all",
			spec:       hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8, MaxPriceCentsPerHour: ptr(int64(1500))},
			wantNil:    true,
			wantReason: "AvailabilityUnavailable",
		},
		{
			name:       "no such config",
			spec:       hypv1alpha1.GPURentalSpec{GPUType: "b200", GPUCount: 8},
			wantNil:    true,
			wantReason: "AvailabilityUnavailable",
		},
		{
			name:       "does not pick bare-metal even if cheaper",
			spec:       hypv1alpha1.GPURentalSpec{GPUType: "h100", GPUCount: 8, Region: "us-central-1"},
			wantRegion: "us-central-1", // the VM at 2390, not the $0.01 bare-metal
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason, _ := selectOption(opts, tc.spec)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil option, got region %s", got.Region)
				}
				if reason != tc.wantReason {
					t.Fatalf("reason = %q, want %q", reason, tc.wantReason)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected an option, got nil (reason %q)", reason)
			}
			if got.Region != tc.wantRegion {
				t.Fatalf("region = %s, want %s", got.Region, tc.wantRegion)
			}
			if got.MachineType != machineTypeVM {
				t.Fatalf("selected non-VM machineType %q", got.MachineType)
			}
		})
	}
}

func TestSameIntSet(t *testing.T) {
	cases := []struct {
		a, b []int
		want bool
	}{
		{[]int{8080, 8443}, []int{8443, 8080}, true},
		{[]int{8080}, []int{8080, 8443}, false},
		{nil, nil, true},
		{[]int{1, 1, 2}, []int{1, 2, 2}, false},
	}
	for _, c := range cases {
		if got := sameIntSet(c.a, c.b); got != c.want {
			t.Errorf("sameIntSet(%v,%v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
