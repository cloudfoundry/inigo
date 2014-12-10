package cell_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("LRP", func() {
	var (
		processGuid string

		runtime ifrit.Process
	)

	BeforeEach(func() {
		fileServer, fileServerStaticDir := componentMaker.FileServer()

		processGuid = factories.GenerateGuid()

		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"router", componentMaker.Router()},
			{"file-server", fileServer},
			{"exec", componentMaker.Executor()},
			{"rep", componentMaker.Rep()},
			{"converger", componentMaker.Converger()},
			{"auctioneer", componentMaker.Auctioneer()},
			{"receptor", componentMaker.Receptor()},
			{"route-emitter", componentMaker.RouteEmitter()},
		}))

		archive_helper.CreateZipArchive(
			filepath.Join(fileServerStaticDir, "lrp.zip"),
			fixtures.HelloWorldIndexLRP(),
		)
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime)
	})

	Describe("desiring", func() {
		var lrp receptor.DesiredLRPCreateRequest

		BeforeEach(func() {
			lrp = receptor.DesiredLRPCreateRequest{
				Domain:      "inigo",
				ProcessGuid: processGuid,
				Instances:   1,
				Stack:       componentMaker.Stack,

				Routes: []string{"lrp-route"},
				Ports:  []uint32{8080},

				Setup: &models.DownloadAction{
					From: fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "lrp.zip"),
					To:   ".",
				},

				Action: &models.RunAction{
					Path: "ruby",
					Args: []string{"server.rb"},
					Env:  []models.EnvironmentVariable{{"PORT", "8080"}},
				},

				Monitor: &models.RunAction{
					Path: "true",
				},
			}
		})

		JustBeforeEach(func() {
			err := receptorClient.CreateDesiredLRP(lrp)
			Ω(err).ShouldNot(HaveOccurred())
		})

		It("eventually runs", func() {
			Eventually(func() []receptor.ActualLRPResponse {
				lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
				Ω(err).ShouldNot(HaveOccurred())

				return lrps
			}).Should(HaveLen(1))

			Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "lrp-route")).Should(ConsistOf([]string{"0"}))
		})

		Context("when it's unhealthy for longer than its start timeout", func() {
			BeforeEach(func() {
				lrp.StartTimeout = 5

				lrp.Monitor = &models.RunAction{
					Path: "false",
				}
			})

			It("eventually stops the LRP", func() {
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

		Context("with a Docker rootfs", func() {
			BeforeEach(func() {
				lrp.RootFSPath = "docker:///cloudfoundry/inigodockertest"

				lrp.Action = &models.RunAction{
					Path: "/dockerapp",
				}
			})

			It("eventually runs", func() {
				Eventually(func() []receptor.ActualLRPResponse {
					lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
					Ω(err).ShouldNot(HaveOccurred())

					return lrps
				}).Should(HaveLen(1))

				Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "lrp-route")).Should(ConsistOf([]string{"0"}))
			})
		})

		Describe("when started with 2 instances", func() {
			BeforeEach(func() {
				lrp.Instances = 2
			})

			JustBeforeEach(func() {
				Eventually(func() []receptor.ActualLRPResponse {
					lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
					Ω(err).ShouldNot(HaveOccurred())

					return lrps
				}).Should(HaveLen(2))
			})

			Describe("changing the instances", func() {
				var newInstances int

				BeforeEach(func() {
					newInstances = 0 // base value; overridden below
				})

				JustBeforeEach(func() {
					err := receptorClient.UpdateDesiredLRP(processGuid, receptor.DesiredLRPUpdateRequest{
						Instances: &newInstances,
					})
					Ω(err).ShouldNot(HaveOccurred())
				})

				Context("scaling it up to 3", func() {
					BeforeEach(func() {
						newInstances = 3
					})

					It("scales up to the correct number of instances", func() {
						Eventually(func() []receptor.ActualLRPResponse {
							lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
							Ω(err).ShouldNot(HaveOccurred())

							return lrps
						}).Should(HaveLen(3))

						Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "lrp-route")).Should(ConsistOf([]string{"0", "1", "2"}))
					})
				})

				Context("scaling it down to 1", func() {
					BeforeEach(func() {
						newInstances = 1
					})

					It("scales down to the correct number of instances", func() {
						Eventually(func() []receptor.ActualLRPResponse {
							lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
							Ω(err).ShouldNot(HaveOccurred())

							return lrps
						}).Should(HaveLen(1))

						Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "lrp-route")).Should(ConsistOf([]string{"0"}))
					})
				})

				Context("scaling it down to 0", func() {
					BeforeEach(func() {
						newInstances = 0
					})

					It("scales down to the correct number of instances", func() {
						Eventually(func() []receptor.ActualLRPResponse {
							lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
							Ω(err).ShouldNot(HaveOccurred())

							return lrps
						}).Should(BeEmpty())

						Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "lrp-route")).Should(BeEmpty())
					})

					It("can be scaled back up", func() {
						newInstances := 1
						err := receptorClient.UpdateDesiredLRP(processGuid, receptor.DesiredLRPUpdateRequest{
							Instances: &newInstances,
						})
						Ω(err).ShouldNot(HaveOccurred())

						Eventually(func() []receptor.ActualLRPResponse {
							lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
							Ω(err).ShouldNot(HaveOccurred())

							return lrps
						}).Should(HaveLen(1))

						Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "lrp-route")).Should(ConsistOf([]string{"0"}))
					})
				})
			})

			Describe("deleting it", func() {
				JustBeforeEach(func() {
					err := receptorClient.DeleteDesiredLRP(processGuid)
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("stops all instances", func() {
					Eventually(func() []receptor.ActualLRPResponse {
						lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
						Ω(err).ShouldNot(HaveOccurred())

						return lrps
					}).Should(BeEmpty())

					Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "lrp-route")).Should(BeEmpty())
				})
			})
		})
	})
})
