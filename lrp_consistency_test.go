package inigo_test

import (
	"fmt"
	"syscall"

	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/loggredile"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/nu7hatch/gouuid"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"

	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

var _ = Describe("LRP Consistency", func() {
	var desiredAppRequest models.DesireAppRequestFromCC
	var appId string
	var processGuid string

	var processGroup ifrit.Process

	var tpsAddr string

	BeforeEach(func() {
		guid, err := uuid.NewV4()
		if err != nil {
			panic("Failed to generate AppID Guid")
		}
		appId = guid.String()

		guid, err = uuid.NewV4()
		if err != nil {
			panic("Failed to generate AppID Guid")
		}

		processGuid = guid.String()

		suiteContext.FileServerRunner.Start()
		suiteContext.ExecutorRunner.Start()
		suiteContext.RepRunner.Start()
		suiteContext.AuctioneerRunner.Start(AUCTION_MAX_ROUNDS)
		suiteContext.AppManagerRunner.Start()
		suiteContext.RouteEmitterRunner.Start()
		suiteContext.RouterRunner.Start()

		processes := grouper.RunGroup{
			"tps":            suiteContext.TPSRunner,
			"nsync-listener": suiteContext.NsyncListenerRunner,
		}

		processGroup = ifrit.Envoke(processes)

		tpsAddr = fmt.Sprintf("http://%s", suiteContext.TPSAddress)

		archive_helper.CreateZipArchive("/tmp/simple-echo-droplet.zip", fixtures.HelloWorldIndexApp())
		inigo_server.UploadFile("simple-echo-droplet.zip", "/tmp/simple-echo-droplet.zip")

		suiteContext.FileServerRunner.ServeFile("some-lifecycle-bundle.tgz", suiteContext.SharedContext.CircusZipPath)
	})

	AfterEach(func() {
		processGroup.Signal(syscall.SIGKILL)
		Eventually(processGroup.Wait()).Should(Receive())
	})

	Context("with an app running", func() {
		var logOutput *gbytes.Buffer
		var stop chan<- bool

		BeforeEach(func() {
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
				NumInstances: 2,
				Routes:       []string{"route-to-simple"},
				StartCommand: "./run",
				LogGuid:      appId,
			}

			//start the first two instances
			err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(helpers.RunningLRPInstancesPoller(tpsAddr, processGuid), 2*LONG_TIMEOUT).Should(HaveLen(2))
			poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
			Eventually(poller, LONG_TIMEOUT*2, 1).Should(Equal([]string{"0", "1"}))
		})

		AfterEach(func() {
			close(stop)
		})

		Describe("Scaling an app up", func() {
			BeforeEach(func() {
				desiredAppRequest.NumInstances = 3

				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("should scale up to the correct number of instances", func() {
				Eventually(helpers.RunningLRPInstancesPoller(tpsAddr, processGuid), LONG_TIMEOUT).Should(HaveLen(3))

				poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Eventually(poller, LONG_TIMEOUT).Should(Equal([]string{"0", "1", "2"}))
			})
		})

		Describe("Scaling an app down", func() {
			BeforeEach(func() {
				desiredAppRequest.NumInstances = 1

				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("should scale down to the correct number of instances", func() {
				Eventually(helpers.RunningLRPInstancesPoller(tpsAddr, processGuid), LONG_TIMEOUT).Should(HaveLen(1))

				poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Eventually(poller, LONG_TIMEOUT, 1).Should(Equal([]string{"0"}))
			})
		})

		Describe("Stopping an app", func() {
			BeforeEach(func() {
				desiredAppRequest.NumInstances = 0

				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("should stop all instances of the app", func() {
				Eventually(helpers.RunningLRPInstancesPoller(tpsAddr, processGuid), LONG_TIMEOUT).Should(BeEmpty())

				poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Eventually(poller, LONG_TIMEOUT).Should(BeEmpty())
			})
		})
	})
})
