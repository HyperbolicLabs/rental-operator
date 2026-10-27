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
	"context"
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

var (
	rentalLabels = []string{"namespace", "name"}
	configLabels = []string{"kind", "namespace", "name"}

	rentalStatusDesc = prometheus.NewDesc(prefix+"gpurental_status",
		"Always 1, labeled with the GPURental's phase and the reason on its Ready condition.",
		slices.Concat(rentalLabels, []string{"phase", "reason"}), nil)
	rentalTerminationPendingDesc = prometheus.NewDesc(prefix+"gpurental_termination_pending",
		"1 while a GPURental has been deleted or set to terminated and its rental is not confirmed gone yet.",
		rentalLabels, nil)

	configManagedDesc = prometheus.NewDesc(prefix+"providerconfig_managed_rentals",
		"GPURentals using the config that have not finished.", configLabels, nil)
	configConditionDesc = prometheus.NewDesc(prefix+"providerconfig_condition",
		"Always 1, labeled with each condition's type and status.",
		slices.Concat(configLabels, []string{"type", "status"}), nil)
)

// collectTimeout bounds the cache reads of one scrape.
const collectTimeout = 3 * time.Second

// statusCollector reports GPURentals, ProviderConfigs and
// ClusterProviderConfigs from reader (the manager's cache) at scrape time.
type statusCollector struct {
	reader client.Reader
}

// NewStatusCollector returns a collector of the objects' status gauges.
// Register it with the controller-runtime metrics registry.
func NewStatusCollector(reader client.Reader) prometheus.Collector {
	return statusCollector{reader: reader}
}

func (c statusCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		rentalStatusDesc, rentalTerminationPendingDesc, configManagedDesc, configConditionDesc,
	} {
		ch <- d
	}
}

// Collect reads from the cache. A kind that cannot be listed in time (the
// cache is still starting) is left out of this scrape.
func (c statusCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), collectTimeout)
	defer cancel()

	var rentals hypv1alpha1.GPURentalList
	if err := c.reader.List(ctx, &rentals); err == nil {
		for i := range rentals.Items {
			collectRental(ch, &rentals.Items[i])
		}
	}
	var configs hypv1alpha1.ProviderConfigList
	if err := c.reader.List(ctx, &configs); err == nil {
		for i := range configs.Items {
			pc := &configs.Items[i]
			collectConfig(ch, []string{hypv1alpha1.ProviderConfigKind, pc.Namespace, pc.Name}, &pc.Status)
		}
	}
	var clusterConfigs hypv1alpha1.ClusterProviderConfigList
	if err := c.reader.List(ctx, &clusterConfigs); err == nil {
		for i := range clusterConfigs.Items {
			cpc := &clusterConfigs.Items[i]
			collectConfig(ch, []string{hypv1alpha1.ClusterProviderConfigKind, "", cpc.Name}, &cpc.Status)
		}
	}
}

func collectRental(ch chan<- prometheus.Metric, gr *hypv1alpha1.GPURental) {
	reason := ""
	if ready := meta.FindStatusCondition(gr.Status.Conditions, hypv1alpha1.ConditionReady); ready != nil {
		reason = ready.Reason
	}
	ch <- prometheus.MustNewConstMetric(rentalStatusDesc, prometheus.GaugeValue, 1,
		gr.Namespace, gr.Name, gr.Status.Phase, reason)

	if gr.Status.TerminatedAt == nil && (!gr.DeletionTimestamp.IsZero() || gr.Spec.Terminated) {
		ch <- prometheus.MustNewConstMetric(rentalTerminationPendingDesc, prometheus.GaugeValue, 1,
			gr.Namespace, gr.Name)
	}
}

func collectConfig(ch chan<- prometheus.Metric, labels []string, st *hypv1alpha1.ProviderConfigStatus) {
	ch <- prometheus.MustNewConstMetric(configManagedDesc, prometheus.GaugeValue, float64(st.ManagedRentals), labels...)
	for _, c := range st.Conditions {
		ch <- prometheus.MustNewConstMetric(configConditionDesc, prometheus.GaugeValue, 1,
			slices.Concat(labels, []string{c.Type, string(c.Status)})...)
	}
}
