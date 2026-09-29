/*
Copyright 2026.

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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/HyperbolicLabs/hyperbolic-operator/internal/hyperbolic"
)

// CatalogCache memoizes rental-options results per credential, so the operator
// calls castle once per account per TTL no matter how many GPUAvailability /
// GPURental objects consume the catalogue.
//
// The catalogue endpoint personalizes its results to the API key (an
// authenticated call returns the account's available offerings; a keyless call
// returns only the generic public view), so entries are keyed by credential —
// not shared globally.
type CatalogCache struct {
	ttl     time.Duration
	mu      sync.RWMutex
	entries map[string]catalogEntry
	group   singleflight.Group
}

type catalogEntry struct {
	options   []hyperbolic.RentalOption
	fetchedAt time.Time
	err       error
}

// NewCatalogCache builds a cache with the given freshness TTL.
func NewCatalogCache(ttl time.Duration) *CatalogCache {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &CatalogCache{ttl: ttl, entries: map[string]catalogEntry{}}
}

// credKey derives a stable, non-reversible cache key from a credential.
func credKey(endpoint, apiKey string) string {
	sum := sha256.Sum256([]byte(endpoint + "\x00" + apiKey))
	return hex.EncodeToString(sum[:8])
}

// Get returns the account's catalogue: cached if fresh, otherwise fetched once
// (concurrent callers for the same credential share a single fetch). On a fetch
// error it returns the last-known options (if any) alongside the error.
func (c *CatalogCache) Get(ctx context.Context, hc *hyperbolic.Client, key string) (options []hyperbolic.RentalOption, fetchedAt time.Time, err error) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if ok && e.err == nil && time.Since(e.fetchedAt) < c.ttl {
		return e.options, e.fetchedAt, nil
	}

	_, fetchErr, _ := c.group.Do(key, func() (interface{}, error) {
		opts, ferr := hc.GetRentalOptions(ctx)
		now := time.Now()
		c.mu.Lock()
		if ferr != nil {
			prev := c.entries[key] // keep any prior options on error
			c.entries[key] = catalogEntry{options: prev.options, fetchedAt: now, err: ferr}
		} else {
			c.entries[key] = catalogEntry{options: opts, fetchedAt: now}
		}
		c.mu.Unlock()
		return opts, ferr
	})

	c.mu.RLock()
	e = c.entries[key]
	c.mu.RUnlock()
	return e.options, e.fetchedAt, fetchErr
}
