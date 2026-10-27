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
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

func secret(namespace, name string, labeled bool, data map[string]string) *corev1.Secret {
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}, StringData: data, Data: map[string][]byte{}}
	for k, v := range data {
		s.Data[k] = []byte(v)
	}
	if labeled {
		s.Labels = map[string]string{hypv1alpha1.LabelCredentials: CredentialsLabelValue}
	}
	return s
}

func newResolver(t *testing.T, objs ...client.Object) Resolver {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := hypv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return Resolver{Client: c, ClusterNamespace: "operator"}
}

func spec(secretName, key string) *hypv1alpha1.ProviderConfigSpec {
	return &hypv1alpha1.ProviderConfigSpec{Credentials: hypv1alpha1.Credentials{SecretRef: hypv1alpha1.SecretKeySelector{Name: secretName, Key: key}}}
}

func TestFromSpec(t *testing.T) {
	ctx := context.Background()
	r := newResolver(t,
		secret("ml", "good", true, map[string]string{"api-key": " sk_live_1 \n", "other": "sk_live_2"}),
		secret("ml", "unlabeled", false, map[string]string{"api-key": "sk_live_3"}),
		secret("ml", "empty", true, map[string]string{"api-key": "  "}),
	)

	c, err := r.FromSpec(ctx, spec("good", ""), "ml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Endpoint != hyperbolic.DefaultBaseURL || c.apiKey != "sk_live_1" {
		t.Errorf("credentials = %#v (key %q), want the default endpoint and the trimmed key", c, c.apiKey)
	}
	if c, _ := r.FromSpec(ctx, spec("good", "other"), "ml"); c.apiKey != "sk_live_2" {
		t.Errorf("secretRef.key was not used")
	}

	for name, tc := range map[string]struct {
		spec      *hypv1alpha1.ProviderConfigSpec
		namespace string
		want      string
	}{
		"missing Secret":        {spec("absent", ""), "ml", "read secret ml/absent"},
		"missing key":           {spec("good", "nope"), "ml", `has no key "nope"`},
		"empty value":           {spec("empty", ""), "ml", "is empty"},
		"unlabeled Secret":      {spec("unlabeled", ""), "ml", "kubectl label secret -n ml unlabeled"},
		"no operator namespace": {spec("good", ""), "", "does not know its own namespace"},
	} {
		if _, err := r.FromSpec(ctx, tc.spec, tc.namespace); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
	if _, err := r.FromSpec(ctx, spec("unlabeled", ""), "ml"); !errors.Is(err, ErrSecretNotLabeled) {
		t.Errorf("unlabeled Secret: err = %v, want ErrSecretNotLabeled", err)
	}
	unsupported := spec("good", "")
	unsupported.Credentials.Source = "Vault"
	if _, err := r.FromSpec(ctx, unsupported, "ml"); err == nil {
		t.Error("an unsupported credentials source was accepted")
	}
}

func TestFromSpecExplainsASecretTheCacheDoesNotHold(t *testing.T) {
	// The cache holds only labeled Secrets, so an unlabeled one looks missing
	// through it. The uncached reader tells which of the two it is.
	cached := newResolver(t)
	direct := newResolver(t, secret("ml", "unlabeled", false, map[string]string{"api-key": "sk_live_3"}))
	cached.Uncached = direct.Client
	if _, err := cached.FromSpec(context.Background(), spec("unlabeled", ""), "ml"); !errors.Is(err, ErrSecretNotLabeled) {
		t.Fatalf("err = %v, want ErrSecretNotLabeled", err)
	}
}

func TestForReference(t *testing.T) {
	ctx := context.Background()
	ns := func(name string, labels map[string]string) *corev1.Namespace {
		return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
	}
	r := newResolver(t,
		secret("ml", "key", true, map[string]string{"api-key": "sk_ns"}),
		secret("operator", "shared-key", true, map[string]string{"api-key": "sk_shared"}),
		&hypv1alpha1.ProviderConfig{ObjectMeta: metav1.ObjectMeta{Namespace: "ml", Name: "default"}, Spec: *spec("key", "")},
		&hypv1alpha1.ClusterProviderConfig{ObjectMeta: metav1.ObjectMeta{Name: "shared"}, Spec: hypv1alpha1.ClusterProviderConfigSpec{
			ProviderConfigSpec: *spec("shared-key", ""),
			AllowedNamespaces:  &metav1.LabelSelector{MatchLabels: map[string]string{"team": "ml"}},
		}},
		ns("ml", map[string]string{"team": "ml"}),
		ns("web", map[string]string{"team": "web"}),
	)

	if c, err := r.ForReference(ctx, "ml", hypv1alpha1.ProviderConfigReference{Name: "default"}); err != nil || c.apiKey != "sk_ns" {
		t.Errorf("namespaced config: key %q, err %v", c.apiKey, err)
	}
	cluster := hypv1alpha1.ProviderConfigReference{Kind: hypv1alpha1.ClusterProviderConfigKind, Name: "shared"}
	if c, err := r.ForReference(ctx, "ml", cluster); err != nil || c.apiKey != "sk_shared" {
		t.Errorf("cluster config from an allowed namespace: key %q, err %v", c.apiKey, err)
	}
	if _, err := r.ForReference(ctx, "web", cluster); !errors.Is(err, ErrNamespaceNotAllowed) {
		t.Errorf("cluster config from another namespace: err = %v, want ErrNamespaceNotAllowed", err)
	}
	if _, err := r.ForReference(ctx, "ml", hypv1alpha1.ProviderConfigReference{}); err == nil {
		t.Error("an empty reference resolved")
	}
	if _, err := r.ForReference(ctx, "web", hypv1alpha1.ProviderConfigReference{Name: "default"}); err == nil {
		t.Error("a ProviderConfig resolved from another namespace")
	}
}

func TestCredentialsMemory(t *testing.T) {
	m := NewCredentialsMemory()
	if _, ok := m.Recall("ml/default"); ok {
		t.Fatal("an empty memory recalled something")
	}
	m.Remember("ml/default", Credentials{Endpoint: "https://api.hyperbolic.ai", apiKey: "sk"})
	if c, ok := m.Recall("ml/default"); !ok || c.apiKey != "sk" {
		t.Fatalf("recalled %#v, %v", c, ok)
	}
}
