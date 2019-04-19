package network

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
)

const dnsTestPodHostName = "dns-querier-1"
const dnsTestServiceName = "dns-test-service"

var _ = Describe("Kubernetes DNS", func() {
	f := framework.NewDefaultFramework("dns")

	/*
		Testname: DNS, cluster
		Description: A conformant CFCR deployment MUST have DNS running in the cluster
	*/
	framework.ConformanceIt("A DNS deployment should be running in the system namespace", func() {
		var c clientset.Interface = f.ClientSet

		By("Finding DNS deployments")
		label := labels.SelectorFromSet(labels.Set(map[string]string{"k8s-app": "kube-dns"}))
		options := metav1.ListOptions{LabelSelector: label.String()}

		deployments, err := c.AppsV1().Deployments(metav1.NamespaceSystem).List(options)
		Expect(err).NotTo(HaveOccurred(), "Failed to list deployments in namespace: %s", metav1.NamespaceSystem)
		Expect(len(deployments.Items)).Should(BeNumerically(">=", 1))

		for _, deployment := range deployments.Items {
			framework.Logf("Ensuring status for deployment %q is as expected", deployment.Name)
			err = framework.WaitForDeploymentComplete(c, &deployment)
			Expect(err).NotTo(HaveOccurred())

			framework.Logf("Ensuring that all pods are running for %q", deployment.Name)
			podlist, err := framework.GetPodsForDeployment(c, &deployment)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(podlist.Items)).Should(BeNumerically("==", *deployment.Spec.Replicas))

			podclient := f.PodClientNS(metav1.NamespaceSystem)
			for _, pod := range podlist.Items {
				Expect(podclient.PodIsReady(pod.Name)).To(Equal(true))
			}
		}
	})

	/*
		Testname: DNS, cluster
		Description: When a Pod is created, the pod MUST be able to resolve cluster DNS entries such as kubernetes.default
			via DNS and /etc/hosts.
		TODO: Remove - duplication of an existing K8s Conformance test just for verification
	*/
	framework.ConformanceIt("Should be able to resolve the internal service DNS name", func() {
		namesToResolve := []string{
			"kubernetes.default",
			"kubernetes.default.svc",
			fmt.Sprintf("kubernetes.default.svc.%s", framework.TestContext.ClusterDNSDomain),
		}

		hostFQDN := fmt.Sprintf("%s.%s.%s.svc.%s", dnsTestPodHostName, dnsTestServiceName, f.Namespace.Name, framework.TestContext.ClusterDNSDomain)
		hostEntries := []string{hostFQDN, dnsTestPodHostName}
		wheezyProbeCmd, wheezyFileNames := createProbeCommand(namesToResolve, hostEntries, "", "wheezy", f.Namespace.Name, framework.TestContext.ClusterDNSDomain)
		jessieProbeCmd, jessieFileNames := createProbeCommand(namesToResolve, hostEntries, "", "jessie", f.Namespace.Name, framework.TestContext.ClusterDNSDomain)
		By("Running these commands on wheezy: " + wheezyProbeCmd + "\n")
		By("Running these commands on jessie: " + jessieProbeCmd + "\n")

		By("creating a pod to probe DNS")
		pod := createDNSPod(f.Namespace.Name, wheezyProbeCmd, jessieProbeCmd, dnsTestPodHostName, dnsTestServiceName)
		validateDNSResults(f, pod, append(wheezyFileNames, jessieFileNames...))
	})
})
