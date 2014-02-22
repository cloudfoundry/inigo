package inigo_test

import (
	"fmt"
	"github.com/cloudfoundry-incubator/inigo/loggredile"
	"strings"

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
			errorMessages := 0
			natsRunner.MessageBus.Subscribe("compiler-stagers-test", func(message *yagnats.Message) {
				if strings.Contains(string(message.Payload), "error") {
					errorMessages++
				}
			})

			natsRunner.MessageBus.PublishWithReplyTo(
				"diego.staging.start",
				"compiler-stagers-test",
				[]byte(`{"app_id": "some-app-guid", "task_id": "some-task-id", "stack": "no-compiler"}`))

			Eventually(func() int {
				return errorMessages
			}, 2.0).Should(Equal(1))

			Consistently(func() int {
				return errorMessages
			}, 2.0).Should(Equal(1))
		})
	})

	Describe("Staging", func() {
		var compilerGuid string
		var appGuid string
		var adminBuildpackGuid string
		var outputGuid string
		var stagingMessage []byte

		BeforeEach(func() {
			compilerGuid = factories.GenerateGuid()
			appGuid = factories.GenerateGuid()
			adminBuildpackGuid = factories.GenerateGuid()
			outputGuid = factories.GenerateGuid()

			//make and upload a compiler
			var compilerFiles = []zipper.ZipFile{
				{"run", fmt.Sprintf(`echo $FOO && %s && $APP_DIR/run && $BUILDPACKS_DIR/test/run`,
					inigolistener.CurlCommand(compilerGuid),
				)},
			}
			zipper.CreateZipFile("/tmp/compiler.zip", compilerFiles)
			inigolistener.UploadFile("compiler.zip", "/tmp/compiler.zip")

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

			executorRunner.Start()

			stagingMessage = []byte(
				fmt.Sprintf(
					`{
						"app_id": "some-app-guid",
						"task_id": "some-task-id",
						"stack": "default",
						"download_uri": "%s",
						"admin_buildpacks" : [{ "key": "test", "url": "%s" }],
						"environment": [["FOO", "%s"]]
					}`,
					inigolistener.DownloadUrl("app.zip"),
					inigolistener.DownloadUrl("admin_buildpack.zip"),
					outputGuid,
				),
			)
		})

		Context("with one stager running", func() {
			BeforeEach(func() {
				stagerRunner.Start("--compilers", fmt.Sprintf(`{"default":"%s"}`, inigolistener.DownloadUrl("compiler.zip")))
			})

			It("runs the compiler on executor with the correct environment variables, bits and log tag", func() {
				messages, stop := loggredile.StreamMessages(
					loggregatorRunner.Config.OutgoingPort,
					"/tail/?app=some-app-guid",
				)

				err := natsRunner.MessageBus.PublishWithReplyTo(
					"diego.staging.start",
					"stager-test",
					stagingMessage,
				)
				Ω(err).ShouldNot(HaveOccurred())
				Eventually(inigolistener.ReportingGuids, 5.0).Should(ContainElement(compilerGuid))
				Eventually(inigolistener.ReportingGuids, 5.0).Should(ContainElement(appGuid))
				Eventually(inigolistener.ReportingGuids, 5.0).Should(ContainElement(adminBuildpackGuid))

				message := <-messages
				Ω(message.GetSourceName()).To(Equal("STG"))
				Ω(string(message.GetMessage())).To(Equal(outputGuid))

				close(stop)
			})
		})

		Context("with two stagers running", func() {
			BeforeEach(func() {
				stagerRunner.Start("--compilers", fmt.Sprintf(`{"default":"%s"}`, inigolistener.DownloadUrl("compiler.zip")))
				otherStagerRunner.Start("--compilers", fmt.Sprintf(`{"default":"%s"}`, inigolistener.DownloadUrl("compiler.zip")))
			})

			AfterEach(func() {
				otherStagerRunner.Stop()
			})

			It("only one returns a staging completed response", func() {
				messages := 0
				natsRunner.MessageBus.Subscribe("two-stagers-test", func(message *yagnats.Message) {
					messages++
				})

				natsRunner.MessageBus.PublishWithReplyTo(
					"diego.staging.start",
					"two-stagers-test",
					stagingMessage,
				)

				Eventually(func() int {
					return messages
				}, 5.0).Should(Equal(1))

				Consistently(func() int {
					return messages
				}, 2.0).Should(Equal(1))
			})
		})
	})
})
