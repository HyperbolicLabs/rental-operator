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

package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

func ptr[T any](v T) *T { return &v }

func TestStatusCollector(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := hypv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	now := metav1.Now()
	ready := func(status metav1.ConditionStatus, reason string) []metav1.Condition {
		return []metav1.Condition{{Type: hypv1alpha1.ConditionReady, Status: status, Reason: reason, LastTransitionTime: now}}
	}
	rental := func(name string, status hypv1alpha1.GPURentalStatus) *hypv1alpha1.GPURental {
		return &hypv1alpha1.GPURental{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ml"}, Status: status}
	}

	running := rental("running", hypv1alpha1.GPURentalStatus{
		Phase: hypv1alpha1.PhaseRunning, CostCentsPerHour: ptr(int64(2200)),
		Conditions: ready(metav1.ConditionTrue, hypv1alpha1.ReasonRunning),
	})
	deleting := rental("deleting", hypv1alpha1.GPURentalStatus{
		Phase: hypv1alpha1.PhaseTerminating, CostCentsPerHour: ptr(int64(300)),
		Conditions: ready(metav1.ConditionFalse, hypv1alpha1.ReasonTerminating),
	})
	deleting.DeletionTimestamp = &now
	deleting.Finalizers = []string{hypv1alpha1.FinalizerTerminate}
	stopping := rental("stopping", hypv1alpha1.GPURentalStatus{
		Phase: hypv1alpha1.PhaseRunning, CostCentsPerHour: ptr(int64(100)),
		Conditions: ready(metav1.ConditionTrue, hypv1alpha1.ReasonRunning),
	})
	stopping.Spec.Terminated = true
	record := rental("record", hypv1alpha1.GPURentalStatus{
		Phase: hypv1alpha1.PhaseTerminated, CostCentsPerHour: ptr(int64(100)), TerminatedAt: &now,
		Conditions: ready(metav1.ConditionFalse, hypv1alpha1.ReasonTerminated),
	})
	record.Spec.Terminated = true
	blocked := rental("blocked", hypv1alpha1.GPURentalStatus{
		Phase:      hypv1alpha1.PhaseBlocked,
		Conditions: ready(metav1.ConditionFalse, hypv1alpha1.ReasonAvailabilityUnavailable),
	})

	pc := &hypv1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "ml"},
		Status: hypv1alpha1.ProviderConfigStatus{
			BalanceCents: ptr(int64(124035)), CostCentsPerHour: ptr(int64(2500)), ManagedRentals: 3,
			Conditions: []metav1.Condition{
				{Type: hypv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: now},
			},
		},
	}
	cpc := &hypv1alpha1.ClusterProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "shared"},
		Status: hypv1alpha1.ProviderConfigStatus{
			BalanceCents: ptr(int64(500)),
			Conditions: []metav1.Condition{
				{Type: hypv1alpha1.ConditionSynced, Status: metav1.ConditionFalse, Reason: hypv1alpha1.ReasonAPIError, LastTransitionTime: now},
			},
		},
	}

	reader := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(running, deleting, stopping, record, blocked, pc, cpc).Build()

	// Money is Hyperbolic's to report, so neither prices nor balances are
	// exported.
	const want = `
# HELP rental_operator_gpurental_status Always 1, labeled with the GPURental's phase and the reason on its Ready condition.
# TYPE rental_operator_gpurental_status gauge
rental_operator_gpurental_status{name="blocked",namespace="ml",phase="Blocked",reason="AvailabilityUnavailable"} 1
rental_operator_gpurental_status{name="deleting",namespace="ml",phase="Terminating",reason="Terminating"} 1
rental_operator_gpurental_status{name="record",namespace="ml",phase="Terminated",reason="Terminated"} 1
rental_operator_gpurental_status{name="running",namespace="ml",phase="Running",reason="Running"} 1
rental_operator_gpurental_status{name="stopping",namespace="ml",phase="Running",reason="Running"} 1
# HELP rental_operator_gpurental_termination_pending 1 while a GPURental has been deleted or set to terminated and its rental is not confirmed gone yet.
# TYPE rental_operator_gpurental_termination_pending gauge
rental_operator_gpurental_termination_pending{name="deleting",namespace="ml"} 1
rental_operator_gpurental_termination_pending{name="stopping",namespace="ml"} 1
# HELP rental_operator_providerconfig_condition Always 1, labeled with each condition's type and status.
# TYPE rental_operator_providerconfig_condition gauge
rental_operator_providerconfig_condition{kind="ClusterProviderConfig",name="shared",namespace="",status="False",type="Synced"} 1
rental_operator_providerconfig_condition{kind="ProviderConfig",name="default",namespace="ml",status="True",type="Ready"} 1
# HELP rental_operator_providerconfig_managed_rentals GPURentals using the config that have not finished.
# TYPE rental_operator_providerconfig_managed_rentals gauge
rental_operator_providerconfig_managed_rentals{kind="ClusterProviderConfig",name="shared",namespace=""} 0
rental_operator_providerconfig_managed_rentals{kind="ProviderConfig",name="default",namespace="ml"} 3
`
	if err := testutil.CollectAndCompare(NewStatusCollector(reader), strings.NewReader(want)); err != nil {
		t.Error(err)
	}
	problems, err := testutil.CollectAndLint(NewStatusCollector(reader))
	if err != nil || len(problems) > 0 {
		t.Errorf("lint: %v %v", problems, err)
	}
}

func TestStatusCollectorSkipsWhatItCannotList(t *testing.T) {
	// An empty scheme cannot list any kind, like a cache that is not ready.
	reader := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
	if n := testutil.CollectAndCount(NewStatusCollector(reader)); n != 0 {
		t.Errorf("collected %d metrics, want 0", n)
	}
}
