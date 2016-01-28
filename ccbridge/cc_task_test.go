package ccbridge_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/world"
	"github.com/cloudfoundry/gunk/urljoiner"

	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

var _ = Describe("Task Runner", func() {
	var (
		logGuid        string
		callbackServer *ghttp.Server
		runtime        ifrit.Process
		bridge         ifrit.Process
	)

	desireTask := func(guid, callbackURL string) (*http.Response, error) {
		desireMessage := fmt.Sprintf(
			`
						{
							"task_guid": "%s",
							"droplet_uri": "%s",
							"rootfs": "%s",
							"memory_mb": 256,
							"disk_mb": 1024,
							"environment":[{"name":"VCAP_APPLICATION", "value":"{}"}],
							"log_guid": "%s",
							"command": "echo jim",
							"completion_callback": "%s",
							"lifecycle": "buildpack"
						}
						`,
			guid,
			fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "droplet.zip"),
			componentMaker.DefaultStack(),
			logGuid,
			callbackURL,
		)

		desireURL := urljoiner.Join("http://"+componentMaker.Addresses.NsyncListener, "v1", "tasks")
		request, err := http.NewRequest("POST", desireURL, strings.NewReader(desireMessage))
		Expect(err).NotTo(HaveOccurred())

		request.Header.Set("Content-Type", "application/json")
		return http.DefaultClient.Do(request)
	}

	BeforeEach(func() {
		logGuid = helpers.GenerateGuid()
		fileServer, fileServerStaticDir := componentMaker.FileServer()

		callbackServer = ghttp.NewServer()
		callbackServer.AppendHandlers(
			ghttp.VerifyRequest("POST", "/the-cc-callback"),
			ghttp.VerifyBasicAuth("jim", "password"),
			ghttp.RespondWith(200, `{}`),
		)

		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"bbs", componentMaker.BBS()},
			{"rep", componentMaker.Rep()},
			{"auctioneer", componentMaker.Auctioneer()},
			{"route-emitter", componentMaker.RouteEmitter()},
			{"converger", componentMaker.Converger()},
			{"router", componentMaker.Router()},
			{"file-server", fileServer},
		}))

		bridge = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"nsync-listener", componentMaker.NsyncListener()},
		}))

		archive_helper.CreateZipArchive(
			filepath.Join(fileServerStaticDir, "droplet.zip"),
			fixtures.HelloWorldIndexApp(),
		)

		helpers.Copy(
			componentMaker.Artifacts.Lifecycles[componentMaker.DefaultStack()],
			filepath.Join(fileServerStaticDir, world.LifecycleFilename),
		)
	})

	AfterEach(func() {
		if callbackServer != nil {
			callbackServer.Close()
		}
		helpers.StopProcesses(runtime, bridge)
	})

	Describe("Start a task", func() {
		Context("when the running message contains a start_command", func() {
			It("runs the app on the executor, registers routes, and shows that they are running via the tps", func() {
				guid := helpers.GenerateGuid()
				resp, err := desireTask(guid, fmt.Sprintf("http://%s:%s@%s/the-cc-callback", "jim", "password", callbackServer.Addr()))
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

				Eventually(callbackServer.ReceivedRequests).Should(HaveLen(1))
			})
		})
	})
})
