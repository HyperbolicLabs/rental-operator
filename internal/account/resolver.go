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
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperbolic "github.com/HyperbolicLabs/hyperbolic-go"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

// CredentialsLabelValue is the value of LabelCredentials on API key Secrets.
const CredentialsLabelValue = "true"

// IsCredentialsSecret reports whether s is labeled to hold an API key.
func IsCredentialsSecret(s *corev1.Secret) bool {
	return s.Labels[hypv1alpha1.LabelCredentials] == CredentialsLabelValue
}

// CredentialsSelector selects the Secrets labeled to hold an API key.
func CredentialsSelector() labels.Selector {
	return labels.SelectorFromSet(labels.Set{hypv1alpha1.LabelCredentials: CredentialsLabelValue})
}

// ErrSecretNotLabeled reports a credentials Secret without LabelCredentials.
var ErrSecretNotLabeled = errors.New("secret is not labeled for the operator")

// notLabeled explains how to fix a credentials Secret without the label.
func notLabeled(namespace, name string) error {
	return fmt.Errorf("%w: the operator only reads Secrets labeled %s=true; run: kubectl label secret -n %s %s %s=true",
		ErrSecretNotLabeled, hypv1alpha1.LabelCredentials, namespace, name, hypv1alpha1.LabelCredentials)
}

// apiKeyFromSecret reads the key value out of the referenced Secret. The
// Secret must carry LabelCredentials. The manager's cache holds only labeled
// Secrets, so an unlabeled one looks missing through c, and uncached (optional)
// is used once to tell the person which of the two it is.
func apiKeyFromSecret(ctx context.Context, c client.Client, uncached client.Reader, namespace string, ref hypv1alpha1.SecretKeySelector) (string, error) {
	key := ref.Key
	if key == "" {
		key = "api-key"
	}
	name := types.NamespacedName{Namespace: namespace, Name: ref.Name}
	var secret corev1.Secret
	if err := c.Get(ctx, name, &secret); err != nil {
		if apierrors.IsNotFound(err) && uncached != nil {
			var direct corev1.Secret
			if uncached.Get(ctx, name, &direct) == nil && !IsCredentialsSecret(&direct) {
				return "", notLabeled(namespace, ref.Name)
			}
		}
		return "", fmt.Errorf("read secret %s/%s: %w", namespace, ref.Name, err)
	}
	if !IsCredentialsSecret(&secret) {
		return "", notLabeled(namespace, ref.Name)
	}
	raw, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("secret %s/%s has no key %q", namespace, ref.Name, key)
	}
	val := strings.TrimSpace(string(raw))
	if val == "" {
		return "", fmt.Errorf("secret %s/%s key %q is empty", namespace, ref.Name, key)
	}
	return val, nil
}

// ErrNamespaceNotAllowed reports a ClusterProviderConfig whose
// allowedNamespaces does not include the namespace using it.
var ErrNamespaceNotAllowed = errors.New("namespace not allowed")

// Resolver resolves the Credentials that resources refer to.
type Resolver struct {
	// Client reads configs, namespaces and Secrets, normally from the
	// manager's cache.
	Client client.Client
	// Uncached reads around the cache, to explain a Secret the cache does not
	// hold because it is not labeled. Optional.
	Uncached client.Reader
	// ClusterNamespace is where ClusterProviderConfig Secrets live: the
	// operator's own namespace.
	ClusterNamespace string
}

// FromSpec resolves a config's credentials. Its Secret is in secretNamespace.
func (s Resolver) FromSpec(ctx context.Context, spec *hypv1alpha1.ProviderConfigSpec, secretNamespace string) (Credentials, error) {
	if src := spec.Credentials.Source; src != "" && src != hypv1alpha1.CredentialsSourceSecret {
		return Credentials{}, fmt.Errorf("credentials source %q is not supported", src)
	}
	if secretNamespace == "" {
		return Credentials{}, errors.New("the operator does not know its own namespace, where ClusterProviderConfig Secrets live; set --cluster-resource-namespace or the POD_NAMESPACE environment variable")
	}
	apiKey, err := apiKeyFromSecret(ctx, s.Client, s.Uncached, secretNamespace, spec.Credentials.SecretRef)
	if err != nil {
		return Credentials{}, err
	}
	endpoint := spec.Endpoint
	if endpoint == "" {
		endpoint = hyperbolic.DefaultBaseURL
	}
	return Credentials{Endpoint: endpoint, apiKey: apiKey}, nil
}

// ForReference resolves the credentials that a resource in namespace refers
// to: a ProviderConfig in the same namespace, or a ClusterProviderConfig that
// allows the namespace.
func (s Resolver) ForReference(ctx context.Context, namespace string, ref hypv1alpha1.ProviderConfigReference) (Credentials, error) {
	if ref.Name == "" {
		return Credentials{}, fmt.Errorf("providerConfigRef.name is required")
	}
	if ref.IsCluster() {
		var cpc hypv1alpha1.ClusterProviderConfig
		if err := s.Client.Get(ctx, types.NamespacedName{Name: ref.Name}, &cpc); err != nil {
			return Credentials{}, fmt.Errorf("read clusterProviderConfig %s: %w", ref.Name, err)
		}
		if err := s.namespaceAllowed(ctx, &cpc, namespace); err != nil {
			return Credentials{}, err
		}
		return s.FromSpec(ctx, &cpc.Spec.ProviderConfigSpec, s.ClusterNamespace)
	}
	var pc hypv1alpha1.ProviderConfig
	if err := s.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, &pc); err != nil {
		return Credentials{}, fmt.Errorf("read providerConfig %s/%s: %w", namespace, ref.Name, err)
	}
	return s.FromSpec(ctx, &pc.Spec, namespace)
}

// namespaceAllowed checks a ClusterProviderConfig's allowedNamespaces.
func (s Resolver) namespaceAllowed(ctx context.Context, cpc *hypv1alpha1.ClusterProviderConfig, namespace string) error {
	if cpc.Spec.AllowedNamespaces == nil {
		return nil
	}
	selector, err := metav1.LabelSelectorAsSelector(cpc.Spec.AllowedNamespaces)
	if err != nil {
		return fmt.Errorf("clusterProviderConfig %s has an invalid allowedNamespaces: %w", cpc.Name, err)
	}
	var ns corev1.Namespace
	if err := s.Client.Get(ctx, types.NamespacedName{Name: namespace}, &ns); err != nil {
		return fmt.Errorf("read namespace %s: %w", namespace, err)
	}
	if !selector.Matches(labels.Set(ns.Labels)) {
		return fmt.Errorf("%w: clusterProviderConfig %s does not allow namespace %s (allowedNamespaces: %s)",
			ErrNamespaceNotAllowed, cpc.Name, namespace, selector)
	}
	return nil
}
