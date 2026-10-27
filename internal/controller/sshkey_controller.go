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

// An SSHKey's key is saved once per account: the API refuses a second copy
// of a key, whatever its comment. So a save is never repeated by accident,
// and what matters is whose key it is. The operator deletes only keys it
// saved:
//
//  1. Before saving, the operator looks the key up on the account. A key the
//     account already has is adopted: GPURentals can use it, and it is never
//     deleted.
//  2. Before sending a save, the operator records create-pending on the
//     object. After a successful save, it records the key's id and clears
//     create-pending, before status is written.
//  3. A key found while create-pending is present was saved by this object,
//     whatever the outcome of its request looked like.

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/account"
)

const (
	// sshKeyRecheck is how often the operator checks that a saved key is
	// still on the account.
	sshKeyRecheck = 10 * time.Minute
	// sshKeyInUseRetry is how often deleting a key that a live rental uses
	// is retried.
	sshKeyInUseRetry = time.Minute
	// maxSSHKeyNameLength is the longest key name the API accepts, as the
	// spec documents.
	maxSSHKeyNameLength = 255
)

// Event reasons emitted on SSHKeys.
const (
	eventKeySaved             = "Saved"
	eventKeyDeleted           = "Deleted"
	eventKeyDeletedExternally = "DeletedExternally"
	eventKeyDeleteBlocked     = "DeletionBlocked"
	eventKeyOrphaned          = "Orphaned"
)

// SSHKeyReconciler saves an SSHKey's public key on its account, and deletes
// the key again when the SSHKey is deleted, if the operator saved it.
//
// Every field is required unless its comment says otherwise.
// SetupWithManager rejects a reconciler that is missing one.
type SSHKeyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// NewClient builds Hyperbolic API clients.
	NewClient account.ClientFactory
	// Credentials remembers the last API key per config, so a saved key can
	// still be deleted after its Secret is deleted.
	Credentials *account.CredentialsMemory
	// Clock tells the time.
	Clock clock.PassiveClock
	// Recorder emits events on SSHKeys.
	Recorder record.EventRecorder
	// APIReader reads around the cache, to explain a Secret the cache does
	// not hold because it is not labeled. Optional.
	APIReader client.Reader
	// ClusterResourceNamespace is the operator's own namespace, where
	// ClusterProviderConfig Secrets live. Optional: without it,
	// ClusterProviderConfigs report that they cannot read their Secret.
	ClusterResourceNamespace string
}

// validate checks the reconciler's required dependencies.
func (r *SSHKeyReconciler) validate() error {
	return checkDependencies("SSHKey", append(apiDependencies(r.Client, r.Scheme, r.NewClient),
		createDeleteDependencies(r.Credentials, r.Clock, r.Recorder)...)...)
}

// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=sshkeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=sshkeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=sshkeys/finalizers,verbs=update
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=providerconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=compute.hyperbolic.ai,resources=clusterproviderconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile saves the key, or adopts the account's copy, and reports it on
// status.
func (r *SSHKeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var key hypv1alpha1.SSHKey
	if err := r.Get(ctx, req.NamespacedName, &key); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	creds, credErr := account.Resolver{Client: r.Client, Uncached: r.APIReader, ClusterNamespace: r.ClusterResourceNamespace}.
		ForReference(ctx, key.Namespace, key.Spec.ProviderConfigRef)
	var hc *hyperbolic.Client
	if credErr == nil {
		hc, credErr = creds.Client(r.NewClient)
	}
	if credErr == nil {
		r.Credentials.Remember(configKey(key.Namespace, key.Spec.ProviderConfigRef), creds)
	}

	if !key.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &key, hc, credErr)
	}

	if !controllerutil.ContainsFinalizer(&key, hypv1alpha1.FinalizerSSHKey) {
		controllerutil.AddFinalizer(&key, hypv1alpha1.FinalizerSSHKey)
		if err := r.Update(ctx, &key); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if credErr != nil {
		reason := credentialErrorReason(credErr)
		r.setSynced(&key, metav1.ConditionFalse, reason, credErr.Error())
		if key.Status.ID == 0 {
			r.setReady(&key, metav1.ConditionFalse, reason, credErr.Error())
		}
		return r.finish(ctx, &key, apiRetry)
	}

	saved, err := account.SavedSSHKey(ctx, hc, key.Spec.PublicKey)
	if err != nil {
		r.setSynced(&key, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "list SSH keys: "+err.Error())
		return r.finish(ctx, &key, apiRetry)
	}
	r.setSynced(&key, metav1.ConditionTrue, hypv1alpha1.ReasonReconcileSuccess, "Last reconcile reached the Hyperbolic API")
	if saved != nil {
		return r.observe(ctx, &key, saved.ID)
	}
	return r.save(ctx, &key, hc)
}

