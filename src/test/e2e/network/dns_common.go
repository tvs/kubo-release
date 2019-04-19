/*
Copyright 2017 The Kubernetes Authors.

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

package network

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	imageutils "k8s.io/kubernetes/test/utils/image"
)

func reverseArray(arr []string) []string {
	for i := 0; i < len(arr)/2; i++ {
		j := len(arr) - i - 1
		arr[i], arr[j] = arr[j], arr[i]
	}
	return arr
}

func createDNSPod(namespace, wheezyProbeCmd, jessieProbeCmd, podHostName, serviceName string) *v1.Pod {
	dnsPod := &v1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dns-test-" + string(uuid.NewUUID()),
			Namespace: namespace,
		},
		Spec: v1.PodSpec{
			Volumes: []v1.Volume{
				{
					Name: "results",
					VolumeSource: v1.VolumeSource{
						EmptyDir: &v1.EmptyDirVolumeSource{},
					},
				},
			},
			Containers: []v1.Container{
				// TODO: Consider scraping logs instead of running a webserver.
				{
					Name:  "webserver",
					Image: imageutils.GetE2EImage(imageutils.TestWebserver),
					Ports: []v1.ContainerPort{
						{
							Name:          "http",
							ContainerPort: 80,
						},
					},
					VolumeMounts: []v1.VolumeMount{
						{
							Name:      "results",
							MountPath: "/results",
						},
					},
				},
				{
					Name:    "querier",
					Image:   imageutils.GetE2EImage(imageutils.Dnsutils),
					Command: []string{"sh", "-c", wheezyProbeCmd},
					VolumeMounts: []v1.VolumeMount{
						{
							Name:      "results",
							MountPath: "/results",
						},
					},
				},
				{
					Name:    "jessie-querier",
					Image:   imageutils.GetE2EImage(imageutils.JessieDnsutils),
					Command: []string{"sh", "-c", jessieProbeCmd},
					VolumeMounts: []v1.VolumeMount{
						{
							Name:      "results",
							MountPath: "/results",
						},
					},
				},
			},
		},
	}

	dnsPod.Spec.Hostname = podHostName
	dnsPod.Spec.Subdomain = serviceName

	return dnsPod
}

func createProbeCommand(namesToResolve []string, hostEntries []string, ptrLookupIP string, fileNamePrefix, namespace, dnsDomain string) (string, []string) {
	fileNames := make([]string, 0, len(namesToResolve)*2)
	probeCmd := "for i in `seq 1 600`; do "
	for _, name := range namesToResolve {
		// Resolve by TCP and UDP DNS.  Use $$(...) because $(...) is
		// expanded by kubernetes (though this won't expand so should
		// remain a literal, safe > sorry).
		lookup := "A"
		if strings.HasPrefix(name, "_") {
			lookup = "SRV"
		}
		fileName := fmt.Sprintf("%s_udp@%s", fileNamePrefix, name)
		fileNames = append(fileNames, fileName)
		probeCmd += fmt.Sprintf(`check="$$(dig +notcp +noall +answer +search %s %s)" && test -n "$$check" && echo OK > /results/%s;`, name, lookup, fileName)
		fileName = fmt.Sprintf("%s_tcp@%s", fileNamePrefix, name)
		fileNames = append(fileNames, fileName)
		probeCmd += fmt.Sprintf(`check="$$(dig +tcp +noall +answer +search %s %s)" && test -n "$$check" && echo OK > /results/%s;`, name, lookup, fileName)
	}

	for _, name := range hostEntries {
		fileName := fmt.Sprintf("%s_hosts@%s", fileNamePrefix, name)
		fileNames = append(fileNames, fileName)
		probeCmd += fmt.Sprintf(`test -n "$$(getent hosts %s)" && echo OK > /results/%s;`, name, fileName)
	}

	podARecByUDPFileName := fmt.Sprintf("%s_udp@PodARecord", fileNamePrefix)
	podARecByTCPFileName := fmt.Sprintf("%s_tcp@PodARecord", fileNamePrefix)
	probeCmd += fmt.Sprintf(`podARec=$$(hostname -i| awk -F. '{print $$1"-"$$2"-"$$3"-"$$4".%s.pod.%s"}');`, namespace, dnsDomain)
	probeCmd += fmt.Sprintf(`check="$$(dig +notcp +noall +answer +search $${podARec} A)" && test -n "$$check" && echo OK > /results/%s;`, podARecByUDPFileName)
	probeCmd += fmt.Sprintf(`check="$$(dig +tcp +noall +answer +search $${podARec} A)" && test -n "$$check" && echo OK > /results/%s;`, podARecByTCPFileName)
	fileNames = append(fileNames, podARecByUDPFileName)
	fileNames = append(fileNames, podARecByTCPFileName)

	if len(ptrLookupIP) > 0 {
		ptrLookup := fmt.Sprintf("%s.in-addr.arpa.", strings.Join(reverseArray(strings.Split(ptrLookupIP, ".")), "."))
		ptrRecByUDPFileName := fmt.Sprintf("%s_udp@PTR", ptrLookupIP)
		ptrRecByTCPFileName := fmt.Sprintf("%s_tcp@PTR", ptrLookupIP)
		probeCmd += fmt.Sprintf(`check="$$(dig +notcp +noall +answer +search %s PTR)" && test -n "$$check" && echo OK > /results/%s;`, ptrLookup, ptrRecByUDPFileName)
		probeCmd += fmt.Sprintf(`check="$$(dig +tcp +noall +answer +search %s PTR)" && test -n "$$check" && echo OK > /results/%s;`, ptrLookup, ptrRecByTCPFileName)
		fileNames = append(fileNames, ptrRecByUDPFileName)
		fileNames = append(fileNames, ptrRecByTCPFileName)
	}

	probeCmd += "sleep 1; done"
	return probeCmd, fileNames
}

func validateDNSResults(f *framework.Framework, pod *v1.Pod, fileNames []string) {
	By("submitting the pod to kubernetes")
	podClient := f.ClientSet.CoreV1().Pods(f.Namespace.Name)
	defer func() {
		By("deleting the pod")
		defer GinkgoRecover()
		podClient.Delete(pod.Name, metav1.NewDeleteOptions(0))
	}()
	if _, err := podClient.Create(pod); err != nil {
		framework.Failf("Failed to create pod %s/%s: %v", pod.Namespace, pod.Name, err)
	}

	framework.ExpectNoError(f.WaitForPodRunning(pod.Name))

	By("retrieving the pod")
	pod, err := podClient.Get(pod.Name, metav1.GetOptions{})
	if err != nil {
		framework.Failf("Failed to get pod %s/%s: %v", pod.Namespace, pod.Name, err)
	}
	// Try to find results for each expected name.
	By("looking for the results for each expected name from probers")
	assertFilesExist(fileNames, "results", pod, f.ClientSet)

	// TODO: probe from the host, too.

	framework.Logf("DNS probes using %s/%s succeeded\n", pod.Namespace, pod.Name)
}

func assertFilesExist(fileNames []string, fileDir string, pod *v1.Pod, client clientset.Interface) {
	assertFilesContain(fileNames, fileDir, pod, client, false, "")
}

func assertFilesContain(fileNames []string, fileDir string, pod *v1.Pod, client clientset.Interface, check bool, expected string) {
	var failed []string

	framework.ExpectNoError(wait.PollImmediate(time.Second*5, time.Second*600, func() (bool, error) {
		failed = []string{}

		ctx, cancel := context.WithTimeout(context.Background(), framework.SingleCallTimeout)
		defer cancel()

		for _, fileName := range fileNames {
			contents, err := client.CoreV1().RESTClient().Get().
				Context(ctx).
				Namespace(pod.Namespace).
				Resource("pods").
				SubResource("proxy").
				Name(pod.Name).
				Suffix(fileDir, fileName).
				Do().Raw()

			if err != nil {
				if ctx.Err() != nil {
					framework.Failf("Unable to read %s from pod %s/%s: %v", fileName, pod.Namespace, pod.Name, err)
				} else {
					framework.Logf("Unable to read %s from pod %s/%s: %v", fileName, pod.Namespace, pod.Name, err)
				}
				failed = append(failed, fileName)
			} else if check && strings.TrimSpace(string(contents)) != expected {
				framework.Logf("File %s from pod  %s/%s contains '%s' instead of '%s'", fileName, pod.Namespace, pod.Name, string(contents), expected)
				failed = append(failed, fileName)
			}
		}
		if len(failed) == 0 {
			return true, nil
		}
		framework.Logf("Lookups using %s/%s failed for: %v\n", pod.Namespace, pod.Name, failed)
		return false, nil
	}))
	Expect(len(failed)).To(Equal(0))
}
