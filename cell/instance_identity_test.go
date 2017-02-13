package cell_test

import (
	"encoding/pem"
	"io/ioutil"
	"os"
	"path/filepath"

	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/inigo/helpers"
	"code.cloudfoundry.org/rep/cmd/rep/config"

	"crypto/x509"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"
)

var _ = Describe("InstanceIdentity", func() {
	var (
		credDir     string
		cellProcess ifrit.Process
	)

	BeforeEach(func() {
		var err error
		credDir, err = ioutil.TempDir(os.TempDir(), "instance-creds")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Chmod(credDir, 0755)).To(Succeed())

		configRepCerts := func(cfg *config.RepConfig) {
			cfg.InstanceIdentityCredDir = credDir
			caPath, err := filepath.Abs("../fixtures/certs/ca.crt")
			Expect(err).NotTo(HaveOccurred())
			keyPath, err := filepath.Abs("../fixtures/certs/ca.key")
			Expect(err).NotTo(HaveOccurred())
			cfg.InstanceIdentityCAPath = caPath
			cfg.InstanceIdentityPrivateKeyPath = keyPath
		}

		cellGroup := grouper.Members{
			{"rep", componentMaker.Rep(configRepCerts)},
			{"auctioneer", componentMaker.Auctioneer()},
		}
		cellProcess = ginkgomon.Invoke(grouper.NewParallel(os.Interrupt, cellGroup))

		Eventually(func() (models.CellSet, error) { return bbsServiceClient.Cells(logger) }).Should(HaveLen(1))
	})

	AfterEach(func() {
		os.RemoveAll(credDir)
		helpers.StopProcesses(cellProcess)
	})

	It("should place a key and certificate signed by the rep's ca in the right location", func() {
		By("running the task and extracting the cert and key")
		containerCert, containerKey := runTaskAndFetchCertAndKey()
		cert := parseCertificate(containerCert, false)

		By("verify the certificate is signed properly")
		caPath, err := filepath.Abs("../fixtures/certs/ca.crt")
		Expect(err).NotTo(HaveOccurred())
		caCertContent, err := ioutil.ReadFile(caPath)
		Expect(err).NotTo(HaveOccurred())
		caCert := parseCertificate(caCertContent, true)
		verifyCertificateIsSignedBy(cert, caCert)

		By("verify the private key matches the cert public key")
		key, err := x509.ParsePKCS1PrivateKey(containerKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(&key.PublicKey).To(Equal(cert.PublicKey))
	})
})

func runTaskAndFetchCertAndKey() ([]byte, []byte) {
	guid := helpers.GenerateGuid()

	expectedTask := helpers.TaskCreateRequest(
		guid,
		&models.RunAction{
			User: "vcap",
			Path: "sh",
			Args: []string{"-c", "cat /etc/cf-instance-credentials/instance.crt /etc/cf-instance-credentials/instance.key > thingy"},
		},
	)
	expectedTask.ResultFile = "/home/vcap/thingy"

	err := bbsClient.DesireTask(logger, expectedTask.TaskGuid, expectedTask.Domain, expectedTask.TaskDefinition)
	Expect(err).NotTo(HaveOccurred())

	var task *models.Task
	Eventually(func() interface{} {
		var err error

		task, err = bbsClient.TaskByGuid(logger, guid)
		Expect(err).NotTo(HaveOccurred())

		return task.State
	}).Should(Equal(models.Task_Completed))

	Expect(task.Failed).To(BeFalse())

	block, rest := pem.Decode([]byte(task.Result))
	Expect(rest).NotTo(BeEmpty())
	Expect(block).NotTo(BeNil())
	cert := block.Bytes
	block, rest = pem.Decode(rest)
	Expect(rest).To(BeEmpty())
	Expect(block).NotTo(BeNil())
	key := block.Bytes

	return cert, key
}

func parseCertificate(cert []byte, pemEncoded bool) *x509.Certificate {
	if pemEncoded {
		block, _ := pem.Decode(cert)
		Expect(block).NotTo(BeNil())
		cert = block.Bytes
	}
	certs, err := x509.ParseCertificates(cert)
	Expect(err).NotTo(HaveOccurred())
	Expect(certs).To(HaveLen(1))
	return certs[0]
}

func verifyCertificateIsSignedBy(cert, parentCert *x509.Certificate) {
	certPool := x509.NewCertPool()
	certPool.AddCert(parentCert)
	certs, err := cert.Verify(x509.VerifyOptions{
		Roots: certPool,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(certs).To(HaveLen(1))
	Expect(certs[0]).To(ContainElement(parentCert))
}
