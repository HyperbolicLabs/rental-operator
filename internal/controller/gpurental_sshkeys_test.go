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
	"net/http"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	"github.com/HyperbolicLabs/hyperbolic-go/hyperbolictest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

var _ = Describe("GPURental sshKeyRefs", func() {
	ctx := context.Background()
	var (
		acct *fakeAccount
		r    *GPURentalReconciler
		kr   *SSHKeyReconciler
	)

	BeforeEach(func() {
		acct = newFakeAccount(ctx)
		r = newRentalReconciler()
		kr = newSSHKeyReconciler()
	})

	refs := func(names ...string) []hypv1alpha1.SSHKeyReference {
		out := make([]hypv1alpha1.SSHKeyReference, 0, len(names))
		for _, n := range names {
			out = append(out, hypv1alpha1.SSHKeyReference{Name: n})
		}
		return out
	}
	get := func(key client.ObjectKey) *hypv1alpha1.GPURental {
		GinkgoHelper()
		gr := &hypv1alpha1.GPURental{}
		Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
		return gr
	}
	setRefs := func(key client.ObjectKey, names ...string) {
		GinkgoHelper()
		gr := get(key)
		gr.Spec.SSHKeyRefs = refs(names...)
		Expect(k8sClient.Update(ctx, gr)).To(Succeed())
	}

	It("waits for its keys, then rents with them", func() {
		gr := acct.rental("train")
		gr.Spec.SSHKeyRefs = refs("alice")
		Expect(k8sClient.Create(ctx, gr)).To(Succeed())
		key := client.ObjectKeyFromObject(gr)
		reconcileOnce(ctx, r, key)
		res := reconcileOnce(ctx, r, key)
		Expect(res.RequeueAfter).To(Equal(blockedRetry))
		gr = get(key)
		Expect(gr.Status.Phase).To(Equal(hypv1alpha1.PhaseBlocked))
		ready := conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionReady)
		Expect(ready.Reason).To(Equal(hypv1alpha1.ReasonSSHKeyNotReady))
		Expect(ready.Message).To(Equal("SSHKey alice does not exist"))
		Expect(acct.api.Requests(http.MethodPost, hyperbolictest.PathRentals)).To(BeZero())

		_, aliceID := acct.saveSSHKey(ctx, kr, acct.sshKey("alice", "alice"))
		Expect(r.rentalsForSSHKey(ctx, acct.sshKey("alice", "alice"))).To(ConsistOf(reconcile.Request{NamespacedName: key}))

		reconcileOnce(ctx, r, key)
		gr = get(key)
		Expect(gr.Status.RentalID).NotTo(BeEmpty())
		Expect(gr.Status.SSHKeyIDs).To(Equal([]int64{aliceID}))
		Expect(acct.api.RentalSSHKeyIDs(gr.Status.RentalID)).To(ConsistOf(int(aliceID)))
	})

	It("does not rent with a key saved on another config", func() {
		other := acct.sshKey("elsewhere", "elsewhere")
		other.Spec.ProviderConfigRef = hypv1alpha1.ProviderConfigReference{Kind: hypv1alpha1.ClusterProviderConfigKind, Name: "shared"}
		Expect(k8sClient.Create(ctx, other)).To(Succeed())
		gr := acct.rental("train")
		gr.Spec.SSHKeyRefs = refs("elsewhere")
		Expect(k8sClient.Create(ctx, gr)).To(Succeed())
		key := client.ObjectKeyFromObject(gr)
		reconcileOnce(ctx, r, key)
		reconcileOnce(ctx, r, key)

		ready := conditionOf(get(key).Status.Conditions, hypv1alpha1.ConditionReady)
		Expect(ready.Reason).To(Equal(hypv1alpha1.ReasonSSHKeyNotReady))
		Expect(ready.Message).To(Equal("SSHKey elsewhere is saved with ClusterProviderConfig shared, and this rental uses ProviderConfig default"))
		Expect(acct.api.Requests(http.MethodPost, hyperbolictest.PathRentals)).To(BeZero())
	})

	It("attaches and detaches keys while the rental runs, and leaves other keys alone", func() {
		key, id := acct.createRental(ctx, r, acct.rental("train"))
		defaultKey := acct.api.RentalSSHKeyIDs(id)
		Expect(defaultKey).To(HaveLen(1), "the API authorizes the newest key by default")
		Expect(acct.api.SetRentalStatus(id, hyperbolic.RentalStatusRunning)).To(BeTrue())
		_, alice := acct.saveSSHKey(ctx, kr, acct.sshKey("alice", "alice"))
		_, bob := acct.saveSSHKey(ctx, kr, acct.sshKey("bob", "bob"))

		By("attaching the keys that were added")
		setRefs(key, "alice", "bob")
		reconcileOnce(ctx, r, key)
		Expect(acct.api.RentalSSHKeyIDs(id)).To(ConsistOf(defaultKey[0], int(alice), int(bob)))
		Expect(get(key).Status.SSHKeyIDs).To(ConsistOf(alice, bob))

		By("calling nothing while the keys are as asked")
		attaches := acct.api.Requests(http.MethodPost, hyperbolictest.PathAttachSSHKey)
		reconcileOnce(ctx, r, key)
		Expect(acct.api.Requests(http.MethodPost, hyperbolictest.PathAttachSSHKey)).To(Equal(attaches))

		By("detaching the keys that were removed")
		setRefs(key, "alice")
		reconcileOnce(ctx, r, key)
		Expect(acct.api.RentalSSHKeyIDs(id)).To(ConsistOf(defaultKey[0], int(alice)))

		setRefs(key)
		reconcileOnce(ctx, r, key)
		Expect(acct.api.RentalSSHKeyIDs(id)).To(ConsistOf(defaultKey[0]))
		Expect(get(key).Status.SSHKeyIDs).To(BeEmpty())
	})

	It("reports a rental that refuses key changes, and keeps its keys", func() {
		key, id := acct.createRental(ctx, r, acct.rental("train"))
		Expect(acct.api.SetRentalStatus(id, hyperbolic.RentalStatusRunning)).To(BeTrue())
		before := acct.api.RentalSSHKeyIDs(id)
		acct.saveSSHKey(ctx, kr, acct.sshKey("alice", "alice"))
		// What the live API answered on a rental that doesn't allow it.
		acct.api.InjectFault(hyperbolictest.Fault{
			Method: http.MethodPost, Path: hyperbolictest.PathAttachSSHKey,
			Status: http.StatusBadRequest, Code: "BAD_REQUEST", Message: "SSH keys cannot be changed on this rental",
		})

		setRefs(key, "alice")
		reconcileOnce(ctx, r, key)
		gr := get(key)
		synced := conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionSynced)
		Expect(synced.Reason).To(Equal(hypv1alpha1.ReasonSSHKeyChangeRejected))
		Expect(synced.Message).To(ContainSubstring("cannot be changed on this rental"))
		Expect(gr.Status.Phase).To(Equal(hypv1alpha1.PhaseRunning))
		Expect(gr.Status.SSHKeyIDs).To(BeEmpty())
		Expect(acct.api.RentalSSHKeyIDs(id)).To(Equal(before))
	})

	It("leaves a running rental's keys alone while a key is not ready", func() {
		_, alice := acct.saveSSHKey(ctx, kr, acct.sshKey("alice", "alice"))
		gr := acct.rental("train")
		gr.Spec.SSHKeyRefs = refs("alice")
		key, id := acct.createRental(ctx, r, gr)
		Expect(acct.api.SetRentalStatus(id, hyperbolic.RentalStatusRunning)).To(BeTrue())

		setRefs(key, "alice", "missing")
		reconcileOnce(ctx, r, key)
		gr = get(key)
		Expect(gr.Status.Phase).To(Equal(hypv1alpha1.PhaseRunning))
		synced := conditionOf(gr.Status.Conditions, hypv1alpha1.ConditionSynced)
		Expect(synced.Status).To(Equal(metav1.ConditionFalse))
		Expect(synced.Reason).To(Equal(hypv1alpha1.ReasonSSHKeyNotReady))
		Expect(acct.api.RentalSSHKeyIDs(id)).To(ConsistOf(int(alice)))
	})
})

