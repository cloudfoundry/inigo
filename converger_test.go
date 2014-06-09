package inigo_test

import (
	"fmt"
	"syscall"
	"time"

	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/loggredile"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/nu7hatch/gouuid"
	"github.com/tedsuo/ifrit"

	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

var _ = Describe("Convergence to desired state", func() {
	var desiredAppRequest models.DesireAppRequestFromCC
	var appId string
	var processGuid string

	var tpsProcess ifrit.Process
	var tpsAddr string

	var logOutput *gbytes.Buffer
	var stop chan<- bool

	CONVERGE_REPEAT_INTERVAL := time.Duration(LONG_TIMEOUT) * time.Second
	LONGER_TIMEOUT := 2 * LONG_TIMEOUT

	BeforeEach(func() {
		guid, err := uuid.NewV4()
		if err != nil {
			panic("Failed to generate App ID")
		}
		appId = guid.String()

		guid, err = uuid.NewV4()
		if err != nil {
			panic("Failed to generate Process Guid")
		}
		processGuid = guid.String()

		suiteContext.FileServerRunner.Start()
		suiteContext.ExecutorRunner.Start()
		suiteContext.RepRunner.Start()
		suiteContext.AuctioneerRunner.Start()
		suiteContext.AppManagerRunner.Start()
		suiteContext.RouteEmitterRunner.Start()
		suiteContext.RouterRunner.Start()
		suiteContext.ConvergerRunner.Start(CONVERGE_REPEAT_INTERVAL, 30*time.Second, 5*time.Minute, 30*time.Second, 300*time.Second)

		tpsProcess = ifrit.Envoke(suiteContext.TPSRunner)
		tpsAddr = fmt.Sprintf("http://127.0.0.1:%d", suiteContext.TPSPort)

		archive_helper.CreateZipArchive("/tmp/simple-echo-droplet.zip", fixtures.HelloWorldIndexApp())
		inigo_server.UploadFile("simple-echo-droplet.zip", "/tmp/simple-echo-droplet.zip")

		suiteContext.FileServerRunner.ServeFile("some-lifecycle-bundle.tgz", suiteContext.SharedContext.CircusZipPath)

		logOutput, stop = loggredile.StreamIntoGBuffer(
			suiteContext.LoggregatorRunner.Config.OutgoingPort,
			fmt.Sprintf("/tail/?app=%s", appId),
			"App",
		)

		desiredAppRequest = models.DesireAppRequestFromCC{
			ProcessGuid:  processGuid,
			DropletUri:   inigo_server.DownloadUrl("simple-echo-droplet.zip"),
			Stack:        suiteContext.RepStack,
			Environment:  []models.EnvironmentVariable{{Key: "VCAP_APPLICATION", Value: "{}"}},
			NumInstances: 1,
			Routes:       []string{"route-to-simple"},
			StartCommand: "./run",
			LogGuid:      appId,
		}

		err = suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
		Ω(err).ShouldNot(HaveOccurred())

		Eventually(helpers.RunningLRPInstancesPoller(tpsAddr, processGuid), LONGER_TIMEOUT).Should(HaveLen(1))
		poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
		Eventually(poller, LONGER_TIMEOUT, 1).Should(Equal([]string{"0"}))
	})

	AfterEach(func() {
		tpsProcess.Signal(syscall.SIGKILL)
		Eventually(tpsProcess.Wait()).Should(Receive())
		close(stop)
	})

	Describe("Executor fault tolerance", func() {
		Context("When starting a long-running process and then bouncing the executor", func() {
			FIt("Eventually brings the long-running process up", func() {
				suiteContext.ExecutorRunner.Stop()

				Eventually(helpers.RunningLRPInstancesPoller(tpsAddr, processGuid), LONGER_TIMEOUT).Should(BeEmpty())
				poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Eventually(poller, LONGER_TIMEOUT, 1).Should(BeEmpty())

				suiteContext.ExecutorRunner.Start()

				Eventually(helpers.RunningLRPInstancesPoller(tpsAddr, processGuid), LONGER_TIMEOUT).Should(HaveLen(1))
				poller = helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Eventually(poller, LONGER_TIMEOUT, 1).Should(Equal([]string{"0"}))
			})
		})
	})
})
