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
	"context"
	"sync"
	"time"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	"golang.org/x/sync/singleflight"
	"k8s.io/utils/clock"
)

const (
	// rentalListProvisioningTTL is how long a list stays fresh while any
	// rental on the account is still starting.
	rentalListProvisioningTTL = 10 * time.Second
	// rentalListTTL is how long a list stays fresh otherwise.
	rentalListTTL = 60 * time.Second
)

// RentalCache shares one list of an account's active rentals among every
// GPURental on that account, so N objects cost one API call per interval
// instead of N. Lists stay fresh for 10s while anything on the account is
// starting and 60s otherwise. Creates and terminates invalidate the
// account's list, and a fetch that started before an invalidation is never
// stored, so a list taken before a create cannot hide the new rental.
type RentalCache struct {
	clock   clock.PassiveClock
	mu      sync.Mutex
	entries map[string]rentalEntry
	// gens counts invalidations per account. A fetch is stored only if no
	// invalidation happened while it was in flight.
	gens  map[string]uint64
	group singleflight.Group
}

type rentalEntry struct {
	rentals   []hyperbolic.RentalWithStatus
	fetchedAt time.Time
}

// NewRentalCache returns an empty cache. A nil clock uses the real one.
func NewRentalCache(c clock.PassiveClock) *RentalCache {
	if c == nil {
		c = clock.RealClock{}
	}
	return &RentalCache{clock: c, entries: map[string]rentalEntry{}, gens: map[string]uint64{}}
}

// List returns the account's active rentals, from the cache if fresh.
// Concurrent callers for the same account share one fetch.
func (c *RentalCache) List(ctx context.Context, hc *hyperbolic.Client, key string) ([]hyperbolic.RentalWithStatus, error) {
	c.mu.Lock()
	e, ok := c.entries[key]
	gen := c.gens[key]
	c.mu.Unlock()
	if ok && c.clock.Since(e.fetchedAt) < ttlFor(e.rentals) {
		return e.rentals, nil
	}

	v, err, _ := c.group.Do(key, func() (any, error) {
		rentals, err := listRentals(ctx, hc)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		if c.gens[key] == gen {
			c.entries[key] = rentalEntry{rentals: rentals, fetchedAt: c.clock.Now()}
		}
		c.mu.Unlock()
		return rentals, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]hyperbolic.RentalWithStatus), nil
}

// Invalidate drops the account's list, after a create or terminate.
func (c *RentalCache) Invalidate(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	c.gens[key]++
}

// ttlFor is short while any rental is starting, so new rentals show up fast.
func ttlFor(rentals []hyperbolic.RentalWithStatus) time.Duration {
	for _, vm := range rentals {
		if vm.Status == hyperbolic.RentalStatusPending {
			return rentalListProvisioningTTL
		}
	}
	return rentalListTTL
}