var _ = DescribeTable("SSHKey updates that requeue the GPURentals using it",
	func(change func(*hypv1alpha1.SSHKey), want bool) {
		before := &hypv1alpha1.SSHKey{Status: hypv1alpha1.SSHKeyStatus{ID: 7, Conditions: []metav1.Condition{{
			Type: hypv1alpha1.ConditionReady, Status: metav1.ConditionTrue, Reason: hypv1alpha1.ReasonSaved,
		}}}}
		after := before.DeepCopy()
		change(after)
		Expect(sshKeyUsableChanged.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: after})).To(Equal(want))
	},
	Entry("a routine status update", func(k *hypv1alpha1.SSHKey) { k.Status.ObservedGeneration = 2 }, false),
	Entry("deletion starting", func(k *hypv1alpha1.SSHKey) { now := metav1.Now(); k.DeletionTimestamp = &now }, true),
	Entry("saved again under another id", func(k *hypv1alpha1.SSHKey) { k.Status.ID = 8 }, true),
	Entry("no longer Ready", func(k *hypv1alpha1.SSHKey) { k.Status.Conditions[0].Status = metav1.ConditionFalse }, true),
)

var _ = DescribeTable("keyChangeErrorReason",
	func(status int, want string) {
		Expect(keyChangeErrorReason(&hyperbolic.APIError{StatusCode: status})).To(Equal(want))
	},
	Entry("a refusal", http.StatusBadRequest, hypv1alpha1.ReasonSSHKeyChangeRejected),
	Entry("a conflict", http.StatusConflict, hypv1alpha1.ReasonSSHKeyChangeRejected),
	Entry("rate limiting", http.StatusTooManyRequests, hypv1alpha1.ReasonAPIError),
	Entry("a server error", http.StatusServiceUnavailable, hypv1alpha1.ReasonAPIError),
)
