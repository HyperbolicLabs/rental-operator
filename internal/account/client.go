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

package account

import (
	"net/http"
	"time"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
)

// RequestTimeout bounds every request to the Hyperbolic API, so it is also
// the longest a create with an unknown outcome can still be in flight.
const RequestTimeout = 30 * time.Second

// ClientFactory builds a Hyperbolic API client for an endpoint and API key.
type ClientFactory func(endpoint, apiKey string) (*hyperbolic.Client, error)

// NewClientFactory returns the ClientFactory the manager uses. Its clients
// identify the operator in the User-Agent header and never retry on their
// own: reconcilers requeue on errors, so a reconcile never blocks on backoff.
// They share one HTTP client over transport (nil: http.DefaultTransport)
// that gives up after RequestTimeout.
func NewClientFactory(userAgent string, transport http.RoundTripper) ClientFactory {
	httpClient := &http.Client{Timeout: RequestTimeout, Transport: transport}
	return func(endpoint, apiKey string) (*hyperbolic.Client, error) {
		return hyperbolic.NewClient(apiKey,
			hyperbolic.WithBaseURL(endpoint),
			hyperbolic.WithUserAgent(userAgent),
			hyperbolic.WithHTTPClient(httpClient),
			hyperbolic.WithRetryPolicy(hyperbolic.NoRetries()),
		)
	}
}
