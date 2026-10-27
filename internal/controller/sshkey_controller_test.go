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
	"strconv"
	"strings"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	"github.com/HyperbolicLabs/hyperbolic-go/hyperbolictest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/account"
)

var _ = Describe("SSHKey", func() {
	ctx := context.Background()
	var (
		acct *fakeAccount
		r    *SSHKeyReconciler
	)

	BeforeEach(func() {
		acct = newFakeAccount(ctx)
		r = newSSHKeyReconciler()
	})

	get := func(name client.ObjectKey) *hypv1alpha1.SSHKey {
		GinkgoHelper()
		key := &hypv1alpha1.SSHKey{}
		Expect(k8sClient.Get(ctx, name, key)).To(Succeed())
		return key
	}
	// onAccount reports whether the fake holds the test key named seed.
	onAccount := func(seed string) bool {
		for _, k := range acct.api.SSHKeys() {
			if account.SameSSHKey(k.PublicKey, testPublicKey(seed)) {
				return true
			}
		}
		return false
	}

	It("saves the key, and deletes it with the object", func() {
		name, id := acct.saveSSHKey(ctx, r, acct.sshKey("alice", "alice"))
		Expect(acct.api.Requests(http.MethodPost, hyperbolictest.PathSSHKeys)).To(Equal(1))
		key := get(name)
		Expect(key.Annotations).To(HaveKeyWithValue(hypv1alpha1.AnnotationSSHKeyID, strconv.FormatInt(id, 10)))
		Expect(key.Annotations).NotTo(HaveKey(hypv1alpha1.AnnotationCreatePending))
		ready := conditionOf(key.Status.Conditions, hypv1alpha1.ConditionReady)
		Expect(ready.Status).To(Equal(metav1.ConditionTrue))
		Expect(ready.Reason).To(Equal(hypv1alpha1.ReasonSaved))
		saved := acct.api.SSHKeys()[len(acct.api.SSHKeys())-1]
		Expect(saved.Name.MustGet()).To(Equal(acct.namespace + "/alice"))

		By("checking the key again without saving it twice")
		reconcileOnce(ctx, r, name)
		Expect(acct.api.Requests(http.MethodPost, hyperbolictest.PathSSHKeys)).To(Equal(1))

		Expect(k8sClient.Delete(ctx, key)).To(Succeed())
		reconcileOnce(ctx, r, name)
		Expect(onAccount("alice")).To(BeFalse())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, name, key))).To(BeTrue())
	})

	It("adopts a key the account already has, and never deletes it", func() {
		existing := acct.api.AddSSHKey(testPublicKey("alice")+"-on-another-laptop", "alice")
		name, id := acct.saveSSHKey(ctx, r, acct.sshKey("alice", "alice"))
		Expect(id).To(Equal(int64(existing.ID)))
		Expect(acct.api.Requests(http.MethodPost, hyperbolictest.PathSSHKeys)).To(BeZero())
		key := get(name)
		Expect(key.Annotations).NotTo(HaveKey(hypv1alpha1.AnnotationSSHKeyID))
		Expect(conditionOf(key.Status.Conditions, hypv1alpha1.ConditionReady).Reason).To(Equal(hypv1alpha1.ReasonAdopted))

		Expect(k8sClient.Delete(ctx, key)).To(Succeed())
		reconcileOnce(ctx, r, name)
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, name, key))).To(BeTrue())
		Expect(onAccount("alice")).To(BeTrue())
	})

	It("leaves the key saved with deletionPolicy Orphan", func() {
		key := acct.sshKey("alice", "alice")
		key.Spec.DeletionPolicy = hypv1alpha1.DeletionPolicyOrphan
		name, _ := acct.saveSSHKey(ctx, r, key)

		Expect(k8sClient.Delete(ctx, key)).To(Succeed())
		reconcileOnce(ctx, r, name)
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, name, key))).To(BeTrue())
		Expect(onAccount("alice")).To(BeTrue())
	})

	It("counts a key saved by a request whose outcome was unknown as its own", func() {
		acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodPost, Path: hyperbolictest.PathSSHKeys, Status: http.StatusBadGateway, AfterCommit: true})
		key := acct.sshKey("alice", "alice")
		Expect(k8sClient.Create(ctx, key)).To(Succeed())
		name := client.ObjectKeyFromObject(key)
		reconcileOnce(ctx, r, name)
		reconcileOnce(ctx, r, name)
		Expect(get(name).Annotations).To(HaveKey(hypv1alpha1.AnnotationCreatePending))
		Expect(conditionOf(get(name).Status.Conditions, hypv1alpha1.ConditionSynced).Status).To(Equal(metav1.ConditionFalse))

		reconcileOnce(ctx, r, name)
		key = get(name)
		Expect(acct.api.Requests(http.MethodPost, hyperbolictest.PathSSHKeys)).To(Equal(1))
		Expect(key.Annotations).To(HaveKey(hypv1alpha1.AnnotationSSHKeyID))
		Expect(key.Annotations).NotTo(HaveKey(hypv1alpha1.AnnotationCreatePending))
		Expect(conditionOf(key.Status.Conditions, hypv1alpha1.ConditionReady).Reason).To(Equal(hypv1alpha1.ReasonSaved))

		Expect(k8sClient.Delete(ctx, key)).To(Succeed())
		reconcileOnce(ctx, r, name)
		Expect(onAccount("alice")).To(BeFalse())
	})

	It("saves the key again after it is deleted outside the operator", func() {
		name, id := acct.saveSSHKey(ctx, r, acct.sshKey("alice", "alice"))
		hc := acct.api.Client(GinkgoTB())
		_, err := hc.SSHKeys.Delete(ctx, hyperbolic.DeleteSSHKeyParams{ID: int(id)})
		Expect(err).NotTo(HaveOccurred())

		reconcileOnce(ctx, r, name)
		key := get(name)
		Expect(key.Status.ID).NotTo(BeZero())
		Expect(key.Status.ID).NotTo(Equal(id))
		Expect(key.Annotations).To(HaveKeyWithValue(hypv1alpha1.AnnotationSSHKeyID, strconv.FormatInt(key.Status.ID, 10)))
		Expect(onAccount("alice")).To(BeTrue())
	})

	It("waits to delete a key while a live rental uses it", func() {
		name, id := acct.saveSSHKey(ctx, r, acct.sshKey("alice", "alice"))
		rental, err := acct.api.Client(GinkgoTB()).Rentals.Create(ctx, h100x1("uses-alice")) // the newest key, alice's
		Expect(err).NotTo(HaveOccurred())
		Expect(acct.api.RentalSSHKeyIDs(rental.RentalID)).To(ConsistOf(int(id)))

		key := get(name)
		Expect(k8sClient.Delete(ctx, key)).To(Succeed())
		res := reconcileOnce(ctx, r, name)
		Expect(res.RequeueAfter).To(Equal(sshKeyInUseRetry))
		key = get(name)
		ready := conditionOf(key.Status.Conditions, hypv1alpha1.ConditionReady)
		Expect(ready.Reason).To(Equal(hypv1alpha1.ReasonKeyInUse))
		Expect(ready.Message).To(ContainSubstring("sshKeyRefs"))
		Expect(onAccount("alice")).To(BeTrue())

		acct.api.TerminateExternally(rental.RentalID)
		reconcileOnce(ctx, r, name)
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, name, key))).To(BeTrue())
		Expect(onAccount("alice")).To(BeFalse())
	})

	It("reports the live API's 409 for a key in use as KeyInUse", func() {
		name, _ := acct.saveSSHKey(ctx, r, acct.sshKey("alice", "alice"))
		acct.api.InjectFault(hyperbolictest.Fault{
			Method: http.MethodPost, Path: hyperbolictest.PathSSHKeyDelete,
			Status: http.StatusConflict, Code: "CONFLICT", Message: "SSH public key is still attached to a live instance",
		})
		Expect(k8sClient.Delete(ctx, get(name))).To(Succeed())
		res := reconcileOnce(ctx, r, name)
		Expect(res.RequeueAfter).To(Equal(sshKeyInUseRetry))
		Expect(conditionOf(get(name).Status.Conditions, hypv1alpha1.ConditionReady).Reason).To(Equal(hypv1alpha1.ReasonKeyInUse))

		By("deleting it once the API allows")
		reconcileOnce(ctx, r, name)
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, name, &hypv1alpha1.SSHKey{}))).To(BeTrue())
		Expect(onAccount("alice")).To(BeFalse())
	})

	It("reports a key the API refuses to save", func() {
		acct.api.InjectFault(hyperbolictest.Fault{Method: http.MethodPost, Path: hyperbolictest.PathSSHKeys, Status: http.StatusBadRequest, Code: "BAD_REQUEST", Message: "unsupported key type"})
		key := acct.sshKey("alice", "alice")
		Expect(k8sClient.Create(ctx, key)).To(Succeed())
		name := client.ObjectKeyFromObject(key)
		reconcileOnce(ctx, r, name)
		res := reconcileOnce(ctx, r, name)
		Expect(res.RequeueAfter).To(Equal(blockedRetry))
		key = get(name)
		ready := conditionOf(key.Status.Conditions, hypv1alpha1.ConditionReady)
		Expect(ready.Reason).To(Equal(hypv1alpha1.ReasonKeyRejected))
		Expect(ready.Message).To(ContainSubstring("unsupported key type"))
		Expect(key.Annotations).NotTo(HaveKey(hypv1alpha1.AnnotationCreatePending))
	})

	It("waits for credentials to delete a key it saved", func() {
		name, _ := acct.saveSSHKey(ctx, r, acct.sshKey("alice", "alice"))
		forgetful := newSSHKeyReconciler() // has never seen the API key
		Expect(k8sClient.Delete(ctx, &hypv1alpha1.ProviderConfig{
			ObjectMeta: metav1.ObjectMeta{Namespace: acct.namespace, Name: acct.config.Name},
		})).To(Succeed())

		Expect(k8sClient.Delete(ctx, get(name))).To(Succeed())
		reconcileOnce(ctx, forgetful, name)
		key := get(name)
		Expect(conditionOf(key.Status.Conditions, hypv1alpha1.ConditionSynced).Reason).To(Equal(hypv1alpha1.ReasonCredentialError))
		Expect(onAccount("alice")).To(BeTrue())

		By("deleting it with the remembered key")
		reconcileOnce(ctx, r, name)
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, name, key))).To(BeTrue())
		Expect(onAccount("alice")).To(BeFalse())
	})

	DescribeTable("rejects an invalid publicKey",
		func(publicKey string) {
			key := acct.sshKey("bad", "bad")
			key.Spec.PublicKey = publicKey
			err := k8sClient.Create(ctx, key)
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "err = %v", err)
		},
		Entry("a private key", "-----BEGIN OPENSSH PRIVATE KEY-----"),
		Entry("a fingerprint", "SHA256:nThbg6kXUpJWGl7E1IGOCspRomTxdCARLviKw6E5SY8"),
		Entry("two keys", testPublicKey("a")+"\n"+testPublicKey("b")),
		Entry("options before the key", `from="10.0.0.1" `+testPublicKey("a")),
	)

	It("accepts a key with a trailing newline, as a block scalar has", func() {
		key := acct.sshKey("newline", "newline")
		key.Spec.PublicKey += "\n"
		Expect(k8sClient.Create(ctx, key)).To(Succeed())
	})

	It("does not let the key or its config change", func() {
		key := acct.sshKey("fixed", "fixed")
		Expect(k8sClient.Create(ctx, key)).To(Succeed())

		changed := key.DeepCopy()
		changed.Spec.PublicKey = testPublicKey("other")
		err := k8sClient.Update(ctx, changed)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("publicKey is immutable"))

		changed = key.DeepCopy()
		changed.Spec.ProviderConfigRef.Name = "other"
		err = k8sClient.Update(ctx, changed)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("providerConfigRef is immutable"))
	})
})

var _ = DescribeTable("sshKeyName",
	func(namespace, name, want string) {
		key := &hypv1alpha1.SSHKey{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
		Expect(sshKeyName(key)).To(Equal(want))
	},
	Entry("is the namespace and name", "team-a", "alice", "team-a/alice"),
	Entry("is cut to the API's limit", "team-a", strings.Repeat("a", 300), "team-a/"+strings.Repeat("a", maxSSHKeyNameLength-len("team-a/"))),
)
