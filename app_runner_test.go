package inigo_test

import (
	"fmt"
	"net/http"

	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/loggredile"

	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

const LONGER_TIMEOUT = 30.0 // "temporary" - see #72360324

var _ = Describe("AppRunner", func() {
	var appId = "simple-echo-app"
	var appVersion = "the-first-one"

	BeforeEach(func() {
		suiteContext.FileServerRunner.Start()
	})

	Describe("Running", func() {
		var runningMessage []byte

		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
			suiteContext.RepRunner.Start()
			suiteContext.AuctioneerRunner.Start()

			archive_helper.CreateZipArchive("/tmp/simple-echo-droplet.zip", fixtures.HelloWorldIndexApp())
			inigo_server.UploadFile("simple-echo-droplet.zip", "/tmp/simple-echo-droplet.zip")

			suiteContext.FileServerRunner.ServeFile("some-lifecycle-bundle.tgz", suiteContext.SharedContext.CircusZipPath)
		})

		JustBeforeEach(func() {
			runningMessage = []byte(
				fmt.Sprintf(
					`{
            "app_id": "%s",
            "app_version": "%s",
            "droplet_uri": "%s",
						"stack": "%s",
            "start_command": "./run",
            "num_instances": 3,
            "environment":[{"key":"VCAP_APPLICATION", "value":"{}"}],
            "routes": ["route-1", "route-2"]
          }`,
					appId,
					appVersion,
					inigo_server.DownloadUrl("simple-echo-droplet.zip"),
					suiteContext.RepStack,
				),
			)
		})

		Context("with the app manager running", func() {
			BeforeEach(func() {
				suiteContext.AppManagerRunner.Start()
				suiteContext.RouteEmitterRunner.Start()
				suiteContext.RouterRunner.Start()
			})

			It("runs the app on the executor and responds with the echo message", func() {
				//stream logs
				logOutput, stop := loggredile.StreamIntoGBuffer(
					suiteContext.LoggregatorRunner.Config.OutgoingPort,
					fmt.Sprintf("/tail/?app=%s", appId),
					"App",
				)
				defer close(stop)

				// publish the app run message
				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", runningMessage)
				Ω(err).ShouldNot(HaveOccurred())

				// Assert the user saw reasonable output
				Eventually(logOutput, LONGER_TIMEOUT).Should(gbytes.Say("hello world"))
				Eventually(logOutput, LONGER_TIMEOUT).Should(gbytes.Say("hello world"))
				Eventually(logOutput, LONGER_TIMEOUT).Should(gbytes.Say("hello world"))
				Ω(logOutput.Contents()).Should(ContainSubstring(`"instance_index":0`))
				Ω(logOutput.Contents()).Should(ContainSubstring(`"instance_index":1`))
				Ω(logOutput.Contents()).Should(ContainSubstring(`"instance_index":2`))
			})

			It("is routable via the router and its configured routes", func() {
				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", runningMessage)
				Ω(err).ShouldNot(HaveOccurred())

				Eventually(helpers.ResponseCodeFromHostPoller(suiteContext.RouterRunner.Addr(), "route-1"), LONGER_TIMEOUT, 0.5).Should(Equal(http.StatusOK))
				Eventually(helpers.ResponseCodeFromHostPoller(suiteContext.RouterRunner.Addr(), "route-2"), LONGER_TIMEOUT, 0.5).Should(Equal(http.StatusOK))
			})

			It("distributes requests to all instances", func() {
				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", runningMessage)
				Ω(err).ShouldNot(HaveOccurred())

				Eventually(helpers.ResponseCodeFromHostPoller(suiteContext.RouterRunner.Addr(), "route-1"), LONGER_TIMEOUT, 0.5).Should(Equal(http.StatusOK))

				respondingIndices := map[string]bool{}

				for i := 0; i < 500; i++ {
					body, err := helpers.ResponseBodyFromHost(suiteContext.RouterRunner.Addr(), "route-1")
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
})
