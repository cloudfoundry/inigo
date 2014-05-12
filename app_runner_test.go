package inigo_test

import (
	"fmt"

	"github.com/cloudfoundry-incubator/inigo/loggredile"

	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	zip_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
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

			outputGuid = factories.GenerateGuid()

			//make and upload a droplet
			var dropletFiles = []zip_helper.ArchiveFile{
				{
					Name: "app/run",
					Body: `#!/bin/bash
          echo hello world
          `,
				},
			}

			zip_helper.CreateZipArchive("/tmp/simple-echo-droplet.zip", dropletFiles)
			inigo_server.UploadFile("simple-echo-droplet.zip", "/tmp/simple-echo-droplet.zip")
		})

		JustBeforeEach(func() {
			runningMessage = []byte(
				fmt.Sprintf(
					`{
            "app_id": "%s",
            "app_version": "%s",
            "droplet_uri": "%s",
            "start_command": "./run"
          }`,
					appId,
					appVersion,
					inigo_server.DownloadUrl("simple-echo-droplet.zip"),
				),
			)
		})

		Context("with the app manager is running", func() {
			BeforeEach(func() {
				suiteContext.AppManagerRunner.Start()
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
			})
		})
	})
})
