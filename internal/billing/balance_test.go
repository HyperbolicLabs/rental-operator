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

package billing

import (
	"testing"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
)

func TestCostPerHourReadsEveryKindOfTerm(t *testing.T) {
	var onDemand, reserved, privateCloud, empty hyperbolic.RentalTerm
	if err := onDemand.FromOnDemandTerm(hyperbolic.OnDemandTerm{CostPerHour: 275}); err != nil {
		t.Fatal(err)
	}
	if err := reserved.FromReservedTerm(hyperbolic.ReservedTerm{CostPerHour: 2200}); err != nil {
		t.Fatal(err)
	}
	if err := privateCloud.FromPrivateCloudTerm(hyperbolic.PrivateCloudTerm{CostPerHour: 3192}); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		term hyperbolic.RentalTerm
		want float64
	}{
		"on-demand":     {onDemand, 275},
		"reserved":      {reserved, 2200},
		"private cloud": {privateCloud, 3192},
		"missing":       {empty, 0},
	} {
		if got := CostPerHour(tc.term); got != tc.want {
			t.Errorf("%s: CostPerHour = %v, want %v", name, got, tc.want)
		}
	}
}
