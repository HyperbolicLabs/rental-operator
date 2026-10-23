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
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
	"github.com/HyperbolicLabs/rental-operator/internal/hyperbolic"
)

// resolvedCreds is an endpoint + API key pair resolved from a ProviderConfig or
// an inline Secret ref.
type resolvedCreds struct {
	endpoint string
	apiKey   string
}

// apiKeyFromSecret reads the key value out of the referenced Secret.
func apiKeyFromSecret(ctx context.Context, c client.Client, namespace string, ref hypv1alpha1.SecretKeySelector) (string, error) {
	key := ref.Key
	if key == "" {
		key = "api-key"
	}
	var secret corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, &secret); err != nil {
		return "", fmt.Errorf("read secret %s/%s: %w", namespace, ref.Name, err)
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

// resolveProviderConfigCreds resolves creds directly from a ProviderConfig.
func resolveProviderConfigCreds(ctx context.Context, c client.Client, pc *hypv1alpha1.ProviderConfig) (resolvedCreds, error) {
	if src := pc.Spec.Credentials.Source; src != "" && src != hypv1alpha1.CredentialsSourceSecret {
		return resolvedCreds{}, fmt.Errorf("credentials source %q is not supported", src)
	}
	apiKey, err := apiKeyFromSecret(ctx, c, pc.Namespace, pc.Spec.Credentials.SecretRef)
	if err != nil {
		return resolvedCreds{}, err
	}
	endpoint := pc.Spec.Endpoint
	if endpoint == "" {
		endpoint = hyperbolic.DefaultEndpoint
	}
	return resolvedCreds{endpoint: endpoint, apiKey: apiKey}, nil
}

// resolveCreds resolves creds by reading the referenced ProviderConfig in the
// given namespace.
func resolveCreds(ctx context.Context, c client.Client, namespace string, pcRef hypv1alpha1.LocalRef) (resolvedCreds, error) {
	if pcRef.Name == "" {
		return resolvedCreds{}, fmt.Errorf("providerConfigRef.name is required")
	}
	var pc hypv1alpha1.ProviderConfig
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: pcRef.Name}, &pc); err != nil {
		return resolvedCreds{}, fmt.Errorf("read providerConfig %s/%s: %w", namespace, pcRef.Name, err)
	}
	return resolveProviderConfigCreds(ctx, c, &pc)
}

// resolveRentalCreds resolves creds for a GPURental.
func resolveRentalCreds(ctx context.Context, c client.Client, r *hypv1alpha1.GPURental) (resolvedCreds, error) {
	return resolveCreds(ctx, c, r.Namespace, r.Spec.ProviderConfigRef)
}

// newClient builds a Hyperbolic client from resolved creds.
func (rc resolvedCreds) newClient() *hyperbolic.Client {
	return hyperbolic.NewClient(rc.endpoint, rc.apiKey)
}

// key is the CatalogCache key for these creds.
func (rc resolvedCreds) key() string {
	return credKey(rc.endpoint, rc.apiKey)
}
