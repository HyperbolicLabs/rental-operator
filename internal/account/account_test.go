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
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	"github.com/HyperbolicLabs/hyperbolic-go/hyperbolictest"
	clocktesting "k8s.io/utils/clock/testing"
)

func TestKeyIsStableAndSeparatesAccounts(t *testing.T) {
	a := KeyFor("https://api.hyperbolic.ai", "sk_one")
	if a != KeyFor("https://api.hyperbolic.ai", "sk_one") {
		t.Error("the same account gave two keys")
	}
	if a == KeyFor("https://api.hyperbolic.ai", "sk_two") || a == KeyFor("https://staging.example", "sk_one") {
		t.Error("two accounts share a key")
	}
	if strings.Contains(string(a), "sk_one") {
		t.Error("the key contains the API key")
	}
}

func TestCredentialsNeverFormatTheAPIKey(t *testing.T) {
	c := Credentials{Endpoint: "https://api.hyperbolic.ai", apiKey: "sk_secret"}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if out := fmt.Sprintf(verb, c); strings.Contains(out, "sk_secret") {
			t.Errorf("%s printed the API key: %s", verb, out)
		}
	}
}

func TestRentalCacheDropsAListFetchedAcrossAnInvalidation(t *testing.T) {
	srv := hyperbolictest.NewServer(t)
	hc := srv.Client(t, hyperbolic.WithRetryPolicy(hyperbolic.NoRetries()))
	cache := NewRentalCache(clocktesting.NewFakePassiveClock(time.Now()))
	key := KeyFor(srv.URL, srv.APIKey)
	lists := func() int { return srv.Requests(http.MethodGet, hyperbolictest.PathActiveRentals) }
	srv.InjectFault(hyperbolictest.Fault{Method: http.MethodGet, Path: hyperbolictest.PathActiveRentals, Delay: 300 * time.Millisecond})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := cache.List(context.Background(), hc, key); err != nil {
			t.Error(err)
		}
	}()
	for lists() == 0 { // wait until the slow fetch is in flight
		time.Sleep(10 * time.Millisecond)
	}
	cache.Invalidate(key) // a create happens meanwhile
	wg.Wait()

	if _, err := cache.List(context.Background(), hc, key); err != nil {
		t.Fatal(err)
	}
	if n := lists(); n != 2 {
		t.Fatalf("listed %d times, want 2: the list fetched across the invalidation was cached", n)
	}
}

func TestRentalOptionsCacheSharesFetchesAndKeepsTheLastGoodOptions(t *testing.T) {
	srv := hyperbolictest.NewServer(t)
	hc := srv.Client(t, hyperbolic.WithRetryPolicy(hyperbolic.NoRetries()))
	cache := NewRentalOptionsCache(time.Nanosecond)
	key := KeyFor(srv.URL, srv.APIKey)
	ctx := context.Background()

	opts, _, err := cache.Get(ctx, hc, key)
	if err != nil || len(opts) != len(hyperbolictest.DefaultRentalOptions()) {
		t.Fatalf("first get: %d options, err %v", len(opts), err)
	}
	srv.InjectFault(hyperbolictest.Fault{Method: http.MethodGet, Path: hyperbolictest.PathRentalOptions, Status: http.StatusServiceUnavailable})
	time.Sleep(time.Millisecond) // past the TTL
	opts, _, err = cache.Get(ctx, hc, key)
	if err == nil || len(opts) == 0 {
		t.Fatalf("failed refresh: %d options, err %v; want the last options and the error", len(opts), err)
	}

	fresh := NewRentalOptionsCache(time.Hour)
	before := srv.Requests(http.MethodGet, hyperbolictest.PathRentalOptions)
	for range 5 {
		if _, _, err := fresh.Get(ctx, hc, key); err != nil {
			t.Fatal(err)
		}
	}
	if n := srv.Requests(http.MethodGet, hyperbolictest.PathRentalOptions) - before; n != 1 {
		t.Fatalf("5 reads within the TTL made %d requests, want 1", n)
	}
}
