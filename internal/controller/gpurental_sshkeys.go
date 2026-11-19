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
	"fmt"
	"net/http"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

// eventSSHKeysUpdated is emitted on a GPURental, with action
// actionUpdateSSHKeys, when the keys from its spec.sshKeyRefs are attached to
// or detached from its rental.
const (
	eventSSHKeysUpdated = "SSHKeysUpdated"
	actionUpdateSSHKeys = "UpdateSSHKeys"
)

// sshKeyIDs resolves spec.sshKeyRefs to the ids of the saved keys, sorted.
// When a key cannot be used yet, it returns a message saying why instead.
func (r *GPURentalReconciler) sshKeyIDs(ctx context.Context, gr *hypv1alpha1.GPURental) (ids []int64, notReady string, err error) {
	for _, ref := range gr.Spec.SSHKeyRefs {
		var key hypv1alpha1.SSHKey
		if err := r.Get(ctx, client.ObjectKey{Namespace: gr.Namespace, Name: ref.Name}, &key); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, fmt.Sprintf("SSHKey %s does not exist", ref.Name), nil
			}
			return nil, "", err
		}
		switch {
		case !refersTo(key.Spec.ProviderConfigRef, gr.Spec.ProviderConfigRef):
			return nil, fmt.Sprintf("SSHKey %s is saved with %s, and this rental uses %s",
				ref.Name, describeConfig(key.Spec.ProviderConfigRef), describeConfig(gr.Spec.ProviderConfigRef)), nil
		case !key.DeletionTimestamp.IsZero():
			return nil, fmt.Sprintf("SSHKey %s is being deleted", ref.Name), nil
		case key.Status.ID == 0 || !apimeta.IsStatusConditionTrue(key.Status.Conditions, hypv1alpha1.ConditionReady):
			return nil, fmt.Sprintf("SSHKey %s is not Ready yet", ref.Name), nil
		}
		ids = append(ids, key.Status.ID)
	}
	slices.Sort(ids)
	return ids, "", nil
}

// applySSHKeys brings the keys authorized on a live rental in line with
// spec.sshKeyRefs: it attaches the keys that were added and detaches the
// ones that were removed. Keys the operator did not attach, such as the one
// the API authorized by default, are left alone. A key that cannot be used
// yet, or a failed call, is reported on Synced and retried on the next poll.
func (r *GPURentalReconciler) applySSHKeys(ctx context.Context, gr *hypv1alpha1.GPURental, hc *hyperbolic.Client, rentalID string) error {
	want, notReady, err := r.sshKeyIDs(ctx, gr)
	if err != nil {
		return err
	}
	if notReady != "" {
		r.setSynced(gr, metav1.ConditionFalse, hypv1alpha1.ReasonSSHKeyNotReady,
			notReady+", so the rental's SSH keys are left as they are")
		return nil
	}
	applied := gr.Status.SSHKeyIDs
	if slices.Equal(want, applied) {
		return nil
	}

	attached, err := hc.SSHKeys.ListForRental(ctx, hyperbolic.ListRentalSSHKeysParams{RentalID: rentalID})
	if err != nil {
		r.setSynced(gr, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "list the rental's SSH keys: "+err.Error())
		return nil
	}
	has := make(map[int64]bool, len(attached))
	for _, k := range attached {
		has[int64(k.ID)] = true
	}
	for _, id := range want {
		if has[id] {
			continue
		}
		_, err := hc.Rentals.AttachSSHKey(ctx, hyperbolic.AttachSSHKeyParams{RentalID: rentalID, SSHPublicKeyID: int(id)})
		if err != nil {
			r.setSynced(gr, metav1.ConditionFalse, keyChangeErrorReason(err), fmt.Sprintf("attach SSH key %d: %v", id, err))
			return nil
		}
	}
	for _, id := range applied {
		if slices.Contains(want, id) || !has[id] {
			continue
		}
		_, err := hc.Rentals.DetachSSHKey(ctx, hyperbolic.DetachSSHKeyParams{RentalID: rentalID, SSHPublicKeyID: int(id)})
		if err != nil && !hyperbolic.IsNotFound(err) {
			r.setSynced(gr, metav1.ConditionFalse, keyChangeErrorReason(err), fmt.Sprintf("detach SSH key %d: %v", id, err))
			return nil
		}
	}
	gr.Status.SSHKeyIDs = want
	r.Recorder.Eventf(gr, nil, corev1.EventTypeNormal, eventSSHKeysUpdated, actionUpdateSSHKeys,
		"The SSH keys from spec.sshKeyRefs on rental %s are now %v", rentalID, want)
	return nil
}

// keyChangeErrorReason tells a refusal to change a rental's keys, a 4xx
// other than rate limiting, from a failure worth retrying. The live API
// refuses with 400 "SSH keys cannot be changed on this rental" on rentals
// that don't allow it.
func keyChangeErrorReason(err error) string {
	code := hyperbolic.StatusCode(err)
	if code >= 400 && code < 500 && code != http.StatusTooManyRequests && code != http.StatusRequestTimeout {
		return hypv1alpha1.ReasonSSHKeyChangeRejected
	}
	return hypv1alpha1.ReasonAPIError
}

// describeConfig names a config reference for people.
func describeConfig(ref hypv1alpha1.ProviderConfigReference) string {
	if ref.IsCluster() {
		return hypv1alpha1.ClusterProviderConfigKind + " " + ref.Name
	}
	return hypv1alpha1.ProviderConfigKind + " " + ref.Name
}

// rentalsForSSHKey maps an SSHKey to the GPURentals in its namespace that
// list it in spec.sshKeyRefs.
func (r *GPURentalReconciler) rentalsForSSHKey(ctx context.Context, obj client.Object) []reconcile.Request {
	var list hypv1alpha1.GPURentalList
	if err := r.List(ctx, &list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	var reqs []reconcile.Request
	for i := range list.Items {
		gr := &list.Items[i]
		if gr.Status.TerminatedAt != nil {
			continue
		}
		if slices.ContainsFunc(gr.Spec.SSHKeyRefs, func(ref hypv1alpha1.SSHKeyReference) bool { return ref.Name == obj.GetName() }) {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(gr)})
		}
	}
	return reqs
}

// sshKeyUsableChanged passes SSHKey events that change whether GPURentals
// can use the key: created, deleted, being deleted, saved under another id,
// or becoming Ready or not. Routine status updates are dropped.
var sshKeyUsableChanged = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldKey, okOld := e.ObjectOld.(*hypv1alpha1.SSHKey)
		newKey, okNew := e.ObjectNew.(*hypv1alpha1.SSHKey)
		if !okOld || !okNew {
			return false
		}
		return oldKey.DeletionTimestamp.IsZero() != newKey.DeletionTimestamp.IsZero() ||
			oldKey.Status.ID != newKey.Status.ID ||
			apimeta.IsStatusConditionTrue(oldKey.Status.Conditions, hypv1alpha1.ConditionReady) !=
				apimeta.IsStatusConditionTrue(newKey.Status.Conditions, hypv1alpha1.ConditionReady)
	},
}
