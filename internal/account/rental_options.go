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
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
)

// RentalOptionsCache memoizes each account's rental options, so the operator
// lists them once per account per TTL no matter how many GPUAvailability and
// GPURental objects read them.
//
// The rental-options endpoint personalizes its results to the API key (an
// authenticated call returns the account's available offerings. A keyless
// call returns only the public view), so entries are kept per account, never
// shared.
type RentalOptionsCache struct {
	ttl     time.Duration
	mu      sync.RWMutex
	entries map[Key]rentalOptionsEntry
	group   singleflight.Group
}

type rentalOptionsEntry struct {
	options   []hyperbolic.RentalOption
	fetchedAt time.Time
	err       error
}

// NewRentalOptionsCache builds a cache whose entries stay fresh for ttl
// (5 minutes if ttl is not positive).
func NewRentalOptionsCache(ttl time.Duration) *RentalOptionsCache {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &RentalOptionsCache{ttl: ttl, entries: map[Key]rentalOptionsEntry{}}
}

// Get returns the account's rental options: cached if fresh, otherwise
// fetched once (concurrent callers for the same account share a single
// fetch). On a fetch error it returns the last-known options, if any,
// alongside the error.
func (c *RentalOptionsCache) Get(ctx context.Context, hc *hyperbolic.Client, key Key) (options []hyperbolic.RentalOption, fetchedAt time.Time, err error) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if ok && e.err == nil && time.Since(e.fetchedAt) < c.ttl {
		return e.options, e.fetchedAt, nil
	}

	_, fetchErr, _ := c.group.Do(string(key), func() (interface{}, error) {
		opts, ferr := hc.Rentals.Options(ctx)
		now := time.Now()
		c.mu.Lock()
		if ferr != nil {
			prev := c.entries[key] // keep any prior options on error
			c.entries[key] = rentalOptionsEntry{options: prev.options, fetchedAt: now, err: ferr}
		} else {
			c.entries[key] = rentalOptionsEntry{options: opts, fetchedAt: now}
		}
		c.mu.Unlock()
		return opts, ferr
	})

	c.mu.RLock()
	e = c.entries[key]
	c.mu.RUnlock()
	return e.options, e.fetchedAt, fetchErr
}

// VirtualMachineOptions returns the virtual machine options among an account's rental
// options.
func VirtualMachineOptions(options []hyperbolic.RentalOption) []hyperbolic.VirtualMachineOption {
	out := make([]hyperbolic.VirtualMachineOption, 0, len(options))
	for _, o := range options {
		vm, err := o.AsVirtualMachineOption()
		if err == nil && vm.MachineType == hyperbolic.VirtualMachineOptionMachineTypeVirtualMachine {
			out = append(out, vm)
		}
	}
	return out
}
