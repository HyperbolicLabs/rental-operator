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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hypv1alpha1 "github.com/HyperbolicLabs/rental-operator/api/v1alpha1"
)

var _ = Describe("ProviderConfig admission", func() {
	ctx := context.Background()
	n := 0

	DescribeTable("endpoint must be https, or http on a loopback host",
		func(endpoint string, allowed bool) {
			n++
			pc := &hypv1alpha1.ProviderConfig{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("endpoint-%d", n), Namespace: "default"},
				Spec: hypv1alpha1.ProviderConfigSpec{
					Credentials: hypv1alpha1.Credentials{
						SecretRef: hypv1alpha1.SecretKeySelector{Name: "hyperbolic-key"},
					},
					Endpoint: endpoint,
				},
			}
			err := k8sClient.Create(ctx, pc)
			if allowed {
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Delete(ctx, pc)).To(Succeed())
				return
			}
			Expect(err).To(MatchError(ContainSubstring("endpoint must be an https URL")))
		},
		Entry("the production API", "https://api.hyperbolic.ai", true),
		Entry("a loopback IPv4 test server", "http://127.0.0.1:8080", true),
		Entry("localhost", "http://localhost:8080", true),
		Entry("a loopback IPv6 test server", "http://[::1]:8080", true),
		Entry("plain http to a remote host", "http://api.hyperbolic.ai", false),
		Entry("a host that only starts with localhost", "http://localhost.example.com", false),
		Entry("another scheme", "ftp://api.hyperbolic.ai", false),
		Entry("not a URL", "api.hyperbolic.ai", false),
	)

	It("defaults the credentials source to Secret and the endpoint to production", func() {
		pc := &hypv1alpha1.ProviderConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "defaults", Namespace: "default"},
			Spec: hypv1alpha1.ProviderConfigSpec{
				Credentials: hypv1alpha1.Credentials{
					SecretRef: hypv1alpha1.SecretKeySelector{Name: "hyperbolic-key"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, pc)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, pc)).To(Succeed()) })

		Expect(pc.Spec.Credentials.Source).To(Equal(hypv1alpha1.CredentialsSourceSecret))
		Expect(pc.Spec.Credentials.SecretRef.Key).To(Equal("api-key"))
		Expect(pc.Spec.Endpoint).To(Equal("https://api.hyperbolic.ai"))
	})
})