// observe records the account's copy of the key, saved under id. It is
// this object's to delete if this object saved it: the id is recorded on
// the object, or a save was sent whose outcome was not recorded.
func (r *SSHKeyReconciler) observe(ctx context.Context, key *hypv1alpha1.SSHKey, id int) (ctrl.Result, error) {
	_, pending := createPendingSince(key)
	owned := pending || key.Annotations[hypv1alpha1.AnnotationSSHKeyID] == strconv.Itoa(id)
	if owned {
		if err := r.recordKeyID(ctx, key, id); err != nil {
			return ctrl.Result{}, err
		}
	} else if err := patchAnnotations(ctx, r.Client, key, nil, hypv1alpha1.AnnotationSSHKeyID); err != nil {
		return ctrl.Result{}, err
	}
	key.Status.ID = int64(id)
	if owned {
		r.setReady(key, metav1.ConditionTrue, hypv1alpha1.ReasonSaved, fmt.Sprintf(
			"Saved on the account as SSH key %d. Deleting this SSHKey deletes it, unless deletionPolicy is Orphan.", id))
	} else {
		r.setReady(key, metav1.ConditionTrue, hypv1alpha1.ReasonAdopted, fmt.Sprintf(
			"The account already had this key, as SSH key %d. The operator uses it and never deletes it.", id))
	}
	return r.finish(ctx, key, sshKeyRecheck)
}

// save saves the key on the account.
func (r *SSHKeyReconciler) save(ctx context.Context, key *hypv1alpha1.SSHKey, hc *hyperbolic.Client) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if id, ok := key.Annotations[hypv1alpha1.AnnotationSSHKeyID]; ok {
		log.Info("the saved key is no longer on the account; saving it again", "id", id)
		r.Recorder.Event(key, corev1.EventTypeWarning, eventKeyDeletedExternally,
			fmt.Sprintf("SSH key %s was deleted outside the operator, so it is saved again", id))
		if err := patchAnnotations(ctx, r.Client, key, nil, hypv1alpha1.AnnotationSSHKeyID); err != nil {
			return ctrl.Result{}, err
		}
	}
	key.Status.ID = 0

	// Write ahead: if this annotation cannot be recorded, send nothing. One
	// left by an earlier save means that save may have landed since the
	// account was listed.
	_, savedBefore := createPendingSince(key)
	pending := map[string]string{hypv1alpha1.AnnotationCreatePending: r.Clock.Now().UTC().Format(time.RFC3339)}
	if err := patchAnnotations(ctx, r.Client, key, pending); err != nil {
		return ctrl.Result{}, fmt.Errorf("record create-pending: %w", err)
	}

	saved, err := hc.SSHKeys.Create(ctx, hyperbolic.CreateSSHKeyParams{
		PublicKey: strings.TrimSpace(key.Spec.PublicKey),
		Name:      hyperbolic.Ptr(sshKeyName(key)),
	})
	switch {
	case hyperbolic.IsOutcomeUnknown(err):
		// Keep create-pending: if the key was saved, the next reconcile finds
		// it and knows it is this object's.
		log.Info("save outcome unknown; will look the key up", "error", err.Error())
		r.setSynced(key, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "save SSH key: "+err.Error())
		return r.finish(ctx, key, pendingPoll)
	case hyperbolic.HasCode(err, hyperbolic.CodeSSHPublicKeyAlreadyExists):
		// Saved since the account was listed: by an earlier save of this
		// object, which keeps create-pending so the key is recorded as its
		// own, or else by someone else, and then the key is adopted.
		if !savedBefore {
			if err := patchAnnotations(ctx, r.Client, key, nil, hypv1alpha1.AnnotationCreatePending); err != nil {
				return ctrl.Result{}, fmt.Errorf("clear create-pending: %w", err)
			}
		}
		return r.finish(ctx, key, pendingPoll)
	case err != nil:
		// A definite failure: nothing was saved.
		if cerr := patchAnnotations(ctx, r.Client, key, nil, hypv1alpha1.AnnotationCreatePending); cerr != nil {
			return ctrl.Result{}, fmt.Errorf("clear create-pending: %w", cerr)
		}
		if hyperbolic.StatusCode(err) == http.StatusBadRequest {
			r.setReady(key, metav1.ConditionFalse, hypv1alpha1.ReasonKeyRejected, "The API refused to save the key: "+err.Error())
			return r.finish(ctx, key, blockedRetry)
		}
		r.setSynced(key, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "save SSH key: "+err.Error())
		r.setReady(key, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "save SSH key: "+err.Error())
		return r.finish(ctx, key, apiRetry)
	}

	if err := r.recordKeyID(ctx, key, saved.ID); err != nil {
		return ctrl.Result{}, err
	}
	key.Status.ID = int64(saved.ID)
	r.setReady(key, metav1.ConditionTrue, hypv1alpha1.ReasonSaved, fmt.Sprintf(
		"Saved on the account as SSH key %d. Deleting this SSHKey deletes it, unless deletionPolicy is Orphan.", saved.ID))
	r.Recorder.Event(key, corev1.EventTypeNormal, eventKeySaved, fmt.Sprintf("Saved the key on the account as SSH key %d", saved.ID))
	log.Info("saved SSH key", "id", saved.ID)
	return r.finish(ctx, key, sshKeyRecheck)
}

