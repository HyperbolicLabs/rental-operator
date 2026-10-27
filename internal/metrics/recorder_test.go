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
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestChartUsesKnownMetrics keeps the chart's alert rules in step with the
// metrics the operator exports.
func TestChartUsesKnownMetrics(t *testing.T) {
	known := map[string]bool{}
	descs := make(chan *prometheus.Desc, 64)
	rec := NewRecorder()
	for _, c := range []prometheus.Collector{rec.rentalOperations, rec.apiRequests, NewStatusCollector(nil)} {
		c.Describe(descs)
	}
	close(descs)
	fqName := regexp.MustCompile(`fqName: "([^"]+)"`)
	for d := range descs {
		known[fqName.FindStringSubmatch(d.String())[1]] = true
	}
	histograms := make(chan *prometheus.Desc, 1)
	rec.apiRequestDuration.Describe(histograms)
	base := fqName.FindStringSubmatch((<-histograms).String())[1]
	for _, suffix := range []string{"_bucket", "_sum", "_count"} {
		known[base+suffix] = true
	}

	used := regexp.MustCompile(`rental_operator_[a-z_]+`)
	for _, file := range []string{"../../charts/rental-operator/files/alerts.yaml"} {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		names := used.FindAllString(string(content), -1)
		if len(names) == 0 {
			t.Errorf("%s uses no rental_operator_ metrics", file)
		}
		for _, name := range names {
			if !known[name] {
				t.Errorf("%s uses %s, which the operator does not export", file, name)
			}
		}
	}
}

func TestPathLabel(t *testing.T) {
	for path, want := range map[string]string{
		"/v2/on-demand/rentals/active":    "/v2/on-demand/rentals/active",
		"/v2/on-demand/rentals/terminate": "/v2/on-demand/rentals/terminate",
		"/v2/on-demand/rentals/8817":      "/v2/on-demand/rentals/:id",
		"/api/v2/customer/balance":        "/api/v2/customer/balance",
		"":                                "",
	} {
		if got := pathLabel(path); got != want {
			t.Errorf("pathLabel(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	rec := NewRecorder()
	hc := &http.Client{Transport: rec.Transport(nil)}
	get := func() {
		resp, err := hc.Get(srv.URL + "/metrics-test/42")
		if err == nil {
			_ = resp.Body.Close()
		}
	}

	served := rec.apiRequests.WithLabelValues(http.MethodGet, "/metrics-test/:id", "503")
	failed := rec.apiRequests.WithLabelValues(http.MethodGet, "/metrics-test/:id", "error")
	before, beforeFailed := testutil.ToFloat64(served), testutil.ToFloat64(failed)

	get()
	srv.Close()
	get()

	if d := testutil.ToFloat64(served) - before; d != 1 {
		t.Errorf("503 responses counted %v times, want 1", d)
	}
	if d := testutil.ToFloat64(failed) - beforeFailed; d != 1 {
		t.Errorf("requests with no response counted %v times, want 1", d)
	}
}

func TestRentalOperationOutcomes(t *testing.T) {
	rec := NewRecorder()
	count := func(op, result string) float64 {
		return testutil.ToFloat64(rec.rentalOperations.WithLabelValues(op, result))
	}

	rec.RentalCreated(nil)
	rec.RentalCreated(errors.New("400 INSUFFICIENT_BALANCE"))
	rec.RentalCreated(hyperbolic.ErrOutcomeUnknown)
	rec.RentalTerminated(nil)
	rec.RentalTerminated(errors.New("503"))
	for _, c := range []struct{ op, result string }{
		{OpCreate, ResultSuccess}, {OpCreate, ResultFailed}, {OpCreate, ResultUnknown},
		{OpTerminate, ResultSuccess}, {OpTerminate, ResultFailed},
	} {
		if got := count(c.op, c.result); got != 1 {
			t.Errorf("%s %s = %v, want 1", c.op, c.result, got)
		}
	}
}

func TestNilRecorderRecordsNothing(t *testing.T) {
	var rec *Recorder
	rec.RentalCreated(nil)
	rec.RentalTerminated(nil)
	if rec.Transport(nil) != http.DefaultTransport {
		t.Error("a nil Recorder should hand back the transport unchanged")
	}
}

func TestRegisterTwiceFails(t *testing.T) {
	reg := prometheus.NewRegistry()
	if err := NewRecorder().Register(reg); err != nil {
		t.Fatal(err)
	}
	if err := NewRecorder().Register(reg); err == nil {
		t.Error("registering a second recorder in one registry succeeded")
	}
}
