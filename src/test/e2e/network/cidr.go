package network

import (
	"flag"
	"net"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
)

var (
	serviceCIDRString string
	podCIDRString     string
)

func init() {
	flag.StringVar(&serviceCIDRString, "cfcr.serviceCIDR", "10.100.200.0/24", "CIDR range for services")
	flag.StringVar(&podCIDRString, "cfcr.podCIDR", "10.200.0.0/16", "CIDR range for pods")
}

var _ = Describe("CIDR values", func() {
	f := framework.NewDefaultFramework("cidr")
	var (
		c           clientset.Interface
		serviceCIDR *net.IPNet
		podCIDR     *net.IPNet
	)

	BeforeEach(func() {
		c = f.ClientSet
	})

	Context("Services", func() {
		BeforeEach(func() {
			var err error
			_, serviceCIDR, err = net.ParseCIDR(serviceCIDRString)
			Expect(err).NotTo(HaveOccurred())
		})

		framework.ConformanceIt("the Kubernetes service uses the first IP in the services CIDR", func() {
			service, err := c.CoreV1().Services("default").Get("kubernetes", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			firstIP := getFirstIP(serviceCIDR)

			ip := net.ParseIP(service.Spec.ClusterIP)
			Expect(ip).NotTo(BeNil(), "Failed to parse IP from service spec")
			Expect(serviceCIDR.Contains(ip)).To(Equal(true), "Service IP is not in the service CIDR")
			Expect(ip.Equal(firstIP)).To(Equal(true), "Service IP does not match first IP of service CIDR")
		})

		framework.ConformanceIt("creates services in the specified CIDR", func() {
			By("Creating a test service")

			serviceName := "cidr-test-service"
			service := framework.CreateServiceSpec(serviceName, "", false, nil)
			service, err := c.CoreV1().Services(f.Namespace.Name).Create(service)
			Expect(err).NotTo(HaveOccurred(), "failed to create service: %s", serviceName)
			defer func() {
				By("Deleting the test service")
				defer GinkgoRecover()
				c.CoreV1().Services(f.Namespace.Name).Delete(service.Name, nil)
			}()

			ip := net.ParseIP(service.Spec.ClusterIP)
			Expect(ip).NotTo(BeNil(), "Failed to parse IP from service spec")
			Expect(serviceCIDR.Contains(ip)).To(Equal(true), "Service IP is not in the service CIDR")
		})
	})

	Context("Pods", func() {
		BeforeEach(func() {
			var err error
			_, podCIDR, err = net.ParseCIDR(podCIDRString)
			Expect(err).NotTo(HaveOccurred())
		})

		framework.ConformanceIt("creates pods in the specified CIDR", func() {
			By("creating a test pod")

			pod, err := framework.CreatePod(c, f.Namespace.Name, nil, nil, false, "")
			Expect(err).NotTo(HaveOccurred())

			ip := net.ParseIP(pod.Status.PodIP)
			Expect(ip).NotTo(BeNil(), "Failed to parse IP from pod spec")
			Expect(podCIDR.Contains(ip)).To(Equal(true), "Pod IP is not in the pod CIDR")
		})
	})
})

func getFirstIP(cidr *net.IPNet) net.IP {
	ip := cidr.IP.To4()
	Expect(ip).NotTo(BeNil())

	ip = ip.Mask(cidr.Mask)
	ip[3]++

	return ip
}
