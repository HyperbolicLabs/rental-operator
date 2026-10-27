//go:build e2e

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

package e2e

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/HyperbolicLabs/rental-operator/test/utils"
)

// The Helm chart is the documented install, so these specs install it in
// both modes on the cluster under test and check that every controller
// starts and reconciles. Nothing here reaches the Hyperbolic API: no Secret
// holds a key, so every object stops at CredentialError before a request.
var _ = Describe("Helm chart", Ordered, func() {
	const (
		release     = "rental-operator"
		chartNS     = "rental-operator-helm"
		sampleNS    = "default" // the namespace the samples name
		rentalName  = "train-run-42"
		configName  = "default"
		serviceAcct = "system:serviceaccount:" + chartNS + ":" + release
	)
	controllers := []string{"providerconfig", "clusterproviderconfig", "gpurental", "gpuavailability"}
	samples := []string{"config/samples/v1alpha1_providerconfig.yaml", "config/samples/v1alpha1_gpurental.yaml"}

	run := func(name string, args ...string) string {
		GinkgoHelper()
		out, err := utils.Run(exec.Command(name, args...))
		Expect(err).NotTo(HaveOccurred())
		return out
	}

	// managerPod returns the one manager pod that is not being deleted.
	managerPod := func(g Gomega) string {
		out, err := utils.Run(exec.Command("kubectl", "get", "pods", "-n", chartNS,
			"-l", "app.kubernetes.io/instance="+release,
			"-o", "go-template={{ range .items }}{{ if not .metadata.deletionTimestamp }}"+
				"{{ .metadata.name }}{{ \"\\n\" }}{{ end }}{{ end }}"))
		g.Expect(err).NotTo(HaveOccurred())
		pods := utils.GetNonEmptyLines(out)
		g.Expect(pods).To(HaveLen(1))
		return pods[0]
	}

	// expectControllersStarted waits until every controller's cache has
	// synced, which needs list and watch on everything it reads.
	expectControllersStarted := func() {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			logs, err := utils.Run(exec.Command("kubectl", "logs", "-n", chartNS, managerPod(g)))
			g.Expect(err).NotTo(HaveOccurred())
			for _, c := range controllers {
				g.Expect(logs).To(MatchRegexp(`Starting workers\s+\{"controller": %q`, c))
			}
			g.Expect(logs).NotTo(ContainSubstring("forbidden"))
		}, 3*time.Minute, 2*time.Second).Should(Succeed())
	}

	jsonpath := func(g Gomega, kind, name, path string) string {
		out, err := utils.Run(exec.Command("kubectl", "get", kind, name, "-n", sampleNS, "-o", "jsonpath="+path))
		g.Expect(err).NotTo(HaveOccurred())
		return out
	}

	// expectSampleReconciled applies the samples, waits for the operator to
	// block the rental on its missing Secret, and deletes both objects.
	expectSampleReconciled := func() {
		GinkgoHelper()
		for _, s := range samples {
			run("kubectl", "apply", "-f", s)
		}
		Eventually(func(g Gomega) {
			g.Expect(jsonpath(g, "gpurental", rentalName, "{.status.phase}")).To(Equal("Blocked"))
			g.Expect(jsonpath(g, "gpurental", rentalName,
				`{.status.conditions[?(@.type=="Ready")].reason}`)).To(Equal("CredentialError"))
			g.Expect(jsonpath(g, "gpurental", rentalName, "{.metadata.finalizers}")).
				To(ContainSubstring("compute.hyperbolic.ai/terminate"))
			g.Expect(jsonpath(g, "providerconfig", configName,
				`{.status.conditions[?(@.type=="Ready")].status}`)).To(Equal("False"))
		}, 2*time.Minute, time.Second).Should(Succeed())

		By("deleting them: nothing was rented, so the finalizers let go")
		run("kubectl", "delete", "gpurental", rentalName, "-n", sampleNS, "--timeout=60s")
		run("kubectl", "delete", "providerconfig", configName, "-n", sampleNS, "--timeout=60s")
	}

	BeforeAll(func() {
		By("creating the release namespace with the restricted Pod Security level")
		run("kubectl", "create", "ns", chartNS)
		run("kubectl", "label", "--overwrite", "ns", chartNS, "pod-security.kubernetes.io/enforce=restricted")
	})

	AfterAll(func() {
		for _, s := range samples {
			_, _ = utils.Run(exec.Command("kubectl", "delete", "-f", s, "--ignore-not-found", "--timeout=60s"))
		}
		_, _ = utils.Run(exec.Command("helm", "uninstall", release, "-n", chartNS, "--wait"))
		// Helm leaves CRDs in place.
		_, _ = utils.Run(exec.Command("kubectl", "delete", "-f", "charts/rental-operator/crds/", "--ignore-not-found"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "ns", chartNS, "--ignore-not-found"))
	})

	AfterEach(func() {
		if !CurrentSpecReport().Failed() {
			return
		}
		for _, args := range [][]string{
			{"logs", "-n", chartNS, "-l", "app.kubernetes.io/instance=" + release, "--tail=200"},
			{"get", "events", "-n", chartNS, "--sort-by=.lastTimestamp"},
			{"get", "gpurentals,providerconfigs", "-n", sampleNS, "-o", "yaml"},
		} {
			out, _ := utils.Run(exec.Command("kubectl", args...))
			_, _ = fmt.Fprintf(GinkgoWriter, "kubectl %s:\n%s\n", strings.Join(args, " "), out)
		}
	})

	It("installs cluster-wide and starts every controller", func() {
		repo, tag, _ := strings.Cut(projectImage, ":")
		run("helm", "install", release, "./charts/rental-operator", "-n", chartNS,
			"--set", "image.repository="+repo, "--set", "image.tag="+tag,
			"--set", "image.pullPolicy=IfNotPresent", "--wait", "--timeout=3m")
		expectControllersStarted()
	})

	It("reconciles a GPURental cluster-wide", func() {
		expectSampleReconciled()
	})

	It("switches to a namespaced install with no Secret access elsewhere", func() {
		run("helm", "upgrade", release, "./charts/rental-operator", "-n", chartNS, "--reuse-values",
			"--set", "watchNamespaces={"+sampleNS+"}", "--wait", "--timeout=3m")
		expectControllersStarted()

		canList := func(ns string) string {
			out, _ := utils.Run(exec.Command("kubectl", "auth", "can-i", "list", "secrets", "-n", ns, "--as", serviceAcct))
			return strings.TrimSpace(out)
		}
		Expect(canList(sampleNS)).To(HavePrefix("yes"))
		Expect(canList("kube-system")).To(HavePrefix("no"))
	})

	It("reconciles a GPURental in a watched namespace", func() {
		expectSampleReconciled()
	})
})
