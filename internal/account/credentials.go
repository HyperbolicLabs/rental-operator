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

// Package account is how the operator reaches a Hyperbolic account: it
// resolves the API key a ProviderConfig or ClusterProviderConfig points at,
// builds API clients for it, caches what every object using the same
// account shares (its active rentals and its rental options), and holds
// the operator's policies for listing, adopting and ending its rentals.
package account

import (
	"crypto/sha256"
	"encoding/hex"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
)

// Key identifies an account for caching. It is derived from the endpoint and
// API key but cannot be turned back into either.
type Key string

// KeyFor returns the Key of the account an API key reaches at endpoint.
func KeyFor(endpoint, apiKey string) Key {
	sum := sha256.Sum256([]byte(endpoint + "\x00" + apiKey))
	return Key(hex.EncodeToString(sum[:8]))
}

// Credentials is what a ProviderConfig or ClusterProviderConfig resolves to:
// an endpoint and API key. Formatting Credentials never shows the API key.
type Credentials struct {
	Endpoint string
	apiKey   string
}

// Key identifies the account these credentials reach.
func (c Credentials) Key() Key { return KeyFor(c.Endpoint, c.apiKey) }

// Client builds an API client for the account with f.
func (c Credentials) Client(f ClientFactory) (*hyperbolic.Client, error) {
	return f(c.Endpoint, c.apiKey)
}

// String names the endpoint only.
func (c Credentials) String() string { return "credentials for " + c.Endpoint }

// GoString keeps %#v from printing the API key.
func (c Credentials) GoString() string { return c.String() }
