package inigo_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/loggredile"
	"github.com/cloudfoundry-incubator/inigo/world"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

var _ = Describe("LRP Consistency", func() {
	var runtime ifrit.Process

	var fileServerStaticDir string

	var appId string
	var processGuid string

	var desiredAppRequest models.DesireAppRequestFromCC

	BeforeEach(func() {
		appId = factories.GenerateGuid()

		processGuid = factories.GenerateGuid()

		fileServer, dir := componentMaker.FileServer()
		fileServerStaticDir = dir

		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"cc", componentMaker.FakeCC()},
			{"tps", componentMaker.TPS()},
			{"receptor", componentMaker.Receptor()},
			{"nsync-listener", componentMaker.NsyncListener()},
			{"exec", componentMaker.Executor()},
			{"rep", componentMaker.Rep()},
			{"file-server", fileServer},
			{"auctioneer", componentMaker.Auctioneer()},
			{"route-emitter", componentMaker.RouteEmitter()},
			{"converger", componentMaker.Converger()},
			{"router", componentMaker.Router()},
			{"loggregator", componentMaker.Loggregator()},
		}))

		archive_helper.CreateZipArchive(
			filepath.Join(fileServerStaticDir, "droplet.zip"),
			fixtures.HelloWorldIndexApp(),
		)

		cp(
			componentMaker.Artifacts.Circuses[componentMaker.Stack],
			filepath.Join(fileServerStaticDir, world.CircusFilename),
		)
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime)
	})

	Context("with an app running", func() {
		var logOutput *gbytes.Buffer
		var stop chan<- bool

		BeforeEach(func() {
			logOutput = gbytes.NewBuffer()

			stop = loggredile.StreamIntoGBuffer(
				componentMaker.Addresses.LoggregatorOut,
				fmt.Sprintf("/tail/?app=%s", appId),
				"App",
				logOutput,
				logOutput,
			)

			desiredAppRequest = models.DesireAppRequestFromCC{
				ProcessGuid:  processGuid,
				DropletUri:   fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "droplet.zip"),
				Stack:        componentMaker.Stack,
				Environment:  []models.EnvironmentVariable{{Name: "VCAP_APPLICATION", Value: "{}"}},
				NumInstances: 2,
				Routes:       []string{"route-to-simple"},
				StartCommand: "./run",
				LogGuid:      appId,
			}

			reqJSON, err := models.ToJSON(desiredAppRequest)
			Ω(err).ShouldNot(HaveOccurred())

			//start the first two instances
			err = natsClient.Publish("diego.desire.app", reqJSON)
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid)).Should(HaveLen(2))
			poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-to-simple")
			Eventually(poller).Should(Equal([]string{"0", "1"}))
		})

		AfterEach(func() {
			close(stop)
		})

		Describe("Scaling an app up", func() {
			BeforeEach(func() {
				desiredAppRequest.NumInstances = 3

				reqJSON, err := models.ToJSON(desiredAppRequest)
				Ω(err).ShouldNot(HaveOccurred())

				err = natsClient.Publish("diego.desire.app", reqJSON)
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("should scale up to the correct number of instances", func() {
				Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid)).Should(HaveLen(3))

				poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-to-simple")
				Eventually(poller).Should(Equal([]string{"0", "1", "2"}))
			})
		})

		Describe("Scaling an app down", func() {
			Measure("should scale down to the correct number of instances", func(b Benchmarker) {
				b.Time("scale down", func() {
					desiredAppRequest.NumInstances = 1

					reqJSON, err := models.ToJSON(desiredAppRequest)
					Ω(err).ShouldNot(HaveOccurred())

					err = natsClient.Publish("diego.desire.app", reqJSON)
					Ω(err).ShouldNot(HaveOccurred())

					Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid)).Should(HaveLen(1))

					poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-to-simple")
					Eventually(poller).Should(Equal([]string{"0"}))
				})

				b.Time("scale up", func() {
					desiredAppRequest.NumInstances = 3

					reqJSON, err := models.ToJSON(desiredAppRequest)
					Ω(err).ShouldNot(HaveOccurred())

					err = natsClient.Publish("diego.desire.app", reqJSON)
					Ω(err).ShouldNot(HaveOccurred())

					Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid)).Should(HaveLen(3))

					poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-to-simple")
					Eventually(poller).Should(Equal([]string{"0", "1", "2"}))
				})
			}, helpers.RepeatCount())
		})

		Describe("Stopping an app", func() {
			Measure("should stop all instances of the app", func(b Benchmarker) {
				b.Time("stop", func() {
					desiredAppRequest.NumInstances = 0

					reqJSON, err := models.ToJSON(desiredAppRequest)
					Ω(err).ShouldNot(HaveOccurred())

					err = natsClient.Publish("diego.desire.app", reqJSON)
					Ω(err).ShouldNot(HaveOccurred())

					Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid)).Should(BeEmpty())

					poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-to-simple")
					Eventually(poller).Should(BeEmpty())
				})

				b.Time("start", func() {
					desiredAppRequest.NumInstances = 2

					reqJSON, err := models.ToJSON(desiredAppRequest)
					Ω(err).ShouldNot(HaveOccurred())

					err = natsClient.Publish("diego.desire.app", reqJSON)
					Ω(err).ShouldNot(HaveOccurred())
					Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid)).Should(HaveLen(2))

					poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-to-simple")
					Eventually(poller).Should(Equal([]string{"0", "1"}))
				})
			}, helpers.RepeatCount())
		})

	})
})
