package inigo_test

import (
	"fmt"
	"github.com/cloudfoundry-incubator/inigo/loggredile"
	"github.com/cloudfoundry/loggregatorlib/logmessage"

	"github.com/cloudfoundry-incubator/inigo/inigolistener"
	"github.com/cloudfoundry-incubator/inigo/stager_runner"
	"github.com/cloudfoundry-incubator/inigo/zipper"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	"github.com/cloudfoundry/yagnats"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Stager", func() {
	var otherStagerRunner *stager_runner.StagerRunner

	BeforeEach(func() {
		otherStagerRunner = stager_runner.New(
			stagerPath,
			etcdRunner.NodeURLS(),
			[]string{fmt.Sprintf("127.0.0.1:%d", natsPort)},
		)
	})

	Context("when unable to find an appropriate compiler", func() {
		BeforeEach(func() {
			executorRunner.Start()
			stagerRunner.Start()
		})

		It("returns an error", func() {
			receivedMessages := make(chan *yagnats.Message)
			natsRunner.MessageBus.Subscribe("compiler-stagers-test", func(message *yagnats.Message) {
				receivedMessages <- message
			})

			natsRunner.MessageBus.PublishWithReplyTo(
				"diego.staging.start",
				"compiler-stagers-test",
				[]byte(`{"app_id": "some-app-guid", "task_id": "some-task-id", "stack": "no-compiler"}`))

			var receivedMessage *yagnats.Message
			Eventually(receivedMessages, 2.0).Should(Receive(&receivedMessage))
			Ω(receivedMessage.Payload).Should(ContainSubstring("No compiler defined for requested stack"))
			Consistently(receivedMessages, 2.0).ShouldNot(Receive())
		})
	})

	Describe("Staging", func() {
		var compilerGuid string
		var appGuid string
		var adminBuildpackGuid string
		var outputGuid string
		var stagingMessage []byte
		var compilerExitStatus int

		BeforeEach(func() {
			fileServerRunner.Start()
			executorRunner.Start()

			compilerGuid = factories.GenerateGuid()
			appGuid = factories.GenerateGuid()
			adminBuildpackGuid = factories.GenerateGuid()
			outputGuid = factories.GenerateGuid()
			compilerExitStatus = 0

			//make and provide a compiler via the file-server
			var compilerFiles = []zipper.ZipFile{
				{"run", fmt.Sprintf(`#!/bin/bash

set -e

# use an environment variable provided for staging
echo $SOME_STAGING_ENV

# tell the listener we're here
%s

# tell the listener the app's bits are here
$APP_DIR/run

# tell the listener the buildpacks are here
$BUILDPACKS_DIR/test/run

# write a staging result
mkdir -p $RESULT_DIR
echo '%s' > $RESULT_DIR/result.json

# create a tarball

mkdir -p $OUTPUT_DIR
echo "the-droplet" > $OUTPUT_DIR/droplet.tgz

# inject success/failure
exit $COMPILER_EXIT_STATUS
`,
					inigolistener.CurlCommand(compilerGuid),
					`{"detected_buildpack":"Test Buildpack"}`,
				)},
			}
			zipper.CreateZipFile("/tmp/compiler.zip", compilerFiles)
			fileServerRunner.ServeFile("compiler.zip", "/tmp/compiler.zip")

			//make and upload an app
			var appFiles = []zipper.ZipFile{
				{"run", inigolistener.CurlCommand(appGuid)},
			}
			zipper.CreateZipFile("/tmp/app.zip", appFiles)
			inigolistener.UploadFile("app.zip", "/tmp/app.zip")

			//make and upload a buildpack
			var adminBuildpackFiles = []zipper.ZipFile{
				{"run", inigolistener.CurlCommand(adminBuildpackGuid)},
			}
			zipper.CreateZipFile("/tmp/admin_buildpack.zip", adminBuildpackFiles)
			inigolistener.UploadFile("admin_buildpack.zip", "/tmp/admin_buildpack.zip")

		})

		JustBeforeEach(func() {
			stagingMessage = []byte(
				fmt.Sprintf(
					`{
						"app_id": "some-app-guid",
						"task_id": "some-task-id",
						"stack": "default",
						"download_uri": "%s",
						"upload_uri": "%s",
						"admin_buildpacks" : [{ "key": "test", "url": "%s" }],
						"environment": [["SOME_STAGING_ENV", "%s"], ["COMPILER_EXIT_STATUS", "%d"]]
					}`,
					inigolistener.DownloadUrl("app.zip"),
					inigolistener.UploadUrl("droplet.tgz"),
					inigolistener.DownloadUrl("admin_buildpack.zip"),
					outputGuid,
					compilerExitStatus,
				),
			)
		})

		Context("with one stager running", func() {
			BeforeEach(func() {
				stagerRunner.Start("--compilers", `{"default":"compiler.zip"}`)
			})

			It("runs the compiler on the executor with the correct environment variables, bits and log tag, and responds with the detected buildpack", func() {
				payloads := make(chan []byte)

				natsRunner.MessageBus.Subscribe("stager-test", func(msg *yagnats.Message) {
					payloads <- msg.Payload
				})

				messages, stop := loggredile.StreamMessages(
					loggregatorRunner.Config.OutgoingPort,
					"/tail/?app=some-app-guid",
				)
				defer close(stop)

				err := natsRunner.MessageBus.PublishWithReplyTo(
					"diego.staging.start",
					"stager-test",
					stagingMessage,
				)
				Ω(err).ShouldNot(HaveOccurred())
				Eventually(inigolistener.ReportingGuids, 10.0).Should(ContainElement(compilerGuid))
				Eventually(inigolistener.ReportingGuids, 5.0).Should(ContainElement(appGuid))
				Eventually(inigolistener.ReportingGuids, 5.0).Should(ContainElement(adminBuildpackGuid))

				var message *logmessage.LogMessage
				Eventually(messages, 5.0).Should(Receive(&message))
				Ω(message.GetSourceName()).To(Equal("STG"))
				Ω(string(message.GetMessage())).To(Equal(outputGuid))

				var payload []byte
				Eventually(payloads, 5.0).Should(Receive(&payload))
				Ω(string(payload)).Should(Equal(`{"detected_buildpack":"Test Buildpack"}`))

				Ω(inigolistener.DownloadFileString("droplet.tgz")).Should(Equal("the-droplet\n"))
			})

			Context("when compilation fails", func() {
				BeforeEach(func() {
					compilerExitStatus = 42
				})

				It("responds with the error, and no detected buildpack present", func() {
					payloads := make(chan []byte)

					natsRunner.MessageBus.Subscribe("stager-test", func(msg *yagnats.Message) {
						payloads <- msg.Payload
					})

					messages, stop := loggredile.StreamMessages(
						loggregatorRunner.Config.OutgoingPort,
						"/tail/?app=some-app-guid",
					)
					defer close(stop)

					err := natsRunner.MessageBus.PublishWithReplyTo(
						"diego.staging.start",
						"stager-test",
						stagingMessage,
					)
					Ω(err).ShouldNot(HaveOccurred())
					Eventually(inigolistener.ReportingGuids, 10.0).Should(ContainElement(compilerGuid))
					Eventually(inigolistener.ReportingGuids, 5.0).Should(ContainElement(appGuid))
					Eventually(inigolistener.ReportingGuids, 5.0).Should(ContainElement(adminBuildpackGuid))

					var message *logmessage.LogMessage
					Eventually(messages, 5.0).Should(Receive(&message))
					Ω(message.GetSourceName()).To(Equal("STG"))
					Ω(string(message.GetMessage())).To(Equal(outputGuid))

					var payload []byte
					Eventually(payloads, 5.0).Should(Receive(&payload))
					Ω(string(payload)).Should(Equal(`{"error":"Process returned with exit value: 42"}`))
				})
			})
		})

		Context("with two stagers running", func() {
			BeforeEach(func() {
				stagerRunner.Start("--compilers", `{"default":"compiler.zip"}`)
				otherStagerRunner.Start("--compilers", `{"default":"compiler.zip"}`)
			})

			AfterEach(func() {
				otherStagerRunner.Stop()
			})

			It("only one returns a staging completed response", func() {
				received := make(chan bool)
				natsRunner.MessageBus.Subscribe("two-stagers-test", func(message *yagnats.Message) {
					received <- true
				})

				natsRunner.MessageBus.PublishWithReplyTo(
					"diego.staging.start",
					"two-stagers-test",
					stagingMessage,
				)

				Eventually(received, 10.0).Should(Receive())
				Consistently(received, 2.0).ShouldNot(Receive())
			})
		})
	})
})
