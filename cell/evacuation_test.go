package cell_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/route-emitter/cfroutes"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	"github.com/pivotal-golang/archiver/extractor/test_helper"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

var _ = Describe("Evacuation", func() {
	var (
		runtime ifrit.Process

		repRunner  ifrit.Runner
		repProcess ifrit.Process

		processGuid string
		appId       string
	)

	BeforeEach(func() {
		processGuid = factories.GenerateGuid()
		appId = factories.GenerateGuid()

		fileServer, fileServerStaticDir := componentMaker.FileServer()
		repRunner = componentMaker.Rep("-evacuationTimeout", "30s")

		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"router", componentMaker.Router()},
			{"file-server", fileServer},
			{"executor", componentMaker.Executor()},
			{"converger", componentMaker.Converger("-convergeRepeatInterval", "1s")},
			{"auctioneer", componentMaker.Auctioneer()},
			{"route-emitter", componentMaker.RouteEmitter()},
		}))

		repProcess = ginkgomon.Invoke(repRunner)

		test_helper.CreateZipArchive(
			filepath.Join(fileServerStaticDir, "lrp.zip"),
			fixtures.HelloWorldIndexLRP(),
		)
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime, repProcess)
	})

	It("handles evacuation", func() {
		By("desiring an LRP")
		lrp := receptor.DesiredLRPCreateRequest{
			Domain:      INIGO_DOMAIN,
			ProcessGuid: processGuid,
			Instances:   1,
			Stack:       componentMaker.Stack,

			Routes: cfroutes.CFRoutes{{Port: 8080, Hostnames: []string{"lrp-route"}}}.RoutingInfo(),
			Ports:  []uint16{8080},

			Setup: &models.DownloadAction{
				From: fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "lrp.zip"),
				To:   ".",
			},

			Action: &models.RunAction{
				Path: "bash",
				Args: []string{"server.sh"},
				Env:  []models.EnvironmentVariable{{"PORT", "8080"}},
			},

			Monitor: &models.RunAction{
				Path: "true",
			},
		}
		err := receptorClient.CreateDesiredLRP(lrp)
		Ω(err).ShouldNot(HaveOccurred())

		By("running an actual LRP instance")
		Eventually(helpers.LRPStatePoller(receptorClient, processGuid, nil)).Should(Equal(receptor.ActualLRPStateRunning))
		Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "lrp-route")).Should(Equal(http.StatusOK))

		By("posting the evacuation endpoint")
		resp, err := http.Post(fmt.Sprintf("http://%s/evacuate", componentMaker.Addresses.Rep), "text/html", nil)
		Ω(err).ShouldNot(HaveOccurred())
		resp.Body.Close()
		Ω(resp.StatusCode).Should(Equal(http.StatusAccepted))

		gRunner, ok := repRunner.(*ginkgomon.Runner)
		Ω(ok).Should(BeTrue())
		Eventually(gRunner.Buffer).Should(gbytes.Say("evacuating.started"))

		By("getting the state of the actual LRP")
		Eventually(func() bool {
			response, err := receptorClient.ActualLRPByProcessGuidAndIndex(processGuid, 0)
			Ω(err).ShouldNot(HaveOccurred())
			return response.Evacuating
		}).Should(BeTrue())

		By("desiring another instance")
		newInstances := 2
		err = receptorClient.UpdateDesiredLRP(processGuid, receptor.DesiredLRPUpdateRequest{Instances: &newInstances})
		Ω(err).ShouldNot(HaveOccurred())
		Consistently(helpers.LRPInstanceStatePoller(receptorClient, processGuid, 1, nil)).Should(Equal(receptor.ActualLRPStateUnclaimed))

		By("making requests to the evacuating instance")
		Consistently(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "lrp-route")).Should(Equal(http.StatusOK))

		By("restarting the rep")
		helpers.StopProcesses(repProcess)
		repRunner = componentMaker.Rep()
		repProcess = ginkgomon.Invoke(repRunner)

		By("getting the state of the actual LRP")
		Eventually(helpers.LRPInstanceStatePoller(receptorClient, processGuid, 0, nil)).Should(Equal(receptor.ActualLRPStateRunning))
		Eventually(helpers.LRPInstanceStatePoller(receptorClient, processGuid, 1, nil)).Should(Equal(receptor.ActualLRPStateRunning))
	})
})
