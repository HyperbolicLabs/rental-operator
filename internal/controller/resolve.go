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
	"github.com/HyperbolicLabs/rental-operator/internal/version"
)

// resolvedCreds is an endpoint + API key pair resolved from a ProviderConfig or
// an inline Secret ref.
type resolvedCreds struct {
	endpoint string
	apiKey   string
}

// errSecretNotLabeled reports a credentials Secret without LabelCredentials.
var errSecretNotLabeled = errors.New("secret is not labeled for the operator")

// notLabeled explains how to fix a credentials Secret without the label.
func notLabeled(namespace, name string) error {
	return fmt.Errorf("%w: the operator only reads Secrets labeled %s=true; run: kubectl label secret -n %s %s %s=true",
		errSecretNotLabeled, hypv1alpha1.LabelCredentials, namespace, name, hypv1alpha1.LabelCredentials)
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
			if uncached.Get(ctx, name, &direct) == nil && !isCredentialsSecret(&direct) {
				return "", notLabeled(namespace, ref.Name)
			}
		}
		return "", fmt.Errorf("read secret %s/%s: %w", namespace, ref.Name, err)
	}
	if !isCredentialsSecret(&secret) {
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

// errNamespaceNotAllowed reports a ClusterProviderConfig whose
// allowedNamespaces does not include the namespace using it.
var errNamespaceNotAllowed = errors.New("namespace not allowed")

// credentialSource resolves the Hyperbolic credentials resources refer to.
type credentialSource struct {
	client client.Client
	// uncached reads around the cache, to explain a Secret the cache does not
	// hold. Optional.
	uncached client.Reader
	// clusterNamespace is where ClusterProviderConfig Secrets live: the
	// operator's own namespace.
	clusterNamespace string
}

// fromSpec resolves a config's credentials. Its Secret is in secretNamespace.
func (s credentialSource) fromSpec(ctx context.Context, spec *hypv1alpha1.ProviderConfigSpec, secretNamespace string) (resolvedCreds, error) {
	if src := spec.Credentials.Source; src != "" && src != hypv1alpha1.CredentialsSourceSecret {
		return resolvedCreds{}, fmt.Errorf("credentials source %q is not supported", src)
	}
	if secretNamespace == "" {
		return resolvedCreds{}, errors.New("the operator does not know its own namespace, where ClusterProviderConfig Secrets live; set --cluster-resource-namespace or the POD_NAMESPACE environment variable")
	}
	apiKey, err := apiKeyFromSecret(ctx, s.client, s.uncached, secretNamespace, spec.Credentials.SecretRef)
	if err != nil {
		return resolvedCreds{}, err
	}
	endpoint := spec.Endpoint
	if endpoint == "" {
		endpoint = hyperbolic.DefaultBaseURL
	}
	return resolvedCreds{endpoint: endpoint, apiKey: apiKey}, nil
}

// forReference resolves the credentials that a resource in namespace refers
// to: a ProviderConfig in the same namespace, or a ClusterProviderConfig that
// allows the namespace.
func (s credentialSource) forReference(ctx context.Context, namespace string, ref hypv1alpha1.ProviderConfigReference) (resolvedCreds, error) {
	if ref.Name == "" {
		return resolvedCreds{}, fmt.Errorf("providerConfigRef.name is required")
	}
	if ref.IsCluster() {
		var cpc hypv1alpha1.ClusterProviderConfig
		if err := s.client.Get(ctx, types.NamespacedName{Name: ref.Name}, &cpc); err != nil {
			return resolvedCreds{}, fmt.Errorf("read clusterProviderConfig %s: %w", ref.Name, err)
		}
		if err := s.namespaceAllowed(ctx, &cpc, namespace); err != nil {
			return resolvedCreds{}, err
		}
		return s.fromSpec(ctx, &cpc.Spec.ProviderConfigSpec, s.clusterNamespace)
	}
	var pc hypv1alpha1.ProviderConfig
	if err := s.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, &pc); err != nil {
		return resolvedCreds{}, fmt.Errorf("read providerConfig %s/%s: %w", namespace, ref.Name, err)
	}
	return s.fromSpec(ctx, &pc.Spec, namespace)
}

// namespaceAllowed checks a ClusterProviderConfig's allowedNamespaces.
func (s credentialSource) namespaceAllowed(ctx context.Context, cpc *hypv1alpha1.ClusterProviderConfig, namespace string) error {
	if cpc.Spec.AllowedNamespaces == nil {
		return nil
	}
	selector, err := metav1.LabelSelectorAsSelector(cpc.Spec.AllowedNamespaces)
	if err != nil {
		return fmt.Errorf("clusterProviderConfig %s has an invalid allowedNamespaces: %w", cpc.Name, err)
	}
	var ns corev1.Namespace
	if err := s.client.Get(ctx, types.NamespacedName{Name: namespace}, &ns); err != nil {
		return fmt.Errorf("read namespace %s: %w", namespace, err)
	}
	if !selector.Matches(labels.Set(ns.Labels)) {
		return fmt.Errorf("%w: clusterProviderConfig %s does not allow namespace %s (allowedNamespaces: %s)",
			errNamespaceNotAllowed, cpc.Name, namespace, selector)
	}
	return nil
}

// ClientFactory builds a Hyperbolic API client for an endpoint and API key.
type ClientFactory func(endpoint, apiKey string) (*hyperbolic.Client, error)

// NewClientFactory returns the ClientFactory the manager uses. Its clients
// identify the operator in the User-Agent header and never retry on their
// own: reconcilers requeue on errors, so a reconcile never blocks on backoff.
func NewClientFactory(userAgent string) ClientFactory {
	return func(endpoint, apiKey string) (*hyperbolic.Client, error) {
		return hyperbolic.NewClient(apiKey,
			hyperbolic.WithBaseURL(endpoint),
			hyperbolic.WithUserAgent(userAgent),
			hyperbolic.WithRetryPolicy(hyperbolic.NoRetries()),
		)
	}
}

// newClient builds a Hyperbolic client from resolved creds with f, or with
// the default factory when f is nil.
func (rc resolvedCreds) newClient(f ClientFactory) (*hyperbolic.Client, error) {
	if f == nil {
		f = NewClientFactory("rental-operator/" + version.Version)
	}
	return f(rc.endpoint, rc.apiKey)
}

// key is the CatalogCache key for these creds.
func (rc resolvedCreds) key() string {
	return credKey(rc.endpoint, rc.apiKey)
}
