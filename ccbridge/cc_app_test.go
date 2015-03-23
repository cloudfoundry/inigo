package ccbridge_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/world"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	"github.com/cloudfoundry/gunk/urljoiner"

	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

var _ = Describe("AppRunner", func() {
	var appId string

	var (
		runtime ifrit.Process
		bridge  ifrit.Process
	)

	desireApp := func(guid string, routes string, instances int) (*http.Response, error) {
		desireMessage := fmt.Sprintf(
			`
						{
							"process_guid": "%s",
							"droplet_uri": "%s",
							"stack": "%s",
							"num_instances": `+strconv.Itoa(instances)+`,
							"memory_mb": 256,
							"disk_mb": 1024,
							"file_descriptors": 16384,
							"environment":[{"name":"VCAP_APPLICATION", "value":"{}"}],
							"routes": `+routes+`,
							"log_guid": "%s"
						}
						`,
			guid,
			fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "droplet.zip"),
			componentMaker.Stack,
			appId,
		)

		desireURL := urljoiner.Join("http://"+componentMaker.Addresses.NsyncListener, "v1", "apps", guid)
		request, err := http.NewRequest("PUT", desireURL, strings.NewReader(desireMessage))
		Ω(err).ShouldNot(HaveOccurred())

		request.Header.Set("Content-Type", "application/json")
		return http.DefaultClient.Do(request)
	}

	BeforeEach(func() {
		appId = factories.GenerateGuid()

		fileServer, fileServerStaticDir := componentMaker.FileServer()

		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"receptor", componentMaker.Receptor()},
			{"exec", componentMaker.Executor()},
			{"rep", componentMaker.Rep()},
			{"auctioneer", componentMaker.Auctioneer()},
			{"route-emitter", componentMaker.RouteEmitter()},
			{"converger", componentMaker.Converger()},
			{"router", componentMaker.Router()},
			{"file-server", fileServer},
		}))

		bridge = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"tps", componentMaker.TPS()},
			{"nsync-listener", componentMaker.NsyncListener()},
		}))

		archive_helper.CreateZipArchive(
			filepath.Join(fileServerStaticDir, "droplet.zip"),
			fixtures.HelloWorldIndexApp(),
		)

		helpers.Copy(
			componentMaker.Artifacts.Lifecycles[componentMaker.Stack],
			filepath.Join(fileServerStaticDir, world.LifecycleFilename),
		)
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime, bridge)
	})

	Describe("Start an application", func() {
		guid := "process-guid"

		Context("when the running message contains a start_command", func() {
			It("runs the app on the executor, registers routes, and shows that they are running via the tps", func() {
				// desire the app
				resp, err := desireApp(guid, `["route-1", "route-2"]`, 2)
				Ω(err).ShouldNot(HaveOccurred())
				Ω(resp.StatusCode).Should(Equal(http.StatusAccepted))

				// check lrp instance statuses
				Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, guid)).Should(HaveLen(2))

				//both routes should be routable
				Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "route-1")).Should(Equal(http.StatusOK))
				Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "route-2")).Should(Equal(http.StatusOK))

				//a given route should route to all three running instances
				poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-1")
				Eventually(poller).Should(Equal([]string{"0", "1"}))
			})
		})

		Context("when the start message does not include a start_command", func() {
			It("runs the app, registers a route, and shows running via tps", func() {
				// desire the app
				resp, err := desireApp(guid, `["route-1"]`, 1)
				Ω(err).ShouldNot(HaveOccurred())
				Ω(resp.StatusCode).Should(Equal(http.StatusAccepted))

				Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, guid)).Should(HaveLen(1))
				Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "route-1")).Should(Equal(http.StatusOK))

				//a given route should route to the running instance
				poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-1")
				Eventually(poller).Should(Equal([]string{"0"}))
			})
		})
	})

	Describe("Stop an index", func() {
		killIndex := func(guid string, index int) (*http.Response, error) {
			killURL := urljoiner.Join("http://"+componentMaker.Addresses.NsyncListener, "v1", "apps", guid, "index", strconv.Itoa(index))
			request, err := http.NewRequest("DELETE", killURL, nil)
			Ω(err).ShouldNot(HaveOccurred())

			return http.DefaultClient.Do(request)
		}

		Context("when there is an instance running", func() {
			guid := "process-guid"

			BeforeEach(func() {
				// desire the app
				resp, err := desireApp(guid, `["route-1"]`, 2)
				Ω(err).ShouldNot(HaveOccurred())
				Ω(resp.StatusCode).Should(Equal(http.StatusAccepted))

				// wait for intances to come up
				Eventually(runningIndexPoller(componentMaker.Addresses.TPS, guid)).Should(ConsistOf(0, 1))
			})

			It("stops the app on the desired index, and then eventually starts it back up", func() {
				resp, err := killIndex(guid, 0)
				Ω(err).ShouldNot(HaveOccurred())
				Ω(resp.StatusCode).Should(Equal(http.StatusAccepted))

				// wait for stop to take effect
				Eventually(runningIndexPoller(componentMaker.Addresses.TPS, guid)).Should(ConsistOf(1))

				// wait for system to re-converge on desired state
				Eventually(runningIndexPoller(componentMaker.Addresses.TPS, guid)).Should(ConsistOf(0, 1))
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
