package inigo_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cloudfoundry-incubator/candiedyaml"
	"github.com/cloudfoundry-incubator/inigo/fake_cc"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/loggredile"
	"github.com/cloudfoundry-incubator/inigo/world"
	"github.com/cloudfoundry-incubator/stager/outbox"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	"github.com/cloudfoundry-incubator/runtime-schema/cc_messages"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	zip_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

var _ = Describe("Stager", func() {
	var appId string
	var taskId string

	var fileServerStaticDir string

	var runtime ifrit.Process

	var fakeCC *fake_cc.FakeCC

	BeforeEach(func() {
		appId = factories.GenerateGuid()
		taskId = factories.GenerateGuid()

		fileServer, dir := componentMaker.FileServer()
		fileServerStaticDir = dir

		fakeCC = componentMaker.FakeCC()

		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"stager", componentMaker.Stager("-minDiskMB", "64", "-minMemoryMB", "64")},
			{"cc", fakeCC},
			{"nsync-listener", componentMaker.NsyncListener()},
			{"exec", componentMaker.Executor()},
			{"rep", componentMaker.Rep()},
			{"file-server", fileServer},
			{"loggregator", componentMaker.Loggregator()},
		}))
	})

	AfterEach(func() {
		helpers.StopProcess(runtime)
	})

	Context("when unable to find an appropriate compiler", func() {
		It("returns an error", func() {
			natsClient.Publish(
				"diego.staging.start",
				[]byte(fmt.Sprintf(`{
					"app_id": "%s",
					"task_id": "%s",
					"app_bits_download_uri": "some-download-uri",
					"build_artifacts_cache_download_uri": "artifacts-download-uri",
					"build_artifacts_cache_upload_uri": "artifacts-upload-uri",
					"stack": "no-circus"
				}`, appId, taskId)),
			)

			Eventually(fakeCC.StagingResponses).Should(HaveLen(1))
			Ω(fakeCC.StagingResponses()[0].Error).Should(ContainSubstring("no compiler defined for requested stack"))
		})
	})

	Describe("Staging", func() {
		var outputGuid string
		var stagingMessage []byte
		var buildpackToUse string

		BeforeEach(func() {
			buildpackToUse = "buildpack.zip"
			outputGuid = factories.GenerateGuid()

			cp(
				componentMaker.Artifacts.Circuses[componentMaker.Stack],
				filepath.Join(fileServerStaticDir, world.CircusFilename),
			)

			//make and upload an app
			var appFiles = []zip_helper.ArchiveFile{
				{Name: "my-app", Body: "scooby-doo"},
			}

			zip_helper.CreateZipArchive(filepath.Join(fileServerStaticDir, "app.zip"), appFiles)

			//make and upload a buildpack
			var adminBuildpackFiles = []zip_helper.ArchiveFile{
				{
					Name: "bin/detect",
					Body: `#!/bin/bash
echo My Buildpack
				`},
				{
					Name: "bin/compile",
					Body: `#!/bin/bash
echo $1 $2
echo COMPILING BUILDPACK
echo $SOME_STAGING_ENV
touch $1/compiled
touch $2/inserted-into-artifacts-cache
				`},
				{
					Name: "bin/release",
					Body: `#!/bin/bash
cat <<EOF
---
default_process_types:
  web: the-start-command
EOF
				`},
			}

			zip_helper.CreateZipArchive(
				filepath.Join(fileServerStaticDir, "buildpack.zip"),
				adminBuildpackFiles,
			)

			var bustedAdminBuildpackFiles = []zip_helper.ArchiveFile{
				{
					Name: "bin/detect",
					Body: `#!/bin/bash]
				exit 1
				`},
				{Name: "bin/compile", Body: `#!/bin/bash`},
				{Name: "bin/release", Body: `#!/bin/bash`},
			}

			zip_helper.CreateZipArchive(
				filepath.Join(fileServerStaticDir, "busted_buildpack.zip"),
				bustedAdminBuildpackFiles,
			)
		})

		JustBeforeEach(func() {
			stagingMessage = []byte(
				fmt.Sprintf(
					`{
						"app_id": "%s",
						"task_id": "%s",
						"memory_mb": 128,
						"disk_mb": 128,
						"file_descriptors": 1024,
						"stack": "lucid64",
						"app_bits_download_uri": "%s",
						"buildpacks" : [{ "name": "test-buildpack", "key": "test-buildpack-key", "url": "%s" }],
						"environment": [{ "name": "SOME_STAGING_ENV", "value": "%s"}]
					}`,
					appId,
					taskId,
					fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "app.zip"),
					fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, buildpackToUse),
					outputGuid,
				),
			)
		})

		Context("with one stager running", func() {
			It("runs the compiler on the executor with the correct environment variables, bits and log tag, and responds with the detected buildpack", func() {
				//stream logs
				logOutput := gbytes.NewBuffer()

				stop := loggredile.StreamIntoGBuffer(
					componentMaker.Addresses.LoggregatorOut,
					fmt.Sprintf("/tail/?app=%s", appId),
					"STG",
					logOutput,
					logOutput,
				)
				defer close(stop)

				//publish the staging message
				err := natsClient.Publish("diego.staging.start", stagingMessage)
				Ω(err).ShouldNot(HaveOccurred())

				//wait for staging to complete
				Eventually(fakeCC.StagingResponses).Should(HaveLen(1))
				Ω(fakeCC.StagingResponses()[0]).Should(Equal(
					cc_messages.StagingResponseForCC{
						AppId:                appId,
						TaskId:               taskId,
						BuildpackKey:         "test-buildpack-key",
						DetectedBuildpack:    "My Buildpack",
						ExecutionMetadata:    "{\"start_command\":\"the-start-command\"}",
						DetectedStartCommand: map[string]string{"web": "the-start-command"},
					}))

				//Assert the user saw reasonable output
				Eventually(logOutput).Should(gbytes.Say("COMPILING BUILDPACK"))
				Ω(logOutput.Contents()).Should(ContainSubstring(outputGuid))

				// Assert that the build artifacts cache was downloaded
				//TODO: how do we test they were downloaded??

				// Download the build artifacts cache from the file-server
				buildArtifactsCacheBytes := downloadBuildArtifactsCache(appId)
				Ω(buildArtifactsCacheBytes).ShouldNot(BeEmpty())

				// Assert that the downloaded build artifacts cache matches what the buildpack created
				artifactsCache, err := gzip.NewReader(bytes.NewReader(buildArtifactsCacheBytes))
				Ω(err).ShouldNot(HaveOccurred())

				untarredBuildArtifactsData := tar.NewReader(artifactsCache)
				buildArtifactContents := map[string][]byte{}
				for {
					hdr, err := untarredBuildArtifactsData.Next()
					if err == io.EOF {
						break
					}

					Ω(err).ShouldNot(HaveOccurred())

					content, err := ioutil.ReadAll(untarredBuildArtifactsData)
					Ω(err).ShouldNot(HaveOccurred())

					buildArtifactContents[hdr.Name] = content
				}

				//Ω(buildArtifactContents).Should(HaveKey("pulled-down-from-artifacts-cache"))
				Ω(buildArtifactContents).Should(HaveKey("./inserted-into-artifacts-cache"))

				//Fetch the compiled droplet from the fakeCC
				dropletData, ok := fakeCC.UploadedDroplets[appId]
				Ω(ok).Should(BeTrue())
				Ω(dropletData).ShouldNot(BeEmpty())

				//Unzip the droplet
				ungzippedDropletData, err := gzip.NewReader(bytes.NewReader(dropletData))
				Ω(err).ShouldNot(HaveOccurred())

				//Untar the droplet
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

				//Assert the droplet has the right files in it
				Ω(dropletContents).Should(HaveKey("./"))
				Ω(dropletContents).Should(HaveKey("./staging_info.yml"))
				Ω(dropletContents).Should(HaveKey("./logs/"))
				Ω(dropletContents).Should(HaveKey("./tmp/"))
				Ω(dropletContents).Should(HaveKey("./app/"))
				Ω(dropletContents).Should(HaveKey("./app/my-app"))
				Ω(dropletContents).Should(HaveKey("./app/compiled"))

				//Assert the files contain the right content
				Ω(string(dropletContents["./app/my-app"])).Should(Equal("scooby-doo"))

				//In particular, staging_info.yml should have the correct detected_buildpack and start_command
				yamlDecoder := candiedyaml.NewDecoder(bytes.NewReader(dropletContents["./staging_info.yml"]))
				stagingInfo := map[string]string{}
				err = yamlDecoder.Decode(&stagingInfo)
				Ω(err).ShouldNot(HaveOccurred())

				Ω(stagingInfo["detected_buildpack"]).Should(Equal("My Buildpack"))
				Ω(stagingInfo["start_command"]).Should(Equal("the-start-command"))

				//Assert nothing else crept into the droplet
				Ω(dropletContents).Should(HaveLen(7))
			})

			Context("when compilation fails", func() {
				BeforeEach(func() {
					buildpackToUse = "busted_buildpack.zip"
				})

				It("responds with the error, and no detected buildpack present", func() {
					logOutput := gbytes.NewBuffer()

					stop := loggredile.StreamIntoGBuffer(
						componentMaker.Addresses.LoggregatorOut,
						fmt.Sprintf("/tail/?app=%s", appId),
						"STG",
						logOutput,
						logOutput,
					)
					defer close(stop)

					err := natsClient.Publish("diego.staging.start", stagingMessage)
					Ω(err).ShouldNot(HaveOccurred())

					Eventually(fakeCC.StagingResponses).Should(HaveLen(1))
					Ω(fakeCC.StagingResponses()[0]).Should(Equal(
						cc_messages.StagingResponseForCC{
							AppId:  appId,
							TaskId: taskId,
							Error:  "Exited with status 1",
						}))
					Eventually(logOutput).Should(gbytes.Say("no valid buildpacks detected"))
				})
			})
		})

		Context("with two stagers running", func() {
			var otherStager ifrit.Process

			BeforeEach(func() {
				otherStager = ginkgomon.Invoke(componentMaker.Stager())
			})

			AfterEach(func() {
				helpers.StopProcess(otherStager)
			})

			It("only one returns a staging completed response", func() {
				err := natsClient.Publish(
					"diego.staging.start",
					stagingMessage,
				)
				Ω(err).ShouldNot(HaveOccurred())

				Eventually(fakeCC.StagingResponses).Should(HaveLen(1))
				Consistently(fakeCC.StagingResponses).Should(HaveLen(1))
			})
		})

		Context("when posting a staging response fails repeatedly", func() {
			var converger ifrit.Process

			BeforeEach(func() {
				converger = ginkgomon.Invoke(componentMaker.Converger(
					"-convergeRepeatInterval", "1s",
					"-kickPendingTaskDuration", "4s",
					"-expireCompletedTaskDuration", "6s",
				))
			})

			AfterEach(func() {
				converger.Signal(os.Kill)
			})

			It("should delete the staging task from the bbs", func() {
				fakeCC.SetStagingResponseStatusCode(http.StatusServiceUnavailable)
				fakeCC.SetStagingResponseBody(`{"error": "bah!"}`)

				err := natsClient.Publish("diego.staging.start", stagingMessage)
				Ω(err).ShouldNot(HaveOccurred())

				expectedStagingResponseCount := 2 * outbox.StagingResponseRetryLimit

				Eventually(fakeCC.StagingResponses).Should(HaveLen(expectedStagingResponseCount))
				Consistently(fakeCC.StagingResponses).Should(HaveLen(expectedStagingResponseCount))
			})
		})
	})

	Describe("Staging Docker", func() {
		var stagingMessage []byte
		var dockerImage = "cloudfoundry/inigodockertest"

		BeforeEach(func() {
			cp(
				componentMaker.Artifacts.DockerCircus,
				filepath.Join(fileServerStaticDir, world.DockerCircusFilename),
			)
		})

		JustBeforeEach(func() {
			stagingMessage = []byte(
				fmt.Sprintf(
					`{
						"app_id": "%s",
						"task_id": "%s",
						"memory_mb": 128,
						"disk_mb": 128,
						"docker_image": "%s",
						"file_descriptors": 1024,
						"stack": "lucid64"
					}`,
					appId,
					taskId,
					dockerImage,
				),
			)
		})

		Context("with one stager running", func() {
			It("runs the metadata extracted on the executor and responds with the execution metadata", func() {
				//stream logs
				logOutput := gbytes.NewBuffer()

				stop := loggredile.StreamIntoGBuffer(
					componentMaker.Addresses.LoggregatorOut,
					fmt.Sprintf("/tail/?app=%s", appId),
					"STG",
					logOutput,
					logOutput,
				)
				defer close(stop)

				//publish the staging message
				err := natsClient.Publish("diego.docker.staging.start", stagingMessage)
				Ω(err).ShouldNot(HaveOccurred())

				Eventually(fakeCC.StagingResponses).Should(HaveLen(1))
				Ω(fakeCC.StagingResponses()[0]).Should(Equal(
					cc_messages.StagingResponseForCC{
						AppId:                appId,
						TaskId:               taskId,
						ExecutionMetadata:    "{\"cmd\":[\"/dockerapp\"]}",
						DetectedStartCommand: map[string]string{"web": "/dockerapp"},
					}))
			})
		})

		Context("with two stagers running", func() {
			var otherStager ifrit.Process

			BeforeEach(func() {
				otherStager = ginkgomon.Invoke(componentMaker.Stager())
			})

			AfterEach(func() {
				helpers.StopProcess(otherStager)
			})

			It("only one returns a staging completed response", func() {
				err := natsClient.Publish(
					"diego.docker.staging.start",
					stagingMessage,
				)
				Ω(err).ShouldNot(HaveOccurred())

				Eventually(fakeCC.StagingResponses).Should(HaveLen(1))
				Consistently(fakeCC.StagingResponses).Should(HaveLen(1))
			})
		})
	})
})

func downloadBuildArtifactsCache(appId string) []byte {
	fileServerUrl := fmt.Sprintf("http://%s/v1/build_artifacts/%s", componentMaker.Addresses.FileServer, appId)
	resp, err := http.Get(fileServerUrl)
	Ω(err).ShouldNot(HaveOccurred())

	Ω(resp.StatusCode).Should(Equal(http.StatusOK))

	bytes, err := ioutil.ReadAll(resp.Body)
	Ω(err).ShouldNot(HaveOccurred())

	return bytes
}
