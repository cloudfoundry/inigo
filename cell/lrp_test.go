package cell_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cloudfoundry-incubator/bbs/models"
	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/route-emitter/cfroutes"
	"github.com/cloudfoundry-incubator/runtime-schema/diego_errors"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
	"github.com/pivotal-golang/lager"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("LRP", func() {
	var (
		processGuid         string
		archiveFiles        []archive_helper.ArchiveFile
		fileServerStaticDir string

		runtime ifrit.Process
	)

	BeforeEach(func() {
		processGuid = helpers.GenerateGuid()

		var fileServer ifrit.Runner
		fileServer, fileServerStaticDir = componentMaker.FileServer()
		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"router", componentMaker.Router()},
			{"file-server", fileServer},
			{"rep", componentMaker.Rep()},
			{"converger", componentMaker.Converger()},
			{"auctioneer", componentMaker.Auctioneer()},
			{"route-emitter", componentMaker.RouteEmitter()},
		}))

		archiveFiles = fixtures.GoServerApp()
	})

	JustBeforeEach(func() {
		archive_helper.CreateZipArchive(
			filepath.Join(fileServerStaticDir, "lrp.zip"),
			archiveFiles,
		)
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime)
	})

	Describe("desiring", func() {
		var lrp *models.DesiredLRP

		BeforeEach(func() {
			lrp = helpers.DefaultLRPCreateRequest(processGuid, "log-guid", 1)
			lrp.Setup = models.WrapAction(&models.DownloadAction{
				From: fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "lrp.zip"),
				To:   "/tmp",
				User: "vcap",
			})
			lrp.Action = models.WrapAction(&models.RunAction{
				User: "vcap",
				Path: "/tmp/go-server",
				Env:  []*models.EnvironmentVariable{{"PORT", "8080"}},
			})
		})

		JustBeforeEach(func() {
			err := bbsClient.DesireLRP(lrp)
			Expect(err).NotTo(HaveOccurred())
		})

		It("eventually runs", func() {
			Eventually(helpers.LRPStatePoller(bbsClient, processGuid, nil)).Should(Equal(models.ActualLRPStateRunning))
			Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost)).Should(ConsistOf([]string{"0"}))
		})

		Context("when it's unhealthy for longer than its start timeout", func() {
			BeforeEach(func() {
				lrp.StartTimeout = 5

				lrp.Monitor = models.WrapAction(&models.RunAction{
					User: "vcap",
					Path: "false",
				})
			})

			It("eventually marks the LRP as crashed", func() {
				Eventually(
					helpers.LRPStatePoller(receptorClient, processGuid, nil),
				).Should(Equal(receptor.ActualLRPStateCrashed))
			})
		})

		Describe("updating routes", func() {
			BeforeEach(func() {
				lrp.Ports = []uint16{8080, 9080}
				lrp.Routes = cfroutes.LegacyCFRoutes{{Port: 8080, Hostnames: []string{"lrp-route-8080"}}}.LegacyRoutingInfo()

				lrp.Action = models.WrapAction(&models.RunAction{
					User: "vcap",
					Path: "/tmp/go-server",
					Env:  []*models.EnvironmentVariable{{"PORT", "8080 9080"}},
				})
			})

			It("can not access container ports without routes", func() {
				Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "lrp-route-8080")).Should(Equal(http.StatusOK))
				Consistently(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "lrp-route-8080")).Should(Equal(http.StatusOK))
				Consistently(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "lrp-route-9080")).Should(Equal(http.StatusNotFound))
			})

			Context("when adding a route", func() {
				logger := lagertest.NewTestLogger("test")

				JustBeforeEach(func() {
					desiredUpdate := models.DesiredLRPUpdate{
						Routes: cfroutes.LegacyCFRoutes{
							{Port: 8080, Hostnames: []string{"lrp-route-8080"}},
							{Port: 9080, Hostnames: []string{"lrp-route-9080"}},
						}.LegacyRoutingInfo(),
					},

						logger.Info("just-before-each", lager.Data{
							"processGuid":   processGuid,
							"updateRequest": desiredUpdate})

							err := BBSClient.UpdateDesiredLRP(processGuid, desiredUpdate) 
					})
					logger.Info("after-update-desired", lager.Data{
						"error": err,
					})
					Expect(err).NotTo(HaveOccurred())
				})

				It("can immediately access the container port with the associated routes", func() {
					Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "lrp-route-8080")).Should(Equal(http.StatusOK))
					Consistently(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "lrp-route-8080")).Should(Equal(http.StatusOK))

					Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "lrp-route-9080")).Should(Equal(http.StatusOK))
					Consistently(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "lrp-route-9080")).Should(Equal(http.StatusOK))
				})
			})
		})

		Describe("when started with 2 instances", func() {
			BeforeEach(func() {
				lrp.Instances = 2
			})

			JustBeforeEach(func() {
				Eventually(func() []receptor.ActualLRPResponse {
					lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
					Expect(err).NotTo(HaveOccurred())

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
					Expect(err).NotTo(HaveOccurred())
				})

				Context("scaling it up to 3", func() {
					BeforeEach(func() {
						newInstances = 3
					})

					It("scales up to the correct number of instances", func() {
						Eventually(func() []receptor.ActualLRPResponse {
							lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
							Expect(err).NotTo(HaveOccurred())

							return lrps
						}).Should(HaveLen(3))

						Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost)).Should(ConsistOf([]string{"0", "1", "2"}))
					})
				})

				Context("scaling it down to 1", func() {
					BeforeEach(func() {
						newInstances = 1
					})

					It("scales down to the correct number of instances", func() {
						Eventually(func() []receptor.ActualLRPResponse {
							lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
							Expect(err).NotTo(HaveOccurred())

							return lrps
						}).Should(HaveLen(1))

						Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost)).Should(ConsistOf([]string{"0"}))
					})
				})

				Context("scaling it down to 0", func() {
					BeforeEach(func() {
						newInstances = 0
					})

					It("scales down to the correct number of instances", func() {
						Eventually(func() []receptor.ActualLRPResponse {
							lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
							Expect(err).NotTo(HaveOccurred())

							return lrps
						}).Should(BeEmpty())

						Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost)).Should(BeEmpty())
					})

					It("can be scaled back up", func() {
						newInstances := 1
						err := receptorClient.UpdateDesiredLRP(processGuid, receptor.DesiredLRPUpdateRequest{
							Instances: &newInstances,
						})
						Expect(err).NotTo(HaveOccurred())

						Eventually(func() []receptor.ActualLRPResponse {
							lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
							Expect(err).NotTo(HaveOccurred())

							return lrps
						}).Should(HaveLen(1))

						Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost)).Should(ConsistOf([]string{"0"}))
					})
				})
			})

			Describe("deleting it", func() {
				JustBeforeEach(func() {
					err := receptorClient.DeleteDesiredLRP(processGuid)
					Expect(err).NotTo(HaveOccurred())
				})

				It("stops all instances", func() {
					Eventually(func() []receptor.ActualLRPResponse {
						lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
						Expect(err).NotTo(HaveOccurred())

						return lrps
					}).Should(BeEmpty())

					Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost)).Should(BeEmpty())
				})
			})
		})

		Context("Egress Rules", func() {
			BeforeEach(func() {
				archiveFiles = fixtures.CurlLRP()
				lrp.Action = models.WrapAction(&models.RunAction{
					User: "vcap",
					Path: "bash",
					Args: []string{"server.sh"},
					Env:  []*models.EnvironmentVariable{{"PORT", "8080"}},
				})

				lrp.Setup = models.WrapAction(&models.DownloadAction{
					From: fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "lrp.zip"),
					To:   ".",
					User: "vcap",
				})
			})

			Context("default networking", func() {
				It("rejects outbound tcp traffic", func() {
					Eventually(func() string {
						bytes, statusCode, err := helpers.ResponseBodyAndStatusCodeFromHost(componentMaker.Addresses.Router, helpers.DefaultHost)
						if err != nil {
							return err.Error()
						}
						if statusCode != http.StatusOK {
							return strconv.Itoa(statusCode)
						}

						return string(bytes)
					}).Should(Equal("28"))
				})
			})

			Context("with appropriate security group setting", func() {
				BeforeEach(func() {
					lrp.EgressRules = []*models.SecurityGroupRule{
						{
							Protocol:     models.TCPProtocol,
							Destinations: []string{"9.0.0.0-89.255.255.255", "90.0.0.0-94.0.0.0"},
							Ports:        []uint32{80},
						},
						{
							Protocol:     models.UDPProtocol,
							Destinations: []string{"0.0.0.0/0"},
							PortRange: &models.PortRange{
								Start: 53,
								End:   53,
							},
						},
					}
				})

				It("allows outbound tcp traffic", func() {
					Eventually(func() string {
						bytes, statusCode, err := helpers.ResponseBodyAndStatusCodeFromHost(componentMaker.Addresses.Router, helpers.DefaultHost)
						if err != nil {
							return err.Error()
						}
						if statusCode != http.StatusOK {
							return strconv.Itoa(statusCode)
						}

						return string(bytes)
					}).Should(Equal("0"))
				})
			})
		})

		Context("Unsupported preloaded rootfs is requested", func() {
			BeforeEach(func() {
				lrp = helpers.LRPCreateRequestWithRootFS(processGuid, helpers.BogusPreloadedRootFS)
			})

			It("fails and sets a placement error", func() {
				lrpFunc := func() string {
					lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
					Expect(err).NotTo(HaveOccurred())
					if len(lrps) == 0 {
						return ""
					}

					return lrps[0].PlacementError
				}

				Eventually(lrpFunc).Should(Equal(diego_errors.CELL_MISMATCH_MESSAGE))
			})
		})

		Context("Unsupported arbitrary rootfs is requested", func() {
			BeforeEach(func() {
				lrp = helpers.LRPCreateRequestWithRootFS(processGuid, "socker://hello")
			})

			It("fails and sets a placement error", func() {
				lrpFunc := func() string {
					lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
					Expect(err).NotTo(HaveOccurred())
					if len(lrps) == 0 {
						return ""
					}

					return lrps[0].PlacementError
				}

				Eventually(lrpFunc).Should(Equal(diego_errors.CELL_MISMATCH_MESSAGE))
			})
		})

		Context("Supported arbitrary rootfs scheme (viz., docker) is requested", func() {
			BeforeEach(func() {
				// docker is supported
				lrp = helpers.DockerLRPCreateRequest(processGuid)
			})

			It("runs", func() {
				Eventually(func() []receptor.ActualLRPResponse {
					lrps, err := receptorClient.ActualLRPsByProcessGuid(processGuid)
					Expect(err).NotTo(HaveOccurred())
					return lrps
				}).Should(HaveLen(1))

				poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost)
				Eventually(poller).Should(ConsistOf([]string{"0"}))
			})
		})
	})
})

