package cell_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	archive_helper "code.cloudfoundry.org/archiver/extractor/test_helper"
	"code.cloudfoundry.org/bbs"
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/durationjson"
	"code.cloudfoundry.org/inigo/fixtures"
	"code.cloudfoundry.org/inigo/helpers"
	"code.cloudfoundry.org/lager"
	repconfig "code.cloudfoundry.org/rep/cmd/rep/config"
	routeemitterconfig "code.cloudfoundry.org/route-emitter/cmd/route-emitter/config"
	"code.cloudfoundry.org/routing-info/cfroutes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"
)

var _ = Describe("LocalRouteEmitter", func() {
	var (
		processGuid                                  string
		runtime                                      ifrit.Process
		archiveFiles                                 []archive_helper.ArchiveFile
		fileServerStaticDir                          string
		cellAID, cellBID, cellARepAddr, cellBRepAddr string
	)

	BeforeEach(func() {
		processGuid = helpers.GenerateGuid()

		var fileServer ifrit.Runner
		fileServer, fileServerStaticDir = componentMaker.FileServer()

		cellAID = "cell-a"
		cellBID = "cell-b"

		cellARepAddr = fmt.Sprintf("0.0.0.0:%d", 14200+GinkgoParallelNode())
		repA := componentMaker.RepN(1, func(config *repconfig.RepConfig) {
			config.CellID = cellAID
			config.ListenAddr = cellARepAddr
			config.EvacuationTimeout = durationjson.Duration(30 * time.Second)
		})

		cellBRepAddr = fmt.Sprintf("0.0.0.0:%d", 14400+GinkgoParallelNode())
		repB := componentMaker.RepN(2, func(config *repconfig.RepConfig) {
			config.CellID = cellBID
			config.ListenAddr = cellBRepAddr
			config.EvacuationTimeout = durationjson.Duration(30 * time.Second)
		})

		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"router", componentMaker.Router()},
			{"file-server", fileServer},
			{"rep-1", repA},
			{"rep-2", repB},
			{"auctioneer", componentMaker.Auctioneer()},
			// override syncinterval and cell id
			{"route-emitter-1", componentMaker.RouteEmitterN(1, func(config *routeemitterconfig.RouteEmitterConfig) {
				config.SyncInterval = durationjson.Duration(time.Hour)
				config.CellID = cellAID
			})},
			{"route-emitter-2", componentMaker.RouteEmitterN(2, func(config *routeemitterconfig.RouteEmitterConfig) {
				config.SyncInterval = durationjson.Duration(time.Hour)
				config.CellID = cellBID
			})},
		}))
		archiveFiles = fixtures.GoServerApp()
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime)
	})

	JustBeforeEach(func() {
		archive_helper.CreateZipArchive(
			filepath.Join(fileServerStaticDir, "lrp.zip"),
			archiveFiles,
		)
	})

	Describe("desiring", func() {
		var (
			lrp       *models.DesiredLRP
			instances int32
		)

		BeforeEach(func() {
			instances = 1
		})

		JustBeforeEach(func() {
			lrp = createDesiredLRP(processGuid)
			lrp.Instances = instances
			err := bbsClient.DesireLRP(logger, lrp)
			Expect(err).NotTo(HaveOccurred())
			Eventually(helpers.LRPStatePoller(logger, bbsClient, processGuid, nil)).Should(Equal(models.ActualLRPStateRunning))
		})

		It("eventually is accessible through the router within a second", func() {
			Eventually(
				helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, helpers.DefaultHost),
				time.Second,
				10*time.Millisecond,
			).Should(Equal(http.StatusOK))
		})

		Context("when there are 3 instances", func() {
			BeforeEach(func() {
				instances = 3
			})

			Context("and a rep start evacuating", func() {
				JustBeforeEach(func() {
					evacuateARep(
						processGuid,
						logger,
						bbsClient,
						cellAID, cellARepAddr,
						cellBID, cellBRepAddr,
					)
				})

				It("eventually should make the new lrp routable within a second", func() {
					Eventually(
						helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost),
						time.Second,
						10*time.Millisecond,
					).Should(ConsistOf([]string{"0", "1", "2"}))
				})
			})

			Context("and the app is deleted", func() {
				JustBeforeEach(func() {
					err := bbsClient.RemoveDesiredLRP(logger, processGuid)
					Expect(err).NotTo(HaveOccurred())
				})

				It("eventually is not accessible through the router within a second", func() {
					Eventually(
						helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost),
						time.Second,
						10*time.Millisecond,
					).Should(BeEmpty())
				})
			})

			Context("and the app is updated", func() {
				var (
					desiredLRPUdate *models.DesiredLRPUpdate
				)

				BeforeEach(func() {
					desiredLRPUdate = &models.DesiredLRPUpdate{}
				})

				JustBeforeEach(func() {
					err := bbsClient.UpdateDesiredLRP(logger, processGuid, desiredLRPUdate)
					Expect(err).NotTo(HaveOccurred())
				})

				Context("to scale the app down", func() {
					BeforeEach(func() {
						newInstances := int32(1)
						desiredLRPUdate.Instances = &newInstances
					})

					It("eventually extra routes are removed within a second", func() {
						Eventually(
							helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost),
							time.Second,
							10*time.Millisecond,
						).Should(ConsistOf([]string{"0"}))
					})
				})

				Context("to add new route", func() {
					BeforeEach(func() {
						routes := cfroutes.CFRoutes{{Hostnames: []string{helpers.DefaultHost, "some-other-route"}, Port: 8080}}.RoutingInfo()
						desiredLRPUdate.Routes = &routes
					})

					It("eventually is accessible using the new route within a second", func() {
						Eventually(
							helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "some-other-route"),
							time.Second,
							10*time.Millisecond,
						).Should(ConsistOf([]string{"0", "1", "2"}))
					})
				})

				Context("and all routes are deleted", func() {
					BeforeEach(func() {
						routes := cfroutes.CFRoutes{}.RoutingInfo()
						desiredLRPUdate.Routes = &routes
					})

					It("eventually not accessible using its route within a second", func() {
						Eventually(
							helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost),
							time.Second,
							10*time.Millisecond,
						).Should(BeEmpty())
					})
				})
			})
		})

		Context("when the instances count change", func() {
			var (
				newInstances int32
			)

			JustBeforeEach(func() {
				err := bbsClient.UpdateDesiredLRP(logger, processGuid, &models.DesiredLRPUpdate{
					Instances: &newInstances,
				})
				Expect(err).NotTo(HaveOccurred())
			})

			Context("to 3 instances", func() {
				BeforeEach(func() {
					newInstances = 3
				})

				JustBeforeEach(func() {
					for i := 1; i < int(newInstances); i++ {
						Eventually(helpers.LRPInstanceStatePoller(logger, bbsClient, processGuid, i, nil)).Should(Equal(models.ActualLRPStateRunning))
					}
				})

				It("eventually is accessible through the router within a second", func() {
					Eventually(
						helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost),
						time.Second,
						10*time.Millisecond,
					).Should(ConsistOf([]string{"0", "1", "2"}))
				})
			})

			Context("to 0 instances", func() {
				BeforeEach(func() {
					newInstances = 0
				})

				It("eventually is not accessible through the router within a second", func() {
					Eventually(
						helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost),
						time.Second,
						10*time.Millisecond,
					).Should(BeEmpty())
				})
			})
		})
	})
})

