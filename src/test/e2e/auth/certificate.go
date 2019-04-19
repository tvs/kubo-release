package certificate

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"time"

	. "github.com/onsi/ginkgo"

	"k8s.io/api/certificates/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	v1beta1client "k8s.io/client-go/kubernetes/typed/certificates/v1beta1"
	"k8s.io/client-go/util/cert"
	"k8s.io/kubernetes/test/e2e/framework"
)

var _ = Describe("Certificate Signing Requests", func() {
	f := framework.NewDefaultFramework("csr")
	var c clientset.Interface

	BeforeEach(func() {
		c = f.ClientSet
	})

	framework.ConformanceIt("when a user gets a CSR signed, it can communicate with the API server", func() {
		By("creating a CSR for a user in `system:masters`")
		const commonName string = "csr-test"

		privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
		framework.ExpectNoError(err)

		csrb, err := cert.MakeCSR(privateKey, &pkix.Name{CommonName: commonName, Organization: []string{"system:masters"}}, nil, nil)
		framework.ExpectNoError(err)

		csr := &v1beta1.CertificateSigningRequest{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: commonName + "-",
			},
			Spec: v1beta1.CertificateSigningRequestSpec{
				Request: csrb,
				Usages: []v1beta1.KeyUsage{
					v1beta1.UsageClientAuth,
					v1beta1.UsageKeyEncipherment,
				},
			},
		}

		framework.Logf("Creating CSR")
		csr, err = c.CertificatesV1beta1().CertificateSigningRequests().Create(csr)
		framework.ExpectNoError(err)

		csrName := csr.Name

		framework.Logf("Approving CSR")
		framework.ExpectNoError(wait.Poll(5*time.Second, time.Minute, func() (bool, error) {
			csr.Status.Conditions = []v1beta1.CertificateSigningRequestCondition{
				{
					Type:    v1beta1.CertificateApproved,
					Reason:  "E2E",
					Message: "Set from an e2e test",
				},
			}
			csr, err = c.CertificatesV1beta1().CertificateSigningRequests().UpdateApproval(csr)
			if err != nil {
				csr, _ = c.CertificatesV1beta1().CertificateSigningRequests().Get(csrName, metav1.GetOptions{})
				framework.Logf("error updating approvial: %v", err)
				return false, nil
			}

			return true, nil
		}))

		framework.Logf("Waiting for CSR to be approved")
		framework.ExpectNoError(wait.Poll(5*time.Second, time.Minute, func() (bool, error) {
			csr, err = c.CertificatesV1beta1().CertificateSigningRequests().Get(csrName, metav1.GetOptions{})
			if err != nil {
				framework.Logf("error getting csr: %v", err)
				return false, nil
			}

			if len(csr.Status.Certificate) == 0 {
				framework.Logf("csr not signed yet")
				return false, nil
			}

			return true, nil
		}))

		By("Using it as a client")
		rcfg, err := framework.LoadConfig()
		framework.ExpectNoError(err)

		rcfg.TLSClientConfig.CertData = csr.Status.Certificate
		rcfg.TLSClientConfig.KeyData = pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
		})
		rcfg.TLSClientConfig.CertFile = ""
		rcfg.BearerToken = ""
		rcfg.AuthProvider = nil
		rcfg.Username = ""
		rcfg.Password = ""

		newClient, err := v1beta1client.NewForConfig(rcfg)
		framework.ExpectNoError(err)
		framework.ExpectNoError(newClient.CertificateSigningRequests().Delete(csrName, nil))
	})
})
