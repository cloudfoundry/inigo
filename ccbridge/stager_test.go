package ccbridge_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cloudfoundry-incubator/candiedyaml"
	"github.com/cloudfoundry-incubator/inigo/fake_cc"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/world"
	"github.com/cloudfoundry/gunk/urljoiner"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	"github.com/cloudfoundry-incubator/runtime-schema/cc_messages"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	zip_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

const (
	buildpack_zip        = "buildpack.zip"
	busted_buildpack_zip = "busted_buildpack.zip"

	staging_source = "STG"
)

var _ = Describe("Stager", func() {
	var appId string
	var taskId string

	var fileServerStaticDir string

	var runtime ifrit.Process
	var bridge ifrit.Process

	var fakeCC *fake_cc.FakeCC

	var buildArtifactsUploadUri string
	var dropletUploadUri string

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

	BeforeEach(func() {
		appId = factories.GenerateGuid()
		taskId = factories.GenerateGuid()

		fileServer, dir := componentMaker.FileServer()
		fileServerStaticDir = dir

		fakeCC = componentMaker.FakeCC()

		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"exec", componentMaker.Executor()},
			{"rep", componentMaker.Rep()},
			{"receptor", componentMaker.Receptor()},
			{"file-server", fileServer},
		}))

		bridge = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"cc", fakeCC},
			{"stager", componentMaker.Stager("-minDiskMB", "64", "-minMemoryMB", "64")},
			{"nsync-listener", componentMaker.NsyncListener()},
		}))

		u, err := url.Parse(fakeCC.Address())
		Ω(err).ShouldNot(HaveOccurred())
		u.User = url.UserPassword(fakeCC.Username(), fakeCC.Password())
		u.Path = urljoiner.Join("staging", "droplets", appId, "upload?async=true")
		dropletUploadUri = u.String()
		u.Path = urljoiner.Join("staging", "buildpack_cache", appId, "upload")
		buildArtifactsUploadUri = u.String()
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime, bridge)
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
					"build_artifacts_cache_upload_uri": "%s",
					"droplet_upload_uri": "%s",
					"stack": "no-circus"
				}`, appId, taskId,
					buildArtifactsUploadUri, dropletUploadUri)),
			)

			Eventually(fakeCC.StagingResponses).Should(HaveLen(1))
			Ω(fakeCC.StagingResponses()[0].Error).Should(ContainSubstring("no compiler defined for requested stack"))
		})
	})

	Describe("Staging", func() {
		var outputGuid string
		var stagingMessage []byte
		var buildpacksToUse string

		createBuildpacks := func(name, key, buildpackPath string) (string, string) {
			u := urljoiner.Join("http://"+componentMaker.Addresses.FileServer+"/v1/static", buildpackPath)
			if name == cc_messages.CUSTOM_BUILDPACK {
				key = u
			}
			return fmt.Sprintf(`[{ "name": "%s", "key": "%s", "url": "%s" }]`, name, key, u), key
		}

		BeforeEach(func() {
			buildpacksToUse, _ = createBuildpacks("test-buildpack", "test-buildpack-key", buildpack_zip)
			outputGuid = factories.GenerateGuid()

			helpers.Copy(
				componentMaker.Artifacts.Circuses[componentMaker.Stack],
				filepath.Join(fileServerStaticDir, world.CircusFilename),
			)

			//make and upload an app
			var appFiles = []zip_helper.ArchiveFile{
				{Name: "my-app", Body: "scooby-doo"},
			}

			zip_helper.CreateZipArchive(filepath.Join(fileServerStaticDir, "app.zip"), appFiles)

			//make and upload a buildpack
			zip_helper.CreateZipArchive(
				filepath.Join(fileServerStaticDir, buildpack_zip),
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
				filepath.Join(fileServerStaticDir, busted_buildpack_zip),
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
						"build_artifacts_cache_upload_uri": "%s",
						"droplet_upload_uri": "%s",
						"buildpacks" : %s,
						"environment": [{ "name": "SOME_STAGING_ENV", "value": "%s"}]
					}`,
					appId,
					taskId,
					fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "app.zip"),
					buildArtifactsUploadUri,
					dropletUploadUri,
					buildpacksToUse,
					outputGuid,
				))
		})

		Context("with one stager running", func() {
			stageWith := func(buildpackName, buildpackKey, buildpackPath string) {
				Context("when compilation succeeds with: "+buildpackKey, func() {
					BeforeEach(func() {
						buildpacksToUse, buildpackKey = createBuildpacks(buildpackName, buildpackKey, buildpackPath)
					})

					It("runs the compiler on the executor with the correct environment variables and bits, and responds with the detected buildpack", func() {
						//publish the staging message
						err := natsClient.Publish("diego.staging.start", stagingMessage)
						Ω(err).ShouldNot(HaveOccurred())

						//wait for staging to complete
						Eventually(fakeCC.StagingResponses).Should(HaveLen(1))
						Ω(fakeCC.StagingResponses()[0]).Should(Equal(
							cc_messages.StagingResponseForCC{
								AppId:                appId,
								TaskId:               taskId,
								BuildpackKey:         buildpackKey,
								DetectedBuildpack:    "My Buildpack",
								ExecutionMetadata:    "{\"start_command\":\"the-start-command\"}",
								DetectedStartCommand: map[string]string{"web": "the-start-command"},
							}))

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
				})
			}

			stageWith("test-buildpack", "zip-buildpack", buildpack_zip)
			stageWith(cc_messages.CUSTOM_BUILDPACK, "custom-zip-buildpack", buildpack_zip)

			Context("with a git buildpack", func() {
				BeforeEach(func() {
					gitPath, err := exec.LookPath("git")
					Ω(err).ShouldNot(HaveOccurred())

					buildpackDir := filepath.Join(fileServerStaticDir, "buildpack")
					err = os.MkdirAll(buildpackDir, os.ModePerm)
					Ω(err).ShouldNot(HaveOccurred())

					execute(buildpackDir, "rm", "-rf", ".git")
					execute(buildpackDir, gitPath, "init")
					execute(buildpackDir, gitPath, "config", "user.email", "you@example.com")
					execute(buildpackDir, gitPath, "config", "user.name", "your name")

					for _, bpFile := range adminBuildpackFiles {
						filename := filepath.Join(buildpackDir, bpFile.Name)
						err = os.MkdirAll(filepath.Dir(filename), 0777)
						Ω(err).ShouldNot(HaveOccurred())

						err = ioutil.WriteFile(filename, []byte(bpFile.Body), 0777)
						Ω(err).ShouldNot(HaveOccurred())
					}

					execute(buildpackDir, gitPath, "add", ".")
					execute(buildpackDir, gitPath, "add", "-A")
					execute(buildpackDir, gitPath, "commit", "-am", "fake commit")
					execute(buildpackDir, gitPath, "update-server-info")
				})

				stageWith(cc_messages.CUSTOM_BUILDPACK, "git-buildpack", "buildpack/.git")
			})

			Context("when compilation fails", func() {
				BeforeEach(func() {
					buildpacksToUse, _ = createBuildpacks("busted-test-buildpack", "busted-test-buildpack-key", busted_buildpack_zip)
				})

				It("responds with the error, and no detected buildpack present", func() {
					err := natsClient.Publish("diego.staging.start", stagingMessage)
					Ω(err).ShouldNot(HaveOccurred())

					Eventually(fakeCC.StagingResponses).Should(HaveLen(1))
					Ω(fakeCC.StagingResponses()[0]).Should(Equal(
						cc_messages.StagingResponseForCC{
							AppId:  appId,
							TaskId: taskId,
							Error:  "Exited with status 1",
						}))
				})
			})
		})

		Context("with two stagers running", func() {
			var otherStager ifrit.Process

			BeforeEach(func() {
				otherStager = ginkgomon.Invoke(componentMaker.StagerN(1))
			})

			AfterEach(func() {
				helpers.StopProcesses(otherStager)
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

			It("eventually gives up", func() {
				fakeCC.SetStagingResponseStatusCode(http.StatusServiceUnavailable)
				fakeCC.SetStagingResponseBody(`{"error": "bah!"}`)

				err := natsClient.Publish("diego.staging.start", stagingMessage)
				Ω(err).ShouldNot(HaveOccurred())

				NUM_STAGERS := 2
				NUM_RETRIES := 3

				Eventually(fakeCC.StagingResponses).Should(HaveLen(NUM_STAGERS * NUM_RETRIES))
				Consistently(fakeCC.StagingResponses).Should(HaveLen(NUM_STAGERS * NUM_RETRIES))
			})
		})
	})

	Describe("Staging Docker", func() {
		var stagingMessage []byte
		var dockerImage = "cloudfoundry/inigodockertest"

		BeforeEach(func() {
			helpers.Copy(
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
				otherStager = ginkgomon.Invoke(componentMaker.StagerN(1))
			})

			AfterEach(func() {
				helpers.StopProcesses(otherStager)
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
	buildArtifactUrl := fmt.Sprintf("http://%s:%s@%s/staging/buildpack_cache/%s/download",
		fake_cc.CC_USERNAME, fake_cc.CC_PASSWORD, componentMaker.Addresses.FakeCC, appId)

	resp, err := http.Get(buildArtifactUrl)
	Ω(err).ShouldNot(HaveOccurred())
	defer resp.Body.Close()

	Ω(resp.StatusCode).Should(Equal(http.StatusOK))

	bytes, err := ioutil.ReadAll(resp.Body)

	Ω(err).ShouldNot(HaveOccurred())

	return bytes
}

func execute(dir string, execCmd string, args ...string) {
	cmd := exec.Command(execCmd, args...)
	cmd.Dir = dir
	err := cmd.Run()
	Ω(err).ShouldNot(HaveOccurred())
}
