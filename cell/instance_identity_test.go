package cell_test

import (
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	archive_helper "code.cloudfoundry.org/archiver/extractor/test_helper"
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/inigo/fixtures"
	"code.cloudfoundry.org/inigo/helpers"
	"code.cloudfoundry.org/rep/cmd/rep/config"

	"crypto/tls"
	"crypto/x509"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"
)

var _ = Describe("InstanceIdentity", func() {
	var (
		credDir             string
		cellProcess         ifrit.Process
		fileServerStaticDir string
	)

	BeforeEach(func() {
		var err error
		credDir, err = ioutil.TempDir(os.TempDir(), "instance-creds")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Chmod(credDir, 0755)).To(Succeed())

		configRepCerts := func(cfg *config.RepConfig) {
			cfg.InstanceIdentityCredDir = credDir
			caPath, err := filepath.Abs("../fixtures/certs/instance-identity.crt")
			Expect(err).NotTo(HaveOccurred())
			keyPath, err := filepath.Abs("../fixtures/certs/instance-identity.key")
			Expect(err).NotTo(HaveOccurred())
			cfg.InstanceIdentityCAPath = caPath
			cfg.InstanceIdentityPrivateKeyPath = keyPath
		}

		exportNetworkVars := func(config *config.RepConfig) {
			config.ExportNetworkEnvVars = true
		}

		var fileServer ifrit.Runner
		fileServer, fileServerStaticDir = componentMaker.FileServer()
		archiveFiles := fixtures.GoServerApp()
		archive_helper.CreateZipArchive(
			filepath.Join(fileServerStaticDir, "lrp.zip"),
			archiveFiles,
		)

		cellGroup := grouper.Members{
			{"router", componentMaker.Router()},
			{"file-server", fileServer},
			{"rep", componentMaker.Rep(configRepCerts, exportNetworkVars)},
			{"auctioneer", componentMaker.Auctioneer()},
			{"route-emitter", componentMaker.RouteEmitter()},
		}
		cellProcess = ginkgomon.Invoke(grouper.NewParallel(os.Interrupt, cellGroup))

		Eventually(func() (models.CellSet, error) { return bbsServiceClient.Cells(logger) }).Should(HaveLen(1))
	})

	AfterEach(func() {
		os.RemoveAll(credDir)
		helpers.StopProcesses(cellProcess)
	})

	verifyCertAndKey := func(command string) {
		By("running the task and getting the concatenated pem cert and key")
		result := runTaskAndGetCommandOutput(command)
		block, rest := pem.Decode([]byte(result))
		Expect(rest).NotTo(BeEmpty())
		Expect(block).NotTo(BeNil())
		containerCert := block.Bytes
		block, rest = pem.Decode(rest)
		Expect(rest).To(BeEmpty())
		Expect(block).NotTo(BeNil())
		containerKey := block.Bytes

		By("verify the certificate is signed properly")
		cert := parseCertificate(containerCert, false)
		caPath, err := filepath.Abs("../fixtures/certs/instance-identity.crt")
		Expect(err).NotTo(HaveOccurred())
		caCertContent, err := ioutil.ReadFile(caPath)
		Expect(err).NotTo(HaveOccurred())
		caCert := parseCertificate(caCertContent, true)
		verifyCertificateIsSignedBy(cert, caCert)

		By("verify the private key matches the cert public key")
		key, err := x509.ParsePKCS1PrivateKey(containerKey)
		Expect(err).NotTo(HaveOccurred())
		Expect(&key.PublicKey).To(Equal(cert.PublicKey))
	}

	It("should place a key and certificate signed by the rep's ca in the right location", func() {
		verifyCertAndKey("cat /etc/cf-instance-credentials/instance.crt /etc/cf-instance-credentials/instance.key")
	})

	It("should add instance identity environment variables to the container", func() {
		verifyCertAndKey("cat $CF_INSTANCE_CERT $CF_INSTANCE_KEY")
	})

	Context("when a server uses the provided cert and key", func() {
		var (
			processGuid string
			ipAddress   string
		)

		BeforeEach(func() {
			processGuid = helpers.GenerateGuid()
			lrp := helpers.DefaultLRPCreateRequest(processGuid, "log-guid", 1)
			lrp.Setup = nil
			lrp.CachedDependencies = []*models.CachedDependency{{
				From:      fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "lrp.zip"),
				To:        "/tmp/diego/lrp",
				Name:      "lrp bits",
				CacheKey:  "lrp-cache-key",
				LogSource: "APP",
			}}
			lrp.LegacyDownloadUser = "vcap"
			lrp.Privileged = true
			lrp.Action = models.WrapAction(&models.RunAction{
				User: "vcap",
				Path: "/tmp/diego/lrp/go-server",
				Env: []*models.EnvironmentVariable{
					{"PORT", "8080"},
					{"HTTPS_PORT", "8081"},
				},
			})
			err := bbsClient.DesireLRP(logger, lrp)
			Expect(err).NotTo(HaveOccurred())
			Eventually(helpers.LRPStatePoller(logger, bbsClient, processGuid, nil)).Should(Equal(models.ActualLRPStateRunning))

			ipAddress = getContainerInternalIP()
		})

		Context("and a client app tries to connect to the server using the ca cert", func() {
			var (
				client http.Client
			)

			BeforeEach(func() {
				caCertContent, err := ioutil.ReadFile("../fixtures/certs/ca-with-no-max-path-length.crt")
				Expect(err).NotTo(HaveOccurred())
				caCert := parseCertificate(caCertContent, true)
				rootCAs := x509.NewCertPool()
				rootCAs.AddCert(caCert)
				client = http.Client{}
				client.Transport = &http.Transport{
					TLSClientConfig: &tls.Config{
						InsecureSkipVerify: false,
						RootCAs:            rootCAs,
					},
				}
			})

			It("successfully connects and verify the sever identity", func() {
				resp, err := client.Get(fmt.Sprintf("https://%s:8081/env", ipAddress))
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(http.StatusOK))
				defer resp.Body.Close()
				body, err := ioutil.ReadAll(resp.Body)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(body)).To(ContainSubstring("CF_INSTANCE_INTERNAL_IP=" + ipAddress))
			})
		})
	})

	Context("when a client uses the provided cert and key", func() {
		It("a server can accept connection from it over tls", func() {
		})
	})
})

func getContainerInternalIP() string {
	By("getting the internal ip address of the container")
	body, code, err := helpers.ResponseBodyAndStatusCodeFromHost(componentMaker.Addresses.Router, helpers.DefaultHost, "env")
	Expect(err).NotTo(HaveOccurred())
	Expect(code).To(Equal(http.StatusOK))
	var ipAddress string
	for _, line := range strings.Fields(string(body)) {
		if strings.HasPrefix(line, "CF_INSTANCE_INTERNAL_IP=") {
			ipAddress = strings.Split(line, "=")[1]
		}
	}
	return ipAddress
}

func runTaskAndGetCommandOutput(command string) string {
	guid := helpers.GenerateGuid()

	expectedTask := helpers.TaskCreateRequest(
		guid,
		&models.RunAction{
			User: "vcap",
			Path: "sh",
			Args: []string{"-c", fmt.Sprintf("%s > thingy", command)},
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

	return task.Result
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