func createDesiredLRP(processGuid string) *models.DesiredLRP {
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
		Env:  []*models.EnvironmentVariable{{"PORT", "8080"}},
	})
	routes := cfroutes.CFRoutes{{Hostnames: []string{helpers.DefaultHost}, Port: 8080}}.RoutingInfo()
	lrp.Routes = &routes
	return lrp
}

func evacuateARep(
	processGuid string,
	logger lager.Logger,
	bbsClient bbs.InternalClient,
	cellAID, cellARepAddr string,
	cellBID, cellBRepAddr string,
) {
	By("finding rep with one instance running")
	actualLRPGroups, err := bbsClient.ActualLRPGroupsByProcessGuid(logger, processGuid)
	Expect(err).NotTo(HaveOccurred())
	Expect(actualLRPGroups).To(HaveLen(3))
	instancePerRepCount := map[string]int{}
	for _, lrpGroup := range actualLRPGroups {
		lrp, _ := lrpGroup.Resolve()
		cellID := lrp.ActualLRPInstanceKey.CellId
		instancePerRepCount[cellID]++
	}
	repWithOneInstance := ""
	for cellID, count := range instancePerRepCount {
		if count == 1 {
			repWithOneInstance = cellID
			break
		}
	}
	Expect(repWithOneInstance).NotTo(BeEmpty())

	evacuatingRepAddr := ""
	otherRepID := ""
	if repWithOneInstance == cellAID {
		evacuatingRepAddr = cellARepAddr
		otherRepID = cellBID
	} else if repWithOneInstance == cellBID {
		evacuatingRepAddr = cellBRepAddr
		otherRepID = cellAID
	} else {
		Fail(fmt.Sprintf("cell id %s doesn't match either cell-a or cell-b", repWithOneInstance))
	}

	By(fmt.Sprintf("sending evacuate request to %s", repWithOneInstance))
	resp, err := http.Post(fmt.Sprintf("http://%s/evacuate", evacuatingRepAddr), "text/html", nil)
	Expect(err).NotTo(HaveOccurred())
	resp.Body.Close()
	Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

	By("waiting for the lrp to run on the new cell")
	Eventually(func() map[string]int {
		lrps := helpers.ActiveActualLRPs(logger, bbsClient, processGuid)
		cellIDs := map[string]int{}
		for _, lrp := range lrps {
			cellIDs[lrp.CellId]++
		}
		return cellIDs
	}).Should(Equal(map[string]int{otherRepID: 3}))
}
