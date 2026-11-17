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
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"time"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"
	"github.com/HyperbolicLabs/hyperbolic-go/hyperbolictest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/account"
)

// testClientFactory builds API clients the way the manager does, recording
// their requests in testMetrics.
var testClientFactory = account.NewClientFactory("rental-operator/test", testMetrics.Transport(nil))

// fakeAccount is a namespace wired to an in-memory Hyperbolic API: a Secret
// holding the fake's API key and a ProviderConfig pointing at the fake.
type fakeAccount struct {
	api       *hyperbolictest.Server
	namespace string
	config    hypv1alpha1.ProviderConfigReference
}

// newFakeAccount starts a fake API (closed when the spec ends) and creates a
// fresh namespace with a Secret and a ProviderConfig named "default" for it.
func newFakeAccount(ctx context.Context) *fakeAccount {
	api := hyperbolictest.NewServer(GinkgoTB())

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "fake-account-"}}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "hyperbolic-key", Namespace: ns.Name,
			Labels: map[string]string{hypv1alpha1.LabelCredentials: "true"},
		},
		StringData: map[string]string{"api-key": api.APIKey},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())

	pc := &hypv1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: ns.Name},
		Spec: hypv1alpha1.ProviderConfigSpec{
			Credentials: hypv1alpha1.Credentials{SecretRef: hypv1alpha1.SecretKeySelector{Name: secret.Name}},
			Endpoint:    api.URL,
		},
	}
	Expect(k8sClient.Create(ctx, pc)).To(Succeed())

	return &fakeAccount{api: api, namespace: ns.Name, config: hypv1alpha1.ProviderConfigReference{Name: pc.Name}}
}

// rental returns an unsaved GPURental for one H100 (the cheapest offering in
// the fake's default catalog) in the account's namespace, using the
// account's ProviderConfig. Change its spec for other shapes.
func (a *fakeAccount) rental(name string) *hypv1alpha1.GPURental {
	return &hypv1alpha1.GPURental{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: a.namespace},
		Spec: hypv1alpha1.GPURentalSpec{
			ProviderConfigRef: a.config,
			GPUType:           "h100",
			GPUCount:          1,
		},
	}
}

// newRentalReconciler returns a GPURentalReconciler wired to envtest and to
// whichever fake API a ProviderConfig points at.
func newRentalReconciler() *GPURentalReconciler {
	return &GPURentalReconciler{
		Client:        k8sClient,
		Scheme:        k8sClient.Scheme(),
		NewClient:     testClientFactory,
		RentalOptions: account.NewRentalOptionsCache(time.Minute),
		Rentals:       account.NewRentalCache(alwaysLater{}),
		Credentials:   account.NewCredentialsMemory(),
		Clock:         clock.RealClock{},
		Recorder:      &events.FakeRecorder{},
		Metrics:       testMetrics,
		APIReader:     k8sClient,
	}
}

// newConfigReconciler returns a ProviderConfigReconciler wired to envtest
// and to whichever fake API a ProviderConfig points at. Its APIReader is
// unset, as when the operator cannot read around its cache.
func newConfigReconciler() *ProviderConfigReconciler {
	return &ProviderConfigReconciler{
		Client:    k8sClient,
		Scheme:    k8sClient.Scheme(),
		NewClient: testClientFactory,
	}
}

// newAvailabilityReconciler returns a GPUAvailabilityReconciler wired to
// envtest, with a rental-options cache that lasts ttl.
func newAvailabilityReconciler(ttl time.Duration) *GPUAvailabilityReconciler {
	return &GPUAvailabilityReconciler{
		Client:        k8sClient,
		Scheme:        k8sClient.Scheme(),
		NewClient:     testClientFactory,
		RentalOptions: account.NewRentalOptionsCache(ttl),
	}
}

// newSSHKeyReconciler returns an SSHKeyReconciler wired to envtest and to
// whichever fake API a ProviderConfig points at.
func newSSHKeyReconciler() *SSHKeyReconciler {
	return &SSHKeyReconciler{
		Client:      k8sClient,
		Scheme:      k8sClient.Scheme(),
		NewClient:   testClientFactory,
		Credentials: account.NewCredentialsMemory(),
		Clock:       clock.RealClock{},
		Recorder:    &events.FakeRecorder{},
		APIReader:   k8sClient,
	}
}

