package cell_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/pivotal-golang/archiver/extractor/test_helper"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Evacuation", func() {
	var (
		runtime ifrit.Process

		cellAID           string
		cellAExecutorAddr string
		cellARepAddr      string

		cellBID           string
		cellBExecutorAddr string
		cellBRepAddr      string

		cellARepRunner *ginkgomon.Runner
		cellBRepRunner *ginkgomon.Runner

		cellA ifrit.Process
		cellB ifrit.Process

		processGuid string
		appId       string
	)

	BeforeEach(func() {
		processGuid = helpers.GenerateGuid()
		appId = helpers.GenerateGuid()

		fileServer, fileServerStaticDir := componentMaker.FileServer()

		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"router", componentMaker.Router()},
			{"file-server", fileServer},
			{"converger", componentMaker.Converger("-convergeRepeatInterval", "1s")},
			{"auctioneer", componentMaker.Auctioneer()},
			{"route-emitter", componentMaker.RouteEmitter()},
		}))

		cellAID = "cell-a"
		cellBID = "cell-b"

		cellAExecutorAddr = fmt.Sprintf("127.0.0.1:%d", 13100+GinkgoParallelNode())
		cellBExecutorAddr = fmt.Sprintf("127.0.0.1:%d", 13200+GinkgoParallelNode())

		cellARepAddr = fmt.Sprintf("0.0.0.0:%d", 14100+GinkgoParallelNode())
		cellBRepAddr = fmt.Sprintf("0.0.0.0:%d", 14200+GinkgoParallelNode())

		cellARepRunner = componentMaker.RepN(0,
			"-cellID", cellAID,
			"-executorURL", "http://"+cellAExecutorAddr,
			"-listenAddr", cellARepAddr,
			"-evacuationTimeout", "30s",
		)

		cellBRepRunner = componentMaker.RepN(1,
			"-cellID", cellBID,
			"-executorURL", "http://"+cellBExecutorAddr,
			"-listenAddr", cellBRepAddr,
			"-evacuationTimeout", "30s",
		)

		cellA = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"executor", componentMaker.Executor(
				"-containerOwnerName", cellAID+"-executor",
				"-listenAddr", cellAExecutorAddr,
			)},
			{"rep", cellARepRunner},
		}))

		cellB = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"executor", componentMaker.Executor(
				"-containerOwnerName", cellBID+"-executor",
				"-listenAddr", cellBExecutorAddr,
			)},
			{"rep", cellBRepRunner},
		}))

		test_helper.CreateZipArchive(
			filepath.Join(fileServerStaticDir, "lrp.zip"),
			fixtures.HelloWorldIndexLRP(),
		)
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime, cellA, cellB)
	})

	It("handles evacuation", func() {
		By("desiring an LRP")
		lrp := helpers.DefaultLRPCreateRequest(processGuid, "log-guid", 1)

		err := receptorClient.CreateDesiredLRP(lrp)
		Expect(err).NotTo(HaveOccurred())

		By("running an actual LRP instance")
		Eventually(helpers.LRPStatePoller(receptorClient, processGuid, nil)).Should(Equal(receptor.ActualLRPStateRunning))
		Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, helpers.DefaultHost)).Should(Equal(http.StatusOK))

		actualLRP, err := receptorClient.ActualLRPByProcessGuidAndIndex(processGuid, 0)
		Expect(err).NotTo(HaveOccurred())

		var evacuatingRepAddr string
		var evacutaingRepRunner *ginkgomon.Runner

		switch actualLRP.CellID {
		case cellAID:
			evacuatingRepAddr = cellARepAddr
			evacutaingRepRunner = cellARepRunner
		case cellBID:
			evacuatingRepAddr = cellBRepAddr
			evacutaingRepRunner = cellBRepRunner
		default:
			panic("what? who?")
		}

		By("posting the evacuation endpoint")
		resp, err := http.Post(fmt.Sprintf("http://%s/evacuate", evacuatingRepAddr), "text/html", nil)
		Expect(err).NotTo(HaveOccurred())
		resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

		By("staying routable so long as its rep is alive")
		Eventually(func() int {
			Expect(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, helpers.DefaultHost)()).To(Equal(http.StatusOK))
			return evacutaingRepRunner.ExitCode()
		}).Should(Equal(0))

		By("still being routable after the evacuated rep has exited")
		Consistently(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, helpers.DefaultHost)).Should(Equal(http.StatusOK))
	})
})