// recordKeyID records that this object saved the key with id, and clears
// create-pending in the same write, before status is updated.
func (r *SSHKeyReconciler) recordKeyID(ctx context.Context, key *hypv1alpha1.SSHKey, id int) error {
	err := patchAnnotations(ctx, r.Client, key,
		map[string]string{hypv1alpha1.AnnotationSSHKeyID: strconv.Itoa(id)},
		hypv1alpha1.AnnotationCreatePending)
	if err != nil {
		return fmt.Errorf("record SSH key id %d: %w", id, err)
	}
	return nil
}

// reconcileDelete deletes the key from the account if this object saved it
// and deletionPolicy is Delete, then removes the finalizer.
func (r *SSHKeyReconciler) reconcileDelete(ctx context.Context, key *hypv1alpha1.SSHKey, hc *hyperbolic.Client, credErr error) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(key, hypv1alpha1.FinalizerSSHKey) {
		return ctrl.Result{}, nil
	}
	id, owned := savedKeyID(key)
	since, pending := createPendingSince(key)
	if !owned && !pending {
		return r.removeFinalizer(ctx, key)
	}
	if key.Spec.DeletionPolicy == hypv1alpha1.DeletionPolicyOrphan {
		if owned {
			r.Recorder.Event(key, corev1.EventTypeNormal, eventKeyOrphaned, fmt.Sprintf(
				"SSH key %d was left on the account (deletionPolicy: Orphan); delete it in the Hyperbolic dashboard when you are done with it", id))
		}
		return r.removeFinalizer(ctx, key)
	}

	if credErr != nil {
		if rc, ok := r.Credentials.Recall(configKey(key.Namespace, key.Spec.ProviderConfigRef)); ok {
			hc, credErr = rc.Client(r.NewClient)
		}
	}
	if credErr != nil {
		msg := fmt.Sprintf("Cannot delete the saved key before deleting this SSHKey: %v. "+
			"Restore the ProviderConfig and its Secret, or set spec.deletionPolicy: Orphan "+
			"and delete the key in the Hyperbolic dashboard.", credErr)
		log.Info("deletion is waiting for credentials", "error", credErr.Error())
		r.Recorder.Event(key, corev1.EventTypeWarning, eventKeyDeleteBlocked, msg)
		r.setSynced(key, metav1.ConditionFalse, hypv1alpha1.ReasonCredentialError, msg)
		return r.finish(ctx, key, apiRetry)
	}

	if !owned {
		// A save was sent and its outcome is unknown: look the key up.
		saved, err := account.SavedSSHKey(ctx, hc, key.Spec.PublicKey)
		if err != nil {
			r.setSynced(key, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "list SSH keys: "+err.Error())
			return r.finish(ctx, key, apiRetry)
		}
		if saved == nil {
			if wait := since.Add(createRequestTimeout + createSettleGrace).Sub(r.Clock.Now()); wait > 0 {
				return r.finish(ctx, key, wait)
			}
			return r.removeFinalizer(ctx, key) // nothing was saved
		}
		id = saved.ID
	}

	err := account.DeleteSSHKey(ctx, hc, id)
	switch {
	case account.IsSSHKeyInUse(err):
		r.setSynced(key, metav1.ConditionTrue, hypv1alpha1.ReasonReconcileSuccess, "Last reconcile reached the Hyperbolic API")
		r.setReady(key, metav1.ConditionFalse, hypv1alpha1.ReasonKeyInUse, fmt.Sprintf(
			"Deletion waits until no live rental uses SSH key %d: remove it from the sshKeyRefs of the GPURentals using it, "+
				"detach it in the Hyperbolic dashboard, wait for those rentals to end, or set spec.deletionPolicy: Orphan.", id))
		return r.finish(ctx, key, sshKeyInUseRetry)
	case err != nil:
		r.setSynced(key, metav1.ConditionFalse, hypv1alpha1.ReasonAPIError, "delete SSH key: "+err.Error())
		return r.finish(ctx, key, apiRetry)
	}
	r.Recorder.Event(key, corev1.EventTypeNormal, eventKeyDeleted, fmt.Sprintf("Deleted SSH key %d from the account", id))
	return r.removeFinalizer(ctx, key)
}

