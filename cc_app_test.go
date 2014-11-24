package inigo_test

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/loggredile"
	"github.com/cloudfoundry-incubator/inigo/world"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"

	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

var _ = Describe("AppRunner", func() {
	var appId string

	var runtime ifrit.Process

	BeforeEach(func() {
		appId = factories.GenerateGuid()

		fileServer, fileServerStaticDir := componentMaker.FileServer()

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

		cp(
			componentMaker.Artifacts.DockerCircus,
			filepath.Join(fileServerStaticDir, world.DockerCircusFilename),
		)
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime)
	})

	Describe("Running", func() {
		Context("when the running message contains a start_command", func() {
			It("runs the app on the executor, registers routes, and shows that they are running via the tps", func() {
				runningMessage := []byte(
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
						fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "droplet.zip"),
						componentMaker.Stack,
						appId,
					),
				)

				//stream logs
				logOutput := gbytes.NewBuffer()

				stop := loggredile.StreamIntoGBuffer(
					componentMaker.Addresses.LoggregatorOut,
					fmt.Sprintf("/tail/?app=%s", appId),
					"APP",
					logOutput,
					logOutput,
				)
				defer close(stop)

				// publish the app run message
				err := natsClient.Publish("diego.desire.app", runningMessage)
				Ω(err).ShouldNot(HaveOccurred())

				// Assert the user saw reasonable output
				Eventually(logOutput.Contents).Should(ContainSubstring("Hello World from index '0'"))
				Eventually(logOutput.Contents).Should(ContainSubstring("Hello World from index '1'"))
				Eventually(logOutput.Contents).Should(ContainSubstring("Hello World from index '2'"))

				// check lrp instance statuses
				Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, "process-guid")).Should(HaveLen(3))

				//both routes should be routable
				Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "route-1")).Should(Equal(http.StatusOK))
				Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "route-2")).Should(Equal(http.StatusOK))

				//a given route should route to all three running instances
				poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-1")
				Eventually(poller).Should(Equal([]string{"0", "1", "2"}))
			})
		})

		Context("when the start message does not include a start_command", func() {
			It("runs the app, registers a route, and shows running via tps", func() {
				runningMessage := []byte(
					fmt.Sprintf(
						`
						{
			        "process_guid": "process-guid",
			        "droplet_uri": "%s",
				      "stack": "%s",
			        "num_instances": 1,
			        "environment":[{"name":"VCAP_APPLICATION", "value":"{}"}],
			        "routes": ["route-1"],
			        "log_guid": "%s"
			      }
			    `,
						fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "droplet.zip"),
						componentMaker.Stack,
						appId,
					),
				)

				//stream logs
				logOutput := gbytes.NewBuffer()

				stop := loggredile.StreamIntoGBuffer(
					componentMaker.Addresses.LoggregatorOut,
					fmt.Sprintf("/tail/?app=%s", appId),
					"APP",
					logOutput,
					logOutput,
				)
				defer close(stop)

				// publish the app run message
				err := natsClient.Publish("diego.desire.app", runningMessage)
				Ω(err).ShouldNot(HaveOccurred())

				// Assert the user saw reasonable output
				Eventually(logOutput.Contents).Should(ContainSubstring("Hello World from index '0'"))

				Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, "process-guid")).Should(HaveLen(1))
				Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "route-1")).Should(Equal(http.StatusOK))

				By("sending a stop index message")
			})
		})

		It("runs docker apps", func() {
			runningMessage := []byte(
				fmt.Sprintf(
					`
           {
             "process_guid": "process-guid",
             "stack": "%s",
             "docker_image": "cloudfoundry/inigodockertest",
             "start_command": "/dockerapp",
             "num_instances": 2,
             "environment":[{"name":"VCAP_APPLICATION", "value":"{}"}],
             "routes": ["route-1", "route-2"],
             "log_guid": "%s"
           }
         `,
					componentMaker.Stack,
					appId,
				),
			)

			// publish the app run message
			err := natsClient.Publish("diego.docker.desire.app", runningMessage)
			Ω(err).ShouldNot(HaveOccurred())

			// check lrp instance statuses
			Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, "process-guid"), DOCKER_PULL_ESTIMATE).Should(HaveLen(2))

			//both routes should be routable
			Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "route-1")).Should(Equal(http.StatusOK))
			Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "route-2")).Should(Equal(http.StatusOK))

			//a given route should route to all running instances
			poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-1")
			Eventually(poller).Should(Equal([]string{"0", "1"}))
		})
	})

	Describe("Stop Index", func() {
		Context("when there is an instance running", func() {
			BeforeEach(func() {
				runningMessage := []byte(
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
						fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "droplet.zip"),
						componentMaker.Stack,
						appId,
					),
				)

				// publish the app run message
				err := natsClient.Publish("diego.desire.app", runningMessage)
				Ω(err).ShouldNot(HaveOccurred())

				// wait for intances to come up
				Eventually(runningIndexPoller(componentMaker.Addresses.TPS, "process-guid")).Should(ConsistOf(0, 1, 2))
			})

			It("stops the app on the desired index, and then eventually starts it back up", func() {
				stopMessage := []byte(`{"process_guid": "process-guid", "index": 1}`)
				err := natsClient.Publish("diego.stop.index", stopMessage)
				Ω(err).ShouldNot(HaveOccurred())

				// wait for stop to take effect
				Eventually(runningIndexPoller(componentMaker.Addresses.TPS, "process-guid")).Should(ConsistOf(0, 2))

				// wait for system to re-converge on desired state
				Eventually(runningIndexPoller(componentMaker.Addresses.TPS, "process-guid")).Should(ConsistOf(0, 1, 2))
			})
		})
	})
})

func runningIndexPoller(tpsAddr string, guid string) func() []int {
	return func() []int {
		indexes := []int{}
		for _, instance := range helpers.RunningLRPInstances(tpsAddr, guid) {
			indexes = append(indexes, int(instance.Index))
		}
		return indexes
	}
}

func cp(sourceFilePath, destinationPath string) {
	data, err := ioutil.ReadFile(sourceFilePath)
	Ω(err).ShouldNot(HaveOccurred())

	ioutil.WriteFile(destinationPath, data, 0644)
}
