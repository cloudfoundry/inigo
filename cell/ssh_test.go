package cell_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/pivotal-golang/archiver/compressor"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"
	"golang.org/x/crypto/ssh"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("SSH", func() {
	verifySSH := func(address, processGuid string, index int) {
		Eventually(
			helpers.LRPInstanceStatePoller(receptorClient, processGuid, index, nil),
		).Should(Equal(receptor.ActualLRPStateRunning))

		clientConfig := &ssh.ClientConfig{
			User: fmt.Sprintf("diego:%s/%d", processGuid, index),
			Auth: []ssh.AuthMethod{ssh.Password("")},
		}

		client, err := ssh.Dial("tcp", address, clientConfig)
		Ω(err).ShouldNot(HaveOccurred())

		session, err := client.NewSession()
		Ω(err).ShouldNot(HaveOccurred())

		output, err := session.Output("/usr/bin/env")
		Ω(err).ShouldNot(HaveOccurred())

		Ω(string(output)).Should(ContainSubstring("USER=vcap"))
	}

	var (
		processGuid         string
		fileServerStaticDir string

		runtime      ifrit.Process
		sshdFileName string
		lrp          receptor.DesiredLRPCreateRequest
		sshdArgs     []string
		address      string
	)

	BeforeEach(func() {
		sshdFileName = "sshd.tgz"
		processGuid = helpers.GenerateGuid()
		address = componentMaker.Addresses.SSHProxy
		sshdArgs = []string{
			"-hostKey=" + componentMaker.SshConfig.HostKey,
			"-authorizedKey=" + componentMaker.SshConfig.AuthorizedKey,
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
		err := tgCompressor.Compress(componentMaker.Artifacts.Executables["sshd"], filepath.Join(fileServerStaticDir, sshdFileName))
		Ω(err).ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime)
	})

	JustBeforeEach(func() {
		lrp = receptor.DesiredLRPCreateRequest{
			ProcessGuid: processGuid,
			Domain:      "inigo",
			Instances:   2,
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
					"-address=0.0.0.0:2222",
				}, sshdArgs...),
			},
			Monitor: &models.RunAction{
				Path: "nc",
				Args: []string{"-z", "127.0.0.1", "2222"},
			},
			StartTimeout: 60,
			RootFS:       "preloaded:" + helpers.PreloadedStacks[0],
			MemoryMB:     128,
			DiskMB:       128,
			Ports:        []uint16{2222},
		}

		err := receptorClient.CreateDesiredLRP(lrp)
		Ω(err).ShouldNot(HaveOccurred())

		Eventually(func() []receptor.ActualLRPResponse {
			lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
			Ω(err).ShouldNot(HaveOccurred())

			return lrps
		}).Should(HaveLen(2))
	})

	Context("when valid processguid and index are used as part of username", func() {

		It("can ssh to appropriate app instance container", func() {
			verifySSH(address, processGuid, 0)
			verifySSH(address, processGuid, 1)
		})

		Context("when invalid password is used", func() {
			var clientConfig *ssh.ClientConfig

			BeforeEach(func() {
				clientConfig = &ssh.ClientConfig{
					User: "diego:" + processGuid + "/0",
					Auth: []ssh.AuthMethod{ssh.Password("invalid:password")},
				}
			})

			It("returns an error", func() {
				Eventually(
					helpers.LRPInstanceStatePoller(receptorClient, processGuid, 0, nil),
				).Should(Equal(receptor.ActualLRPStateRunning))

				_, err := ssh.Dial("tcp", address, clientConfig)
				Ω(err).Should(HaveOccurred())
			})

		})

	})

	Context("when non-existent index is used as part of username", func() {
		var clientConfig *ssh.ClientConfig

		BeforeEach(func() {
			clientConfig = &ssh.ClientConfig{
				User: "diego:" + processGuid + "/3",
				Auth: []ssh.AuthMethod{ssh.Password("")},
			}
		})

		It("returns an error", func() {
			_, err := ssh.Dial("tcp", address, clientConfig)
			Ω(err).Should(HaveOccurred())
		})
	})

	Context("when non-existent process guid is used as part of username", func() {
		var clientConfig *ssh.ClientConfig

		BeforeEach(func() {
			clientConfig = &ssh.ClientConfig{
				User: "diego:not-existing-process-guid/0",
				Auth: []ssh.AuthMethod{ssh.Password("")},
			}
		})

		It("returns an error", func() {
			_, err := ssh.Dial("tcp", address, clientConfig)
			Ω(err).Should(HaveOccurred())
		})
	})

	Context("when invalid username format is used", func() {
		var clientConfig *ssh.ClientConfig

		BeforeEach(func() {
			clientConfig = &ssh.ClientConfig{
				User: "vcap",
				Auth: []ssh.AuthMethod{ssh.Password("some-password")},
			}
		})

		It("returns an error", func() {
			_, err := ssh.Dial("tcp", address, clientConfig)
			Ω(err).Should(HaveOccurred())
		})
	})
})