// savedKeyID returns the id of the key this object saved, if it saved one.
func savedKeyID(key *hypv1alpha1.SSHKey) (int, bool) {
	id, err := strconv.Atoi(key.Annotations[hypv1alpha1.AnnotationSSHKeyID])
	return id, err == nil
}

// sshKeyName is the name the key is saved under, which the dashboard shows:
// "<namespace>/<name>", cut to the API's limit.
func sshKeyName(key *hypv1alpha1.SSHKey) string {
	name := key.Namespace + "/" + key.Name
	if len(name) > maxSSHKeyNameLength {
		name = name[:maxSSHKeyNameLength]
	}
	return name
}

func (r *SSHKeyReconciler) removeFinalizer(ctx context.Context, key *hypv1alpha1.SSHKey) (ctrl.Result, error) {
	controllerutil.RemoveFinalizer(key, hypv1alpha1.FinalizerSSHKey)
	if err := r.Update(ctx, key); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *SSHKeyReconciler) setReady(key *hypv1alpha1.SSHKey, status metav1.ConditionStatus, reason, msg string) {
	apimeta.SetStatusCondition(&key.Status.Conditions, metav1.Condition{
		Type: hypv1alpha1.ConditionReady, Status: status, Reason: reason, Message: msg,
		ObservedGeneration: key.Generation,
	})
	key.Status.ObservedGeneration = key.Generation
}

func (r *SSHKeyReconciler) setSynced(key *hypv1alpha1.SSHKey, status metav1.ConditionStatus, reason, msg string) {
	apimeta.SetStatusCondition(&key.Status.Conditions, metav1.Condition{
		Type: hypv1alpha1.ConditionSynced, Status: status, Reason: reason, Message: msg,
		ObservedGeneration: key.Generation,
	})
}

// finish writes status and returns a requeue result.
func (r *SSHKeyReconciler) finish(ctx context.Context, key *hypv1alpha1.SSHKey, requeue time.Duration) (ctrl.Result, error) {
	if err := r.Status().Update(ctx, key); err != nil {
		return ctrl.Result{}, err
	}
	if requeue > 0 {
		return ctrl.Result{RequeueAfter: requeue}, nil
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SSHKeyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := r.validate(); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&hypv1alpha1.SSHKey{}).
		Named("sshkey").
		Complete(r)
}
