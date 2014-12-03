package cell_test

import (
	"os"

	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Starting an arbitrary LRP", func() {
	var (
		processGuid string

		runtime ifrit.Process
	)

	BeforeEach(func() {
		processGuid = factories.GenerateGuid()

		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"router", componentMaker.Router()},
			{"exec", componentMaker.Executor()},
			{"rep", componentMaker.Rep()},
			{"converger", componentMaker.Converger()},
			{"auctioneer", componentMaker.Auctioneer()},
			{"receptor", componentMaker.Receptor()},
			{"route-emitter", componentMaker.RouteEmitter()},
		}))
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime)
	})

	Context("when desiring a LRP", func() {
		It("eventually runs on an executor", func() {
			err := receptorClient.CreateDesiredLRP(receptor.DesiredLRPCreateRequest{
				Domain:      "inigo",
				ProcessGuid: processGuid,
				Instances:   1,
				Stack:       componentMaker.Stack,
				MemoryMB:    128,
				DiskMB:      1024,
				Ports: []uint32{
					8080,
				},

				Action: &models.RunAction{
					Path: "bash",
					Args: []string{
						"-c",
						"while true; do sleep 2; done",
					},
				},

				Monitor: &models.RunAction{
					Path: "bash",
					Args: []string{"-c", "echo all good"},
				},
			})
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(func() []receptor.ActualLRPResponse {
				lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
				Ω(err).ShouldNot(HaveOccurred())

				return lrps
			}).Should(HaveLen(1))
		})

		Context("and its unhealthy and its start timeout is exceeded", func() {
			It("eventually stops the LRP", func() {
				err := receptorClient.CreateDesiredLRP(receptor.DesiredLRPCreateRequest{
					Domain:      "inigo",
					ProcessGuid: processGuid,
					Instances:   1,
					Stack:       componentMaker.Stack,
					MemoryMB:    128,
					DiskMB:      1024,
					Ports: []uint32{
						8080,
					},
					StartTimeout: 1,
					Action: &models.RunAction{
						Path: "bash",
						Args: []string{
							"-c",
							"while true; do sleep 2; done",
						},
					},

					Monitor: &models.RunAction{
						Path: "bash",
						Args: []string{"-c", "exit -1"},
					},
				})
				Ω(err).ShouldNot(HaveOccurred())

				Eventually(func() []receptor.ActualLRPResponse {
					lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
					Ω(err).ShouldNot(HaveOccurred())

					return lrps
				}).Should(HaveLen(1))

				Eventually(func() []receptor.ActualLRPResponse {
					lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
					Ω(err).ShouldNot(HaveOccurred())

					return lrps
				}).Should(HaveLen(0))
			})
		})
	})

	Context("when desiring a LRP with a Docker rootfs", func() {
		var helloWorldInstancePoller func() []string

		BeforeEach(func() {
			helloWorldInstancePoller = helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-to-simple")
		})

		It("eventually runs on an executor", func() {
			err := receptorClient.CreateDesiredLRP(receptor.DesiredLRPCreateRequest{
				Domain:      "inigo",
				ProcessGuid: processGuid,
				Instances:   1,
				Stack:       componentMaker.Stack,
				RootFSPath:  "docker:///cloudfoundry/inigodockertest",
				Routes:      []string{"route-to-simple"},
				Ports: []uint32{
					8080,
				},

				Action: &models.RunAction{
					Path: "/dockerapp",
				},
			})
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(func() []receptor.ActualLRPResponse {
				lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
				Ω(err).ShouldNot(HaveOccurred())

				return lrps
			}).Should(HaveLen(1))

			Eventually(helloWorldInstancePoller).Should(Equal([]string{"0"}))
		})
	})
})