var _ = Describe("Crashing LRPs", func() {
	var (
		processGuid string
		runtime     ifrit.Process
	)

	BeforeEach(func() {
		fileServer, _ := componentMaker.FileServer()

		processGuid = helpers.GenerateGuid()

		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"router", componentMaker.Router()},
			{"file-server", fileServer},
			{"rep", componentMaker.Rep()},
			{"converger", componentMaker.Converger(
				"-convergeRepeatInterval", "1s",
			)},
			{"auctioneer", componentMaker.Auctioneer()},
			{"route-emitter", componentMaker.RouteEmitter()},
		}))
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime)
	})

	Describe("crashing apps", func() {
		Context("when an app flaps", func() {
			BeforeEach(func() {
				lrp := helpers.CrashingLRPCreateRequest(processGuid)

				err := receptorClient.CreateDesiredLRP(lrp)
				Expect(err).NotTo(HaveOccurred())
			})

			It("imediately restarts the app 3 times", func() {
				crashCount := func() int {
					actual, err := receptorClient.ActualLRPByProcessGuidAndIndex(processGuid, 0)
					Expect(err).NotTo(HaveOccurred())
					return actual.CrashCount
				}
				// the receptor immediately starts it 3 times
				Eventually(crashCount).Should(Equal(3))
				// then exponential backoff kicks in
				Consistently(crashCount, 15*time.Second).Should(Equal(3))
				// eventually we cross the first backoff threshold (30 seconds)
				Eventually(crashCount, 30*time.Second).Should(Equal(4))
			})
		})
	})
})
