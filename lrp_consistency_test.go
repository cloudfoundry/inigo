package inigo_test

import (
	"fmt"
	"net/http"
	"syscall"

	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/tedsuo/ifrit"

	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

var _ = Describe("LRP Consistency", func() {
	var desiredAppRequest models.DesireAppRequestFromCC
	var appId = "simple-echo-app"

	var tpsProcess ifrit.Process
	var tpsAddr string

	BeforeEach(func() {
		suiteContext.FileServerRunner.Start()
		suiteContext.ExecutorRunner.Start()
		suiteContext.RepRunner.Start()
		suiteContext.AuctioneerRunner.Start()
		suiteContext.AppManagerRunner.Start()
		suiteContext.RouteEmitterRunner.Start()
		suiteContext.RouterRunner.Start()

		tpsProcess = ifrit.Envoke(suiteContext.TPSRunner)
		tpsAddr = fmt.Sprintf("http://127.0.0.1:%d", suiteContext.TPSPort)

		archive_helper.CreateZipArchive("/tmp/simple-echo-droplet.zip", fixtures.HelloWorldIndexApp())
		inigo_server.UploadFile("simple-echo-droplet.zip", "/tmp/simple-echo-droplet.zip")

		suiteContext.FileServerRunner.ServeFile("some-lifecycle-bundle.tgz", suiteContext.SharedContext.CircusZipPath)

		desiredAppRequest = models.DesireAppRequestFromCC{
			AppId:        appId,
			AppVersion:   "the-first-one",
			DropletUri:   inigo_server.DownloadUrl("simple-echo-droplet.zip"),
			Stack:        suiteContext.RepStack,
			Environment:  []models.EnvironmentVariable{{Key: "VCAP_APPLICATION", Value: "{}"}},
			NumInstances: 3,
			Routes:       []string{"route-to-simple"},
			StartCommand: "./run",
		}
	})

	AfterEach(func() {
		tpsProcess.Signal(syscall.SIGKILL)
		Eventually(tpsProcess.Wait()).Should(Receive())
	})

	Describe("Scaling an app up", func() {
		BeforeEach(func() {
			//start the first instance
			desiredAppRequest.NumInstances = 1

			err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(helpers.ResponseCodeFromHostPoller(suiteContext.RouterRunner.Addr(), "route-to-simple"), LONG_TIMEOUT, 0.5).Should(Equal(http.StatusOK))
		})

		It("should scale up to the correct number of apps", func() {
			desiredAppRequest.NumInstances = 3

			err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(func() interface {} {
				return helpers.LRPInstances(tpsAddr, "simple-echo-app-the-first-one")
			}, LONG_TIMEOUT).Should(HaveLen(3))

			respondingIndices := map[string]bool{}

			for i := 0; i < 500; i++ {
				body, err := helpers.ResponseBodyFromHost(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Ω(err).ShouldNot(HaveOccurred())
				respondingIndices[string(body)] = true
			}

			Ω(respondingIndices).Should(HaveLen(3))

			Ω(respondingIndices).Should(HaveKey("0"))
			Ω(respondingIndices).Should(HaveKey("1"))
			Ω(respondingIndices).Should(HaveKey("2"))
		})
	})
})
