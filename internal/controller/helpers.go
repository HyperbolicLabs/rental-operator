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

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// patchFinalizers applies change to obj's finalizers and writes only them,
// failing on a concurrent change instead of overwriting it. An Update would
// write the whole object back as the Go types serialize it ("ttl: 8h" becomes
// "8h0m0s"), which bumps the generation and shows as drift in GitOps tools.
func patchFinalizers(ctx context.Context, c client.Client, obj client.Object, change func()) error {
	base := obj.DeepCopyObject().(client.Object)
	change()
	return c.Patch(ctx, obj, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}
