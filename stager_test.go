package inigo_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"github.com/cloudfoundry-incubator/inigo/loggredile"
	"github.com/fraenkel/candiedyaml"
	"io"
	"io/ioutil"

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
		var outputGuid string
		var stagingMessage []byte
		var buildpackToUse string

		BeforeEach(func() {
			fileServerRunner.Start()
			executorRunner.Start()
			buildpackToUse = "admin_buildpack.zip"

			outputGuid = factories.GenerateGuid()

			fileServerRunner.ServeFile("smelter.zip", smelterZipPath)

			//make and upload an app
			var appFiles = []zipper.ZipFile{
				{"my-app", "scooby-doo"},
			}
			zipper.CreateZipFile("/tmp/app.zip", appFiles)
			inigolistener.UploadFile("app.zip", "/tmp/app.zip")

			//make and upload a buildpack
			var adminBuildpackFiles = []zipper.ZipFile{
				{"bin/detect", `#!/bin/bash
				echo My Buildpack
				`},
				{"bin/compile", `#!/bin/bash
				echo COMPILING BUILDPACK
				echo $SOME_STAGING_ENV
				touch $1/compiled
				`},
				{"bin/release", `#!/bin/bash
cat <<EOF
---
default_process_types:
  web: start-command
EOF
				`},
			}
			zipper.CreateZipFile("/tmp/admin_buildpack.zip", adminBuildpackFiles)
			inigolistener.UploadFile("admin_buildpack.zip", "/tmp/admin_buildpack.zip")

			var bustedAdminBuildpackFiles = []zipper.ZipFile{
				{"bin/detect", `#!/bin/bash]
				exit 1
				`},
				{"bin/compile", `#!/bin/bash`},
				{"bin/release", `#!/bin/bash`},
			}
			zipper.CreateZipFile("/tmp/busted_admin_buildpack.zip", bustedAdminBuildpackFiles)
			inigolistener.UploadFile("busted_admin_buildpack.zip", "/tmp/busted_admin_buildpack.zip")
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
						"admin_buildpacks" : [{ "key": "test-buildpack", "url": "%s" }],
						"environment": [["SOME_STAGING_ENV", "%s"]]
					}`,
					inigolistener.DownloadUrl("app.zip"),
					inigolistener.UploadUrl("droplet.tgz"),
					inigolistener.DownloadUrl(buildpackToUse),
					outputGuid,
				),
			)
		})

		Context("with one stager running", func() {
			BeforeEach(func() {
				stagerRunner.Start("--compilers", `{"default":"smelter.zip"}`)
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

				logOutput := ""
				go func() {
					for message := range messages {
						Ω(message.GetSourceName()).To(Equal("STG"))
						logOutput += string(message.GetMessage()) + "\n"
					}
				}()

				err := natsRunner.MessageBus.PublishWithReplyTo(
					"diego.staging.start",
					"stager-test",
					stagingMessage,
				)
				Ω(err).ShouldNot(HaveOccurred())

				var payload []byte
				Eventually(payloads, 10.0).Should(Receive(&payload))
				Ω(string(payload)).Should(Equal(`{"detected_buildpack":"My Buildpack"}`))

				Eventually(func() string {
					return logOutput
				}).Should(ContainSubstring("COMPILING BUILDPACK"))
				Ω(logOutput).Should(ContainSubstring(outputGuid))

				dropletData := []byte(inigolistener.DownloadFileString("droplet.tgz"))
				Ω(dropletData).ShouldNot(BeEmpty())

				ungzippedDropletData, err := gzip.NewReader(bytes.NewReader(dropletData))
				Ω(err).ShouldNot(HaveOccurred())

				untarredDropletData := tar.NewReader(ungzippedDropletData)
				dropletContents := map[string][]byte{}
				for {
					hdr, err := untarredDropletData.Next()
					if err == io.EOF {
						break
					}
					Ω(err).ShouldNot(HaveOccurred())

					content, err := ioutil.ReadAll(untarredDropletData)
					Ω(err).ShouldNot(HaveOccurred())

					dropletContents[hdr.Name] = content
				}

				Ω(dropletContents).Should(HaveKey("./"))
				Ω(dropletContents).Should(HaveKey("./staging_info.yml"))
				Ω(dropletContents).Should(HaveKey("./logs/"))
				Ω(dropletContents).Should(HaveKey("./tmp/"))
				Ω(dropletContents).Should(HaveKey("./app/"))
				Ω(dropletContents).Should(HaveKey("./app/my-app"))
				Ω(dropletContents).Should(HaveKey("./app/compiled"))

				Ω(string(dropletContents["./app/my-app"])).Should(Equal("scooby-doo"))

				yamlDecoder := candiedyaml.NewDecoder(bytes.NewReader(dropletContents["./staging_info.yml"]))
				stagingInfo := map[string]string{}
				err = yamlDecoder.Decode(&stagingInfo)
				Ω(err).ShouldNot(HaveOccurred())

				Ω(stagingInfo["detected_buildpack"]).Should(Equal("My Buildpack"))
				Ω(stagingInfo["start_command"]).Should(Equal("start-command"))

				Ω(dropletContents).Should(HaveLen(7))
			})

			Context("when compilation fails", func() {
				BeforeEach(func() {
					buildpackToUse = "busted_admin_buildpack.zip"
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

					logOutput := ""
					go func() {
						for message := range messages {
							Ω(message.GetSourceName()).To(Equal("STG"))
							logOutput += string(message.GetMessage()) + "\n"
						}
					}()

					err := natsRunner.MessageBus.PublishWithReplyTo(
						"diego.staging.start",
						"stager-test",
						stagingMessage,
					)
					Ω(err).ShouldNot(HaveOccurred())

					var payload []byte
					Eventually(payloads, 10.0).Should(Receive(&payload))
					Ω(string(payload)).Should(Equal(`{"error":"Process returned with exit value: 1"}`))

					Eventually(func() string {
						return logOutput
					}, 5.0).Should(ContainSubstring("no buildpack detected"))
				})
			})
		})

		Context("with two stagers running", func() {
			BeforeEach(func() {
				stagerRunner.Start("--compilers", `{"default":"smelter.zip"}`)
				otherStagerRunner.Start("--compilers", `{"default":"smelter.zip"}`)
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
