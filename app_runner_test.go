package inigo_test

import (
	"fmt"
	"net/http"
	"syscall"
	"time"

	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/loggredile"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"

	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

var _ = Describe("AppRunner", func() {
	var appId = "simple-echo-app"

	var processGroup ifrit.Process

	var tpsAddr string

	BeforeEach(func() {
		suiteContext.FileServerRunner.Start()

		processes := grouper.RunGroup{
			"tps":            suiteContext.TPSRunner,
			"nsync-listener": suiteContext.NsyncListenerRunner,
		}

		processGroup = ifrit.Envoke(processes)

		tpsAddr = fmt.Sprintf("http://%s", suiteContext.TPSAddress)
	})

	AfterEach(func() {
		processGroup.Signal(syscall.SIGKILL)
		Eventually(processGroup.Wait()).Should(Receive())
	})

	Describe("Running", func() {
		var runningMessage []byte

		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
			suiteContext.RepRunner.Start()
			suiteContext.AuctioneerRunner.Start(AUCTION_MAX_ROUNDS)
			suiteContext.ConvergerRunner.Start(CONVERGE_REPEAT_INTERVAL, 30*time.Second, 5*time.Minute, PENDING_AUCTION_KICK_THRESHOLD, CLAIMED_AUCTION_REAP_THRESHOLD)
			suiteContext.RouteEmitterRunner.Start()
			suiteContext.RouterRunner.Start()

			archive_helper.CreateZipArchive("/tmp/simple-echo-droplet.zip", fixtures.HelloWorldIndexApp())
			inigo_server.UploadFile("simple-echo-droplet.zip", "/tmp/simple-echo-droplet.zip")

			suiteContext.FileServerRunner.ServeFile("some-lifecycle-bundle.tgz", suiteContext.SharedContext.CircusZipPath)
		})

		JustBeforeEach(func() {
			runningMessage = []byte(
				fmt.Sprintf(
					`
						{
			        "process_guid": "process-guid",
			        "droplet_uri": "%s",
				      "stack": "%s",
			        "start_command": "./run",
			        "num_instances": 3,
			        "environment":[{"name":"VCAP_APPLICATION", "value":"{}"}],
			        "routes": ["route-1", "route-2"],
			        "log_guid": "%s"
			      }
			    `,
					inigo_server.DownloadUrl("simple-echo-droplet.zip"),
					suiteContext.RepStack,
					appId,
				),
			)
		})

		It("runs the app on the executor, registers routes, and shows that they are running via the tps", func() {
			//stream logs
			logOutput := gbytes.NewBuffer()

			stop := loggredile.StreamIntoGBuffer(
				suiteContext.LoggregatorRunner.Config.OutgoingPort,
				fmt.Sprintf("/tail/?app=%s", appId),
				"App",
				logOutput,
				logOutput,
			)
			defer close(stop)

			// publish the app run message
			err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", runningMessage)
			Ω(err).ShouldNot(HaveOccurred())

			// Assert the user saw reasonable output
			Eventually(logOutput.Contents).Should(ContainSubstring("Hello World from index '0'"))
			Eventually(logOutput.Contents).Should(ContainSubstring("Hello World from index '1'"))
			Eventually(logOutput.Contents).Should(ContainSubstring("Hello World from index '2'"))

			// check lrp instance statuses
			Eventually(helpers.RunningLRPInstancesPoller(tpsAddr, "process-guid"), DEFAULT_EVENTUALLY_TIMEOUT, 0.5).Should(HaveLen(3))

			//both routes should be routable
			Eventually(helpers.ResponseCodeFromHostPoller(suiteContext.RouterRunner.Addr(), "route-1"), DEFAULT_EVENTUALLY_TIMEOUT, 0.5).Should(Equal(http.StatusOK))
			Eventually(helpers.ResponseCodeFromHostPoller(suiteContext.RouterRunner.Addr(), "route-2"), DEFAULT_EVENTUALLY_TIMEOUT, 0.5).Should(Equal(http.StatusOK))

			//a given route should route to all three runninginstances
			Eventually(helpers.ResponseCodeFromHostPoller(suiteContext.RouterRunner.Addr(), "route-1"), DEFAULT_EVENTUALLY_TIMEOUT, 0.5).Should(Equal(http.StatusOK))

			poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-1")
			Eventually(poller, DEFAULT_EVENTUALLY_TIMEOUT, 1).Should(Equal([]string{"0", "1", "2"}))
		})
	})
})
