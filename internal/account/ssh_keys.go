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
	"strings"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
)

// SameSSHKey reports whether two authorized_keys lines hold the same key:
// the same type and body, whatever their comments. The account holds each
// key once by the same rule.
func SameSSHKey(a, b string) bool {
	fa, fb := strings.Fields(a), strings.Fields(b)
	return len(fa) >= 2 && len(fb) >= 2 && fa[0] == fb[0] && fa[1] == fb[1]
}

// SavedSSHKey returns the account's saved copy of publicKey, or nil when the
// account does not have it.
func SavedSSHKey(ctx context.Context, hc *hyperbolic.Client, publicKey string) (*hyperbolic.SSHKeyWithLiveRentalCount, error) {
	keys, err := hc.SSHKeys.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range keys {
		if SameSSHKey(keys[i].PublicKey, publicKey) {
			return &keys[i], nil
		}
	}
	return nil, nil
}

// IsSSHKeyInUse reports whether deleting a key failed because a live rental
// uses it. The spec documents the code SSH_PUBLIC_KEY_IN_USE, and the live
// API answers 409 CONFLICT.
func IsSSHKeyInUse(err error) bool {
	return hyperbolic.HasCode(err, hyperbolic.CodeSSHPublicKeyInUse) || hyperbolic.StatusCode(err) == http.StatusConflict
}

// DeleteSSHKey deletes a saved key. One that is already gone counts as
// deleted, so deleting twice succeeds. A key that a live rental uses is not
// deleted, and IsSSHKeyInUse reports the error.
func DeleteSSHKey(ctx context.Context, hc *hyperbolic.Client, id int) error {
	_, err := hc.SSHKeys.Delete(ctx, hyperbolic.DeleteSSHKeyParams{ID: id})
	if err == nil || hyperbolic.IsNotFound(err) {
		return nil
	}
	if IsSSHKeyInUse(err) {
		return err
	}
	// How the live API answers for a key that is already gone has not been
	// observed, so the list settles whether it is.
	keys, listErr := hc.SSHKeys.List(ctx)
	if listErr != nil {
		return err
	}
	for _, k := range keys {
		if k.ID == id {
			return err
		}
	}
	return nil
}
