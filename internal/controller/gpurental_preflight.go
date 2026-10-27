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
	"fmt"
	"sort"
	"time"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/account"
)

// selectOption picks the cheapest enabled, available VM option matching the spec.
func selectOption(options []hyperbolic.RentalOption, spec hypv1alpha1.GPURentalSpec) (chosen *hyperbolic.VirtualMachineOption, reason, msg string) {
	candidates := make([]hyperbolic.VirtualMachineOption, 0, len(options))
	for _, o := range account.VirtualMachineOptions(options) {
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
