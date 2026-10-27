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

// Package metrics defines rental-operator's Prometheus metrics: a Recorder
// that the reconcilers and the Hyperbolic API client update as things happen,
// and a collector that reports the operator's objects at scrape time.
//
// Nothing is registered on import. The manager registers both explicitly.
// docs/observability.md lists every metric and the alerts built on them.
package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	"github.com/prometheus/client_golang/prometheus"
)

const prefix = "rental_operator_"

// Values of the operation and result labels of rental_operations_total.
const (
	OpCreate    = "create"
	OpTerminate = "terminate"

	ResultSuccess = "success"
	ResultFailed  = "failed"
	// ResultUnknown is a create that timed out or failed with a 5xx: it may
	// have rented, and is settled by looking the rental up by label.
	ResultUnknown = "unknown"
)

// Recorder holds the metrics updated as rentals are created and terminated
// and as the Hyperbolic API is called. A nil Recorder records nothing.
type Recorder struct {
	rentalOperations   *prometheus.CounterVec
	apiRequests        *prometheus.CounterVec
	apiRequestDuration *prometheus.HistogramVec
}

// NewRecorder returns a Recorder whose operation counters start at zero for
// every outcome, so increase() sees the first one.
func NewRecorder() *Recorder {
	r := &Recorder{
		rentalOperations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "rental_operations_total",
			Help: "Rental creates and terminations sent to the Hyperbolic API, by outcome (success, failed, or unknown for a create that may have rented).",
		}, []string{"operation", "result"}),
		apiRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "api_requests_total",
			Help: `Requests to the Hyperbolic API, by method, path and HTTP status code ("error" when no response arrived).`,
		}, []string{"method", "path", "code"}),
		apiRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    prefix + "api_request_duration_seconds",
			Help:    "Time until the Hyperbolic API's response headers arrived, by method and path.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}, []string{"method", "path"}),
	}
	for _, res := range []string{ResultSuccess, ResultFailed, ResultUnknown} {
		r.rentalOperations.WithLabelValues(OpCreate, res)
	}
	for _, res := range []string{ResultSuccess, ResultFailed} {
		r.rentalOperations.WithLabelValues(OpTerminate, res)
	}
	return r
}

// Register registers the recorder's metrics with reg.
func (r *Recorder) Register(reg prometheus.Registerer) error {
	for _, c := range []prometheus.Collector{r.rentalOperations, r.apiRequests, r.apiRequestDuration} {
		if err := reg.Register(c); err != nil {
			return err
		}
	}
	return nil
}

// RentalCreated records the outcome of a create call that returned err.
func (r *Recorder) RentalCreated(err error) {
	if r == nil {
		return
	}
	result := ResultSuccess
	switch {
	case hyperbolic.IsOutcomeUnknown(err):
		result = ResultUnknown
	case err != nil:
		result = ResultFailed
	}
	r.rentalOperations.WithLabelValues(OpCreate, result).Inc()
}

// RentalTerminated records the outcome of a terminate call that returned err.
func (r *Recorder) RentalTerminated(err error) {
	if r == nil {
		return
	}
	result := ResultSuccess
	if err != nil {
		result = ResultFailed
	}
	r.rentalOperations.WithLabelValues(OpTerminate, result).Inc()
}

// Transport wraps next (nil means http.DefaultTransport) so that every
// request through it is counted and timed.
func (r *Recorder) Transport(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	if r == nil {
		return next
	}
	return transport{next: next, recorder: r}
}

type transport struct {
	next     http.RoundTripper
	recorder *Recorder
}

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	path := pathLabel(req.URL.Path)
	start := time.Now()
	resp, err := t.next.RoundTrip(req)
	t.recorder.apiRequestDuration.WithLabelValues(req.Method, path).Observe(time.Since(start).Seconds())
	code := "error"
	if err == nil {
		code = strconv.Itoa(resp.StatusCode)
	}
	t.recorder.apiRequests.WithLabelValues(req.Method, path, code).Inc()
	return resp, err
}

// pathLabel turns numeric path segments into ":id", so the path label stays
// bounded if the API ever puts ids in paths.
func pathLabel(path string) string {
	segments := strings.Split(path, "/")
	for i, s := range segments {
		if s != "" && strings.Trim(s, "0123456789") == "" {
			segments[i] = ":id"
		}
	}
	return strings.Join(segments, "/")
}
