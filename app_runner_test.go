package inigo_test

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"syscall"

	"github.com/cloudfoundry-incubator/garden/warden"
	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/loggredile"
	"github.com/cloudfoundry-incubator/inigo/world"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"

	"github.com/cloudfoundry/yagnats"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"

	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

var _ = Describe("AppRunner", func() {
	var appId string

	var wardenClient warden.Client

	var natsClient yagnats.NATSClient

	var fileServerStaticDir string

	var plumbing ifrit.Process
	var runtime ifrit.Process

	BeforeEach(func() {
		appId = factories.GenerateGuid()

		wardenLinux := componentMaker.WardenLinux()
		wardenClient = wardenLinux.NewClient()

		fileServer, dir := componentMaker.FileServer()
		fileServerStaticDir = dir

		natsClient = yagnats.NewClient()

		plumbing = grouper.EnvokeGroup(grouper.RunGroup{
			"etcd":         componentMaker.Etcd(),
			"nats":         componentMaker.NATS(),
			"warden-linux": wardenLinux,
		})

		runtime = grouper.EnvokeGroup(grouper.RunGroup{
			"cc":             componentMaker.FakeCC(),
			"tps":            componentMaker.TPS(),
			"nsync-listener": componentMaker.NsyncListener(),
			"exec":           componentMaker.Executor(),
			"rep":            componentMaker.Rep(),
			"file-server":    fileServer,
			"auctioneer":     componentMaker.Auctioneer(),
			"route-emitter":  componentMaker.RouteEmitter(),
			"converger":      componentMaker.Converger(),
			"router":         componentMaker.Router(),
			"loggregator":    componentMaker.Loggregator(),
		})

		err := natsClient.Connect(&yagnats.ConnectionInfo{
			Addr: componentMaker.Addresses.NATS,
		})
		Ω(err).ShouldNot(HaveOccurred())

		inigo_server.Start(wardenClient)
	})

	AfterEach(func() {
		inigo_server.Stop(wardenClient)

		runtime.Signal(syscall.SIGKILL)
		Eventually(runtime.Wait(), DEFAULT_EVENTUALLY_TIMEOUT).Should(Receive())

		plumbing.Signal(syscall.SIGKILL)
		Eventually(plumbing.Wait(), DEFAULT_EVENTUALLY_TIMEOUT).Should(Receive())
	})

	Describe("Running", func() {
		var runningMessage []byte

		BeforeEach(func() {
			archive_helper.CreateZipArchive("/tmp/simple-echo-droplet.zip", fixtures.HelloWorldIndexApp())
			inigo_server.UploadFile("simple-echo-droplet.zip", "/tmp/simple-echo-droplet.zip")

			cp(
				componentMaker.Artifacts.Circuses[componentMaker.Stack],
				filepath.Join(fileServerStaticDir, world.CircusZipFilename),
			)
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
					componentMaker.Stack,
					appId,
				),
			)
		})

		It("runs the app on the executor, registers routes, and shows that they are running via the tps", func() {
			//stream logs
			logOutput := gbytes.NewBuffer()

			stop := loggredile.StreamIntoGBuffer(
				componentMaker.Addresses.LoggregatorOut,
				fmt.Sprintf("/tail/?app=%s", appId),
				"App",
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
			Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, "process-guid"), DEFAULT_EVENTUALLY_TIMEOUT, 0.5).Should(HaveLen(3))

			//both routes should be routable
			Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "route-1"), DEFAULT_EVENTUALLY_TIMEOUT, 0.5).Should(Equal(http.StatusOK))
			Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "route-2"), DEFAULT_EVENTUALLY_TIMEOUT, 0.5).Should(Equal(http.StatusOK))

			//a given route should route to all three runninginstances
			Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "route-1"), DEFAULT_EVENTUALLY_TIMEOUT, 0.5).Should(Equal(http.StatusOK))

			poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-1")
			Eventually(poller, DEFAULT_EVENTUALLY_TIMEOUT, 1).Should(Equal([]string{"0", "1", "2"}))
		})
	})
})

func cp(sourceFilePath, destinationPath string) {
	data, err := ioutil.ReadFile(sourceFilePath)
	Ω(err).ShouldNot(HaveOccurred())

	ioutil.WriteFile(destinationPath, data, 0644)
}
