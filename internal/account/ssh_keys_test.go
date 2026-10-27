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
	"net/http"
	"testing"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	"github.com/HyperbolicLabs/hyperbolic-go/hyperbolictest"
)

const aliceKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGFsaWNlLWtleS1mb3ItdGhlLWFjY291bnQtdGVzdHMtMDA alice@laptop"

func TestSameSSHKeyIgnoresTheComment(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{aliceKey, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGFsaWNlLWtleS1mb3ItdGhlLWFjY291bnQtdGVzdHMtMDA", true},
		{aliceKey, "ssh-ed25519  AAAAC3NzaC1lZDI1NTE5AAAAIGFsaWNlLWtleS1mb3ItdGhlLWFjY291bnQtdGVzdHMtMDA alice@desktop\n", true},
		{aliceKey, hyperbolictest.DefaultSSHPublicKey, false},
		{aliceKey, "ssh-rsa AAAAC3NzaC1lZDI1NTE5AAAAIGFsaWNlLWtleS1mb3ItdGhlLWFjY291bnQtdGVzdHMtMDA", false},
		{"", "", false},
	} {
		if got := SameSSHKey(tc.a, tc.b); got != tc.want {
			t.Errorf("SameSSHKey(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSavedSSHKey(t *testing.T) {
	api := hyperbolictest.NewServer(t)
	c := api.Client(t)
	if k, err := SavedSSHKey(context.Background(), c, aliceKey); err != nil || k != nil {
		t.Fatalf("before saving: %+v, %v; want nothing", k, err)
	}
	saved := api.AddSSHKey(aliceKey, "alice")
	k, err := SavedSSHKey(context.Background(), c, aliceKey)
	if err != nil || k == nil || k.ID != saved.ID {
		t.Fatalf("after saving: %+v, %v; want key %d", k, err, saved.ID)
	}
}

func TestDeleteSSHKey(t *testing.T) {
	ctx := context.Background()
	api := hyperbolictest.NewServer(t)
	// Like the operator's clients, this one does not retry, so a fault
	// reaches DeleteSSHKey.
	c, err := hyperbolic.NewClient(api.APIKey, hyperbolic.WithBaseURL(api.URL), hyperbolic.WithRetryPolicy(hyperbolic.NoRetries()))
	if err != nil {
		t.Fatal(err)
	}
	alice := api.AddSSHKey(aliceKey, "alice")

	for i := range 2 {
		if err := DeleteSSHKey(ctx, c, alice.ID); err != nil {
			t.Fatalf("delete #%d: %v", i+1, err)
		}
	}

	t.Run("a failed delete that took effect counts", func(t *testing.T) {
		k := api.AddSSHKey(aliceKey, "alice")
		api.InjectFault(hyperbolictest.Fault{
			Method: http.MethodPost, Path: hyperbolictest.PathSSHKeyDelete,
			Status: http.StatusBadGateway, AfterCommit: true,
		})
		if err := DeleteSSHKey(ctx, c, k.ID); err != nil {
			t.Fatalf("err = %v, want nil: the key is gone", err)
		}
	})

	t.Run("a failed delete that did not take effect is an error", func(t *testing.T) {
		k := api.AddSSHKey(aliceKey, "alice")
		api.InjectFault(hyperbolictest.Fault{
			Method: http.MethodPost, Path: hyperbolictest.PathSSHKeyDelete,
			Status: http.StatusBadGateway,
		})
		if err := DeleteSSHKey(ctx, c, k.ID); hyperbolic.StatusCode(err) != http.StatusBadGateway {
			t.Fatalf("err = %v, want the 502", err)
		}
	})

	t.Run("a key in use is not deleted", func(t *testing.T) {
		k, err := SavedSSHKey(ctx, c, aliceKey)
		if err != nil || k == nil {
			t.Fatalf("saved key: %+v, %v", k, err)
		}
		rent(t, c, "train") // authorizes the newest key, alice's
		if err := DeleteSSHKey(ctx, c, k.ID); !IsSSHKeyInUse(err) {
			t.Fatalf("err = %v, want one IsSSHKeyInUse reports", err)
		}
	})
}

func TestDeleteSSHKeyInUseAsTheLiveAPIAnswers(t *testing.T) {
	api := hyperbolictest.NewServer(t)
	c := api.Client(t)
	k := api.AddSSHKey(aliceKey, "alice")
	// What the live API answered for a key on a running rental.
	api.InjectFault(hyperbolictest.Fault{
		Method: http.MethodPost, Path: hyperbolictest.PathSSHKeyDelete,
		Status: http.StatusConflict, Code: "CONFLICT",
		Message: "SSH public key is still attached to a live instance; it can be deleted once they are terminated",
	})
	if err := DeleteSSHKey(context.Background(), c, k.ID); !IsSSHKeyInUse(err) {
		t.Fatalf("err = %v, want one IsSSHKeyInUse reports", err)
	}
}
