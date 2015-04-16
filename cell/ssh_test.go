package cell_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/pivotal-golang/archiver/compressor"
	"github.com/pivotal-golang/lager"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"
	"golang.org/x/crypto/ssh"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = FDescribe("SSH", func() {
	var (
		processGuid         string
		fileServerStaticDir string

		runtime      ifrit.Process
		sshdFileName string
		lrp          receptor.DesiredLRPCreateRequest
		sshdArgs     []string
		address      string
		logger       *lagertest.TestLogger
	)

	BeforeEach(func() {
		logger = lagertest.NewTestLogger("test")
		sshdFileName = "sshd.tgz"
		processGuid = helpers.GenerateGuid()
		address = componentMaker.Addresses.SSHProxy
		sshdArgs = []string{
			"-hostKey=" + componentMaker.SshConfig.HostKey,
			"-publicUserKey=" + componentMaker.SshConfig.PublicKey,
		}

		var fileServer ifrit.Runner
		fileServer, fileServerStaticDir = componentMaker.FileServer()
		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"router", componentMaker.Router()},
			{"file-server", fileServer},
			{"exec", componentMaker.Executor()},
			{"rep", componentMaker.Rep()},
			{"converger", componentMaker.Converger()},
			{"auctioneer", componentMaker.Auctioneer()},
			{"route-emitter", componentMaker.RouteEmitter()},
			{"ssh-proxy", componentMaker.SSHProxy()},
		}))

		tgCompressor := compressor.NewTgz()
		logger.Info("sshd-executable", lager.Data{"sshd": componentMaker.Artifacts.Executables["sshd"]})
		err := tgCompressor.Compress(componentMaker.Artifacts.Executables["sshd"], filepath.Join(fileServerStaticDir, sshdFileName))
		Ω(err).ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime)
	})

	JustBeforeEach(func() {
		lrp = receptor.DesiredLRPCreateRequest{
			ProcessGuid:          processGuid,
			Domain:               "inigo",
			Instances:            1,
			EnvironmentVariables: []receptor.EnvironmentVariable{{Name: "SSHTEST", Value: "say-hello"}},
			Setup: &models.SerialAction{
				Actions: []models.Action{
					&models.DownloadAction{
						Artifact: "sshd",
						From:     fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, sshdFileName),
						To:       "/tmp",
						CacheKey: "sshd",
					},
				},
			},
			Action: &models.RunAction{
				Path: "/tmp/sshd",
				Args: append([]string{
					"-address=127.0.0.1:2222",
				}, sshdArgs...),
			},
			Monitor: &models.RunAction{
				Path: "nc",
				Args: []string{"-z", "127.0.0.1", "2222"},
			},
			RootFS:   "preloaded:" + helpers.PreloadedStacks[0],
			MemoryMB: 128,
			DiskMB:   128,
			Ports:    []uint16{2222},
			EgressRules: []models.SecurityGroupRule{
				{
					Protocol:     models.TCPProtocol,
					Destinations: []string{"0.0.0.0-255.255.255.255"},
					Ports:        []uint16{2222},
				},
			},
		}

		err := receptorClient.CreateDesiredLRP(lrp)
		Ω(err).ShouldNot(HaveOccurred())
	})

	It("eventually runs", func() {
		Eventually(func() []receptor.ActualLRPResponse {
			lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
			Ω(err).ShouldNot(HaveOccurred())

			return lrps
		}).Should(HaveLen(1))
	})

	Context("when valid username and password are used", func() {
		var clientConfig *ssh.ClientConfig

		BeforeEach(func() {
			clientConfig = &ssh.ClientConfig{
				User: "diego:" + processGuid + "/0",
				Auth: []ssh.AuthMethod{ssh.Password("")},
			}
		})

		It("can ssh to appropriate app instance container", func() {
			Eventually(
				helpers.LRPInstanceStatePoller(receptorClient, processGuid, 0, nil),
			).Should(Equal(receptor.ActualLRPStateRunning))
			logger.Info("dialing-to-app", lager.Data{"address": address})
			client, err := ssh.Dial("tcp", address, clientConfig)
			Ω(err).ShouldNot(HaveOccurred())

			session, err := client.NewSession()
			Ω(err).ShouldNot(HaveOccurred())

			output, err := session.Output("echo -n $SSHTEST")
			Ω(err).ShouldNot(HaveOccurred())

			Ω(string(output)).Should(Equal("say-hello"))
		})

	})

})
