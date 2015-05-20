package ccbridge_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudfoundry-incubator/candiedyaml"
	"github.com/cloudfoundry-incubator/inigo/fake_cc"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/world"
	"github.com/cloudfoundry/gunk/urljoiner"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	"github.com/cloudfoundry-incubator/receptor/task_handler"
	"github.com/cloudfoundry-incubator/runtime-schema/cc_messages"
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

	var (
		cell   ifrit.Process
		brain  ifrit.Process
		bridge ifrit.Process
	)

	var fakeCC *fake_cc.FakeCC

	var buildArtifactsUploadUri string
	var dropletUploadUri string

	var adminBuildpackFiles = []zip_helper.ArchiveFile{
		{
			Name: "bin/detect",
			Body: `#!/bin/sh
echo My Buildpack
				`},
		{
			Name: "bin/compile",
			Body: `#!/bin/sh
echo $1 $2
echo COMPILING BUILDPACK
echo $SOME_STAGING_ENV
touch $1/compiled
touch $2/inserted-into-artifacts-cache
				`},
		{
			Name: "bin/release",
			Body: `#!/bin/sh
cat <<EOF
---
default_process_types:
  web: the-start-command
EOF
				`},
	}

	BeforeEach(func() {
		appId = helpers.GenerateGuid()
		taskId = helpers.GenerateGuid()

		fileServer, dir := componentMaker.FileServer()
		fileServerStaticDir = dir

		fakeCC = componentMaker.FakeCC()

		cell = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"rep", componentMaker.Rep("-memoryMB=1024")},
		}))

		brain = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"receptor", componentMaker.Receptor()},
			{"auctioneer", componentMaker.Auctioneer()},
			{"file-server", fileServer},
		}))

		bridge = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"cc", fakeCC},
			{"stager", componentMaker.Stager()},
			{"nsync-listener", componentMaker.NsyncListener()},
		}))

		u, err := url.Parse(fakeCC.Address())
		Expect(err).NotTo(HaveOccurred())
		u.User = url.UserPassword(fakeCC.Username(), fakeCC.Password())
		u.Path = urljoiner.Join("staging", "droplets", appId, "upload?async=true")
		dropletUploadUri = u.String()
		u.Path = urljoiner.Join("staging", "buildpack_cache", appId, "upload")
		buildArtifactsUploadUri = u.String()
	})

	AfterEach(func() {
		helpers.StopProcesses(cell, brain, bridge)
	})

	stageApplication := func(stagingGuid, payload string) (*http.Response, error) {
		stageURL := urljoiner.Join("http://"+componentMaker.Addresses.Stager, "v1", "staging", stagingGuid)
		request, err := http.NewRequest("PUT", stageURL, strings.NewReader(payload))
		Expect(err).NotTo(HaveOccurred())

		return http.DefaultClient.Do(request)
	}

	Context("when unable to find an appropriate compiler", func() {
		It("returns an error", func() {
			resp, err := stageApplication(fmt.Sprintf("%s-%s", appId, taskId), fmt.Sprintf(`{
					"app_id": "%s",
					"log_guid": "%s",
					"lifecycle": "buildpack",
					"lifecycle_data": {
						"app_bits_download_uri": "some-download-uri",
						"build_artifacts_cache_download_uri": "artifacts-download-uri",
						"build_artifacts_cache_upload_uri": "%s",
						"droplet_upload_uri": "%s",
						"stack": "no-lifecycle"
					}
				}`, appId, appId, buildArtifactsUploadUri, dropletUploadUri),
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))

			decoder := json.NewDecoder(resp.Body)

			var response cc_messages.StagingResponseForCC
			err = decoder.Decode(&response)
			Expect(err).NotTo(HaveOccurred())

			Expect(response.Error).To(Equal(&cc_messages.StagingError{
				Id:      cc_messages.STAGING_ERROR,
				Message: "staging failed",
			}))

		})
	})

	Describe("Staging", func() {
		var outputGuid string
		var memory int
		var stagingGuid string
		var stagingMessage []byte
		var buildpacksToUse string

		createBuildpack := func(name, key, buildpackPath string) (string, string) {
			u := urljoiner.Join("http://"+componentMaker.Addresses.FileServer+"/v1/static", buildpackPath)
			if name == cc_messages.CUSTOM_BUILDPACK {
				key = u
			}
			return fmt.Sprintf(`[{ "name": "%s", "key": "%s", "url": "%s" }]`, name, key, u), key
		}

		BeforeEach(func() {
			buildpacksToUse, _ = createBuildpack("test-buildpack", "test-buildpack-key", buildpack_zip)
			outputGuid = helpers.GenerateGuid()
			memory = 128

			helpers.Copy(
				componentMaker.Artifacts.Lifecycles[componentMaker.DefaultStack()],
				filepath.Join(fileServerStaticDir, world.LifecycleFilename),
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
					Body: `#!/bin/sh
				exit 1
				`},
				{Name: "bin/compile", Body: `#!/bin/sh`},
				{Name: "bin/release", Body: `#!/bin/sh`},
			}

			zip_helper.CreateZipArchive(
				filepath.Join(fileServerStaticDir, busted_buildpack_zip),
				bustedAdminBuildpackFiles,
			)
		})

		JustBeforeEach(func() {
			stagingGuid = fmt.Sprintf("%s-%s", appId, taskId)
			stagingMessage = []byte(
				fmt.Sprintf(
					`{
						"app_id": "%s",
						"log_guid": "%s",
						"memory_mb": %d,
						"disk_mb": 128,
						"file_descriptors": 1024,
						"environment": [{ "name": "SOME_STAGING_ENV", "value": "%s"}],
						"lifecycle": "buildpack",
						"lifecycle_data": {
							"app_bits_download_uri": "%s",
							"build_artifacts_cache_upload_uri": "%s",
							"droplet_upload_uri": "%s",
							"stack": "%s",
							"buildpacks" : %s
						}
					}`,
					appId,
					appId,
					memory,
					outputGuid,
					fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "app.zip"),
					buildArtifactsUploadUri,
					dropletUploadUri,
					helpers.DefaultStack,
					buildpacksToUse,
				))
		})

		Context("with one stager running", func() {
			stageWith := func(buildpackName, buildpackKey, buildpackPath string) {
				Context("when compilation succeeds with: "+buildpackKey, func() {
					BeforeEach(func() {
						buildpacksToUse, buildpackKey = createBuildpack(buildpackName, buildpackKey, buildpackPath)
					})

					It("runs the compiler on the executor with the correct environment variables and bits, and responds with the detected buildpack", func() {
						resp, err := stageApplication(stagingGuid, string(stagingMessage))
						Expect(err).NotTo(HaveOccurred())
						Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

						//wait for staging to complete
						Eventually(fakeCC.StagingResponses).Should(HaveLen(1))
						buildpackResponse := cc_messages.BuildpackStagingResponse{
							BuildpackKey:      buildpackKey,
							DetectedBuildpack: "My Buildpack",
						}
						lifecycleDataJSON, err := json.Marshal(buildpackResponse)
						Expect(err).NotTo(HaveOccurred())

						lifecycleData := json.RawMessage(lifecycleDataJSON)

						Expect(fakeCC.StagingResponses()[0]).To(Equal(
							cc_messages.StagingResponseForCC{
								ExecutionMetadata:    "{\"start_command\":\"the-start-command\"}",
								DetectedStartCommand: map[string]string{"web": "the-start-command"},
								LifecycleData:        &lifecycleData,
							}))

						Expect(fakeCC.StagingGuids()[0]).To(Equal(stagingGuid))

						// Assert that the build artifacts cache was downloaded
						//TODO: how do we test they were downloaded??

						// Download the build artifacts cache from the file-server
						buildArtifactsCacheBytes := downloadBuildArtifactsCache(appId)
						Expect(buildArtifactsCacheBytes).NotTo(BeEmpty())

						// Assert that the downloaded build artifacts cache matches what the buildpack created
						artifactsCache, err := gzip.NewReader(bytes.NewReader(buildArtifactsCacheBytes))
						Expect(err).NotTo(HaveOccurred())

						untarredBuildArtifactsData := tar.NewReader(artifactsCache)
						buildArtifactContents := map[string][]byte{}
						for {
							hdr, err := untarredBuildArtifactsData.Next()
							if err == io.EOF {
								break
							}

							Expect(err).NotTo(HaveOccurred())

							content, err := ioutil.ReadAll(untarredBuildArtifactsData)
							Expect(err).NotTo(HaveOccurred())

							buildArtifactContents[hdr.Name] = content
						}

						Expect(buildArtifactContents).To(HaveKey("./inserted-into-artifacts-cache"))

						//Fetch the compiled droplet from the fakeCC
						dropletData, ok := fakeCC.UploadedDroplets[appId]
						Expect(ok).To(BeTrue())
						Expect(dropletData).NotTo(BeEmpty())

						//Unzip the droplet
						ungzippedDropletData, err := gzip.NewReader(bytes.NewReader(dropletData))
						Expect(err).NotTo(HaveOccurred())

						//Untar the droplet
						untarredDropletData := tar.NewReader(ungzippedDropletData)
						dropletContents := map[string][]byte{}
						for {
							hdr, err := untarredDropletData.Next()
							if err == io.EOF {
								break
							}
							Expect(err).NotTo(HaveOccurred())

							content, err := ioutil.ReadAll(untarredDropletData)
							Expect(err).NotTo(HaveOccurred())

							dropletContents[hdr.Name] = content
						}

						//Assert the droplet has the right files in it
						Expect(dropletContents).To(HaveKey("./"))
						Expect(dropletContents).To(HaveKey("./staging_info.yml"))
						Expect(dropletContents).To(HaveKey("./logs/"))
						Expect(dropletContents).To(HaveKey("./tmp/"))
						Expect(dropletContents).To(HaveKey("./app/"))
						Expect(dropletContents).To(HaveKey("./app/my-app"))
						Expect(dropletContents).To(HaveKey("./app/compiled"))

						//Assert the files contain the right content
						Expect(string(dropletContents["./app/my-app"])).To(Equal("scooby-doo"))

						//In particular, staging_info.yml should have the correct detected_buildpack and start_command
						yamlDecoder := candiedyaml.NewDecoder(bytes.NewReader(dropletContents["./staging_info.yml"]))
						stagingInfo := map[string]string{}
						err = yamlDecoder.Decode(&stagingInfo)
						Expect(err).NotTo(HaveOccurred())

						Expect(stagingInfo["detected_buildpack"]).To(Equal("My Buildpack"))
						Expect(stagingInfo["start_command"]).To(Equal("the-start-command"))

						//Assert nothing else crept into the droplet
						Expect(dropletContents).To(HaveLen(7))
					})
				})
			}

			stageWith("test-buildpack", "zip-buildpack", buildpack_zip)
			stageWith(cc_messages.CUSTOM_BUILDPACK, "custom-zip-buildpack", buildpack_zip)

			Context("with a git buildpack", func() {
				BeforeEach(func() {
					gitPath, err := exec.LookPath("git")
					Expect(err).NotTo(HaveOccurred())

					buildpackDir := filepath.Join(fileServerStaticDir, "buildpack")
					err = os.MkdirAll(buildpackDir, os.ModePerm)
					Expect(err).NotTo(HaveOccurred())

					execute(buildpackDir, "rm", "-rf", ".git")
					execute(buildpackDir, gitPath, "init")
					execute(buildpackDir, gitPath, "config", "user.email", "you@example.com")
					execute(buildpackDir, gitPath, "config", "user.name", "your name")

					for _, bpFile := range adminBuildpackFiles {
						filename := filepath.Join(buildpackDir, bpFile.Name)
						err = os.MkdirAll(filepath.Dir(filename), 0777)
						Expect(err).NotTo(HaveOccurred())

						err = ioutil.WriteFile(filename, []byte(bpFile.Body), 0777)
						Expect(err).NotTo(HaveOccurred())
					}

					execute(buildpackDir, gitPath, "add", ".")
					execute(buildpackDir, gitPath, "add", "-A")
					execute(buildpackDir, gitPath, "commit", "-am", "fake commit")
					execute(buildpackDir, gitPath, "update-server-info")
				})

				stageWith(cc_messages.CUSTOM_BUILDPACK, "git-buildpack", "buildpack/.git")
			})

			Context("when no detected buildpack present", func() {
				BeforeEach(func() {
					buildpacksToUse, _ = createBuildpack("busted-test-buildpack", "busted-test-buildpack-key", busted_buildpack_zip)
				})

				It("responds with the error", func() {
					resp, err := stageApplication(stagingGuid, string(stagingMessage))
					Expect(err).NotTo(HaveOccurred())
					Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

					Eventually(fakeCC.StagingResponses).Should(HaveLen(1))
					Expect(fakeCC.StagingResponses()[0]).To(Equal(
						cc_messages.StagingResponseForCC{
							Error: &cc_messages.StagingError{
								Id:      cc_messages.STAGING_ERROR,
								Message: "staging failed",
							},
						}))

					Expect(fakeCC.StagingGuids()[0]).To(Equal(stagingGuid))
				})
			})

			Context("when too much memory is requested", func() {
				BeforeEach(func() {
					memory = 2048
				})

				It("returns a staging completed response with 'insufficient resources' error", func() {
					resp, err := stageApplication(stagingGuid, string(stagingMessage))
					Expect(err).NotTo(HaveOccurred())
					Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

					Eventually(fakeCC.StagingResponses).Should(HaveLen(1))
					Expect(fakeCC.StagingResponses()[0]).To(Equal(
						cc_messages.StagingResponseForCC{
							Error: &cc_messages.StagingError{
								Id:      cc_messages.INSUFFICIENT_RESOURCES,
								Message: "insufficient resources",
							},
						}))

					Expect(fakeCC.StagingGuids()[0]).To(Equal(stagingGuid))
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
				resp, err := stageApplication(stagingGuid, string(stagingMessage))
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

				Eventually(fakeCC.StagingResponses).Should(HaveLen(1))
				Consistently(fakeCC.StagingResponses).Should(HaveLen(1))
			})
		})

		Context("with no cell running", func() {
			BeforeEach(func() {
				helpers.StopProcesses(cell)
			})

			It("returns a staging completed response with 'found no compatible cell' error", func() {
				resp, err := stageApplication(stagingGuid, string(stagingMessage))
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

				Eventually(fakeCC.StagingResponses).Should(HaveLen(1))
				Expect(fakeCC.StagingResponses()[0]).To(Equal(
					cc_messages.StagingResponseForCC{
						Error: &cc_messages.StagingError{
							Id:      cc_messages.NO_COMPATIBLE_CELL,
							Message: "found no compatible cell",
						},
					}))

				Expect(fakeCC.StagingGuids()[0]).To(Equal(stagingGuid))
			})
		})

		Context("when posting a staging response fails repeatedly", func() {
			var converger ifrit.Process
			const convergeRepeatInterval = time.Second

			// Choose duration factors so that:
			//  a) the resolving task will be rescheduled for completion before being expired
			//  b) the above will only happen once before the task is expired
			// Thus, the total number of expected resolution attempts is 2:
			//  a) once immediately after the task completes
			//  b) once as a result of convergence
			const expireFactor = 11
			const kickFactor = 7
			const expectedResolutionAttempts = 2

			BeforeEach(func() {
				converger = ginkgomon.Invoke(componentMaker.Converger(
					"-convergeRepeatInterval", convergeRepeatInterval.String(),
					"-expireCompletedTaskDuration", (expireFactor * convergeRepeatInterval).String(),
					"-kickPendingTaskDuration", (kickFactor * convergeRepeatInterval).String(),
				))
			})

			AfterEach(func() {
				converger.Signal(os.Kill)
			})

			It("eventually gives up", func() {
				fakeCC.SetStagingResponseStatusCode(http.StatusServiceUnavailable)
				fakeCC.SetStagingResponseBody(`{"error": "bah!"}`)

				resp, err := stageApplication(stagingGuid, string(stagingMessage))
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.StatusCode).To(Equal(http.StatusAccepted))

				numExpectedStagingResponses := task_handler.MAX_RETRIES * expectedResolutionAttempts

				Eventually(fakeCC.StagingResponses).Should(HaveLen(numExpectedStagingResponses))
				Consistently(fakeCC.StagingResponses).Should(HaveLen(numExpectedStagingResponses))
			})
		})
	})
})

func downloadBuildArtifactsCache(appId string) []byte {
	buildArtifactUrl := fmt.Sprintf("http://%s:%s@%s/staging/buildpack_cache/%s/download",
		fake_cc.CC_USERNAME, fake_cc.CC_PASSWORD, componentMaker.Addresses.FakeCC, appId)

	resp, err := http.Get(buildArtifactUrl)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()

	Expect(resp.StatusCode).To(Equal(http.StatusOK))

	bytes, err := ioutil.ReadAll(resp.Body)

	Expect(err).NotTo(HaveOccurred())

	return bytes
}

func execute(dir string, execCmd string, args ...string) {
	cmd := exec.Command(execCmd, args...)
	cmd.Dir = dir
	err := cmd.Run()
	Expect(err).NotTo(HaveOccurred())
}