// sshKey returns an unsaved SSHKey for a test key named seed (see
// testPublicKey) in the account's namespace, using the account's
// ProviderConfig.
func (a *fakeAccount) sshKey(name, seed string) *hypv1alpha1.SSHKey {
	return &hypv1alpha1.SSHKey{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: a.namespace},
		Spec: hypv1alpha1.SSHKeySpec{
			ProviderConfigRef: a.config,
			PublicKey:         testPublicKey(seed),
		},
	}
}

// saveSSHKey creates key and reconciles it until it is Ready (finalizer,
// then save). It returns the object's key and the saved key's id.
func (a *fakeAccount) saveSSHKey(ctx context.Context, r reconcile.Reconciler, key *hypv1alpha1.SSHKey) (types.NamespacedName, int64) {
	GinkgoHelper()
	Expect(k8sClient.Create(ctx, key)).To(Succeed())
	name := types.NamespacedName{Namespace: key.Namespace, Name: key.Name}
	reconcileOnce(ctx, r, name)
	reconcileOnce(ctx, r, name)
	Expect(k8sClient.Get(ctx, name, key)).To(Succeed())
	Expect(key.Status.ID).NotTo(BeZero(), "the key was not saved: %+v", key.Status.Conditions)
	return name, key.Status.ID
}

// testPublicKey returns a well-formed ed25519 public key line, one per
// seed, with seed as its comment.
func testPublicKey(seed string) string {
	body := make([]byte, 32)
	copy(body, seed)
	blob := binary.BigEndian.AppendUint32(nil, 11)
	blob = append(blob, "ssh-ed25519"...)
	blob = binary.BigEndian.AppendUint32(blob, 32)
	blob = append(blob, body...)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + " " + seed
}

// alwaysLater is a clock on which every cached rental list is already
// stale: every poll in these tests comes after the refresh interval, so the
// fake API's latest state is always read. The cache's own specs use a fake
// clock instead.
type alwaysLater struct{}

func (alwaysLater) Now() time.Time                  { return time.Now() }
func (alwaysLater) Since(t time.Time) time.Duration { return time.Since(t) + 24*time.Hour }

// createRental creates gr and reconciles it until the fake API holds its
// rental (finalizer, then create). It returns the object's key and the
// rental's id.
func (a *fakeAccount) createRental(ctx context.Context, r reconcile.Reconciler, gr *hypv1alpha1.GPURental) (types.NamespacedName, string) {
	GinkgoHelper()
	Expect(k8sClient.Create(ctx, gr)).To(Succeed())
	key := types.NamespacedName{Namespace: gr.Namespace, Name: gr.Name}
	reconcileOnce(ctx, r, key)
	reconcileOnce(ctx, r, key)
	Expect(k8sClient.Get(ctx, key, gr)).To(Succeed())
	Expect(gr.Status.RentalID).NotTo(BeEmpty(), "the rental was not created: %+v", gr.Status.Conditions)
	return key, gr.Status.RentalID
}

// reconcileOnce runs one reconcile of key and fails the spec on error.
func reconcileOnce(ctx context.Context, r reconcile.Reconciler, key types.NamespacedName) ctrl.Result {
	GinkgoHelper()
	res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
	Expect(err).NotTo(HaveOccurred())
	return res
}

// rentalOptions turns option literals into the API's rental options, keeping
// each literal's machineType, so bare-metal entries can test the filters.
func rentalOptions(vms []hyperbolic.VirtualMachineOption) []hyperbolic.RentalOption {
	out := make([]hyperbolic.RentalOption, len(vms))
	for i, vm := range vms {
		b, err := json.Marshal(vm)
		if err == nil {
			err = out[i].UnmarshalJSON(b)
		}
		if err != nil {
			panic(err)
		}
	}
	return out
}

// h100x1 asks for one H100 virtual machine in us-central-1, labeled label.
func h100x1(label string) hyperbolic.CreateRentalParams {
	return hyperbolic.CreateRentalParams{
		RentalType: hyperbolic.RentalTypeVirtualMachine,
		GPUType:    hyperbolic.Ptr(hyperbolic.GPUTypeH100),
		GPUCount:   1,
		Region:     hyperbolic.Ptr("us-central-1"),
		Label:      &label,
	}
}
