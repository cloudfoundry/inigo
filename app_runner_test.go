package inigo_test

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"

	"github.com/cloudfoundry-incubator/inigo/loggredile"

	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

var _ = Describe("AppRunner", func() {
	var appId = "simple-echo-app"
	var appVersion = "the-first-one"

	BeforeEach(func() {
		suiteContext.FileServerRunner.Start()
	})

	Describe("Running", func() {
		var outputGuid string
		var runningMessage []byte

		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
			suiteContext.RepRunner.Start()
			suiteContext.AuctioneerRunner.Start()

			outputGuid = factories.GenerateGuid()

			//make and upload a droplet
			var dropletFiles = []archive_helper.ArchiveFile{
				{
					Name: "app/run",
					Body: `#!/bin/bash
          env
          echo hello world

					ruby <<END_MAGIC_SERVER
require 'webrick'
require 'json'

server = WEBrick::HTTPServer.new :BindAddress => "0.0.0.0", :Port => ENV['PORT']

index = JSON.parse(ENV["VCAP_APPLICATION"])["instance_index"]

server.mount_proc '/' do |req, res|
	res.body = index.to_s
  res.status = 200
end

trap('INT') {
  server.shutdown
}

server.start
END_MAGIC_SERVER
          `,
				},
			}

			archive_helper.CreateZipArchive("/tmp/simple-echo-droplet.zip", dropletFiles)
			inigo_server.UploadFile("simple-echo-droplet.zip", "/tmp/simple-echo-droplet.zip")

			var healthCheckFiles = []archive_helper.ArchiveFile{
				{
					Name: "diego-health-check",
					Body: `#!/bin/bash
					exit 0
          `,
				},
			}

			archive_helper.CreateTarGZArchive("/tmp/some-health-check.tgz", healthCheckFiles)
			suiteContext.FileServerRunner.ServeFile("some-health-check.tgz", "/tmp/some-health-check.tgz")
		})

		var responseCodeFromHost = func(host string) func() int {
			return func() int {
				request := &http.Request{
					URL: &url.URL{
						Scheme: "http",
						Host:   suiteContext.RouterRunner.Addr(),
						Path:   "/",
					},

					Host: host,
				}

				response, err := http.DefaultClient.Do(request)
				if err != nil {
					return 0
				}

				response.Body.Close()

				return response.StatusCode
			}
		}

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
				messages, stop := loggredile.StreamMessages(
					suiteContext.LoggregatorRunner.Config.OutgoingPort,
					fmt.Sprintf("/tail/?app=%s", appId),
				)
				defer close(stop)

				logOutput := gbytes.NewBuffer()
				go func() {
					for message := range messages {
						defer GinkgoRecover()

						Ω(message.GetSourceName()).To(Equal("App"))
						logOutput.Write([]byte(string(message.GetMessage()) + "\n"))
					}
				}()

				// publish the app run message
				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", runningMessage)
				Ω(err).ShouldNot(HaveOccurred())

				// Assert the user saw reasonable output
				Eventually(logOutput, LONG_TIMEOUT).Should(gbytes.Say("hello world"))
				Eventually(logOutput, LONG_TIMEOUT).Should(gbytes.Say("hello world"))
				Eventually(logOutput, LONG_TIMEOUT).Should(gbytes.Say("hello world"))
				Ω(logOutput.Contents()).Should(ContainSubstring(`"instance_index":0`))
				Ω(logOutput.Contents()).Should(ContainSubstring(`"instance_index":1`))
				Ω(logOutput.Contents()).Should(ContainSubstring(`"instance_index":2`))
			})

			It("is routable via the router and its configured routes", func() {
				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", runningMessage)
				Ω(err).ShouldNot(HaveOccurred())

				Eventually(responseCodeFromHost("route-1"), LONG_TIMEOUT).Should(Equal(http.StatusOK))
				Eventually(responseCodeFromHost("route-2"), LONG_TIMEOUT).Should(Equal(http.StatusOK))
			})

			It("distributes requests to all instances", func() {
				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", runningMessage)
				Ω(err).ShouldNot(HaveOccurred())

				Eventually(responseCodeFromHost("route-1"), LONG_TIMEOUT).Should(Equal(http.StatusOK))

				responseCounts := map[string]int{}

				for i := 0; i < 500; i++ {
					request := &http.Request{
						URL: &url.URL{
							Scheme: "http",
							Host:   suiteContext.RouterRunner.Addr(),
							Path:   "/",
						},

						Host: "route-1",
					}

					response, err := http.DefaultClient.Do(request)
					Ω(err).ShouldNot(HaveOccurred())
					Ω(response.StatusCode).Should(Equal(http.StatusOK))

					body, err := ioutil.ReadAll(response.Body)
					Ω(err).ShouldNot(HaveOccurred())

					responseCounts[string(body)]++
				}

				Ω(responseCounts).Should(HaveLen(3))

				for _, count := range responseCounts {
					Ω(count).ShouldNot(BeZero())
				}
			})
		})
	})
})
