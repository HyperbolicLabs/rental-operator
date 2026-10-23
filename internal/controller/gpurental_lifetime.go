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
	"time"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

// defaultProvisioningTimeout applies when spec.lifetime.provisioningTimeout is
// unset (the API server defaults it on new objects).
const defaultProvisioningTimeout = 30 * time.Minute

// checkLifetime records when the rental started running and enforces
// spec.lifetime. It returns the Ready reason and message when the rental must
// be terminated now. Otherwise until is the time left to the next deadline
// (zero when there is none).
func (r *GPURentalReconciler) checkLifetime(gr *hypv1alpha1.GPURental, vm *hyperbolic.RentalWithStatus) (reason, msg string, until time.Duration) {
	now := r.now()
	if vm.Status == hyperbolic.RentalStatusRunning && gr.Status.RunningSince == nil {
		t := metav1.NewTime(now)
		gr.Status.RunningSince = &t
	}
	lt := gr.Spec.Lifetime

	if gr.Status.RunningSince != nil {
		if lt == nil || lt.TTL == nil {
			gr.Status.ExpiresAt = nil
			return "", "", 0
		}
		expires := gr.Status.RunningSince.Add(lt.TTL.Duration)
		e := metav1.NewTime(expires)
		gr.Status.ExpiresAt = &e
		if !now.Before(expires) {
			return hypv1alpha1.ReasonTTLExpired, fmt.Sprintf(
				"spec.lifetime.ttl (%s) ran out, so the rental was terminated; this GPURental is kept as a record", lt.TTL.Duration), 0
		}
		return "", "", expires.Sub(now)
	}

	// Not running yet: the provisioning timeout applies.
	if gr.Status.RentedAt == nil {
		return "", "", 0
	}
	timeout := defaultProvisioningTimeout
	if lt != nil && lt.ProvisioningTimeout != nil {
		timeout = lt.ProvisioningTimeout.Duration
	}
	deadline := gr.Status.RentedAt.Add(timeout)
	if !now.Before(deadline) {
		return hypv1alpha1.ReasonProvisioningTimedOut, fmt.Sprintf(
			"The rental did not reach Running within spec.lifetime.provisioningTimeout (%s), so it was terminated; rentals that never run are not charged", timeout), 0
	}
	return "", "", deadline.Sub(now)
}
