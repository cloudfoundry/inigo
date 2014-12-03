package cell_test

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pivotal-golang/archiver/extractor/test_helper"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"

	"github.com/cloudfoundry-incubator/executor"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/inigo_announcement_server"
	"github.com/cloudfoundry-incubator/inigo/loggredile"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/ghttp"
)

var _ = Describe("Executor", func() {
	var (
		executorProcess,
		fileServerProcess, repProcess, auctioneerProcess, loggregatorProcess, receptorProcess, convergerProcess ifrit.Process
	)

	var fileServerStaticDir string

	BeforeEach(func() {
		var fileServerRunner ifrit.Runner

		fileServerRunner, fileServerStaticDir = componentMaker.FileServer()

		executorProcess = ginkgomon.Invoke(componentMaker.Executor("-memoryMB", "1024"))
		fileServerProcess = ginkgomon.Invoke(fileServerRunner)
		repProcess = ginkgomon.Invoke(componentMaker.Rep())
		auctioneerProcess = ginkgomon.Invoke(componentMaker.Auctioneer())
		loggregatorProcess = ginkgomon.Invoke(componentMaker.Loggregator())
		receptorProcess = ginkgomon.Invoke(componentMaker.Receptor())
		convergerProcess = ginkgomon.Invoke(componentMaker.Converger())
	})

	AfterEach(func() {
		helpers.StopProcesses(executorProcess, fileServerProcess, repProcess, auctioneerProcess, loggregatorProcess, receptorProcess, convergerProcess)
	})

	Describe("Heartbeating", func() {
		It("should heartbeat its presence (through the rep)", func() {
			Eventually(receptorClient.Cells).Should(HaveLen(1))
		})
	})

	Describe("Resource limits", func() {
		It("should only pick up tasks if it has capacity", func() {
			firstGuyGuid := factories.GenerateGuid()
			secondGuyGuid := factories.GenerateGuid()

			err := receptorClient.CreateTask(receptor.TaskCreateRequest{
				TaskGuid: firstGuyGuid,
				Domain:   "inigo",
				Stack:    componentMaker.Stack,
				MemoryMB: 1024,
				DiskMB:   1024,
				Action: &models.RunAction{
					Path: "curl",
					Args: []string{inigo_announcement_server.AnnounceURL(firstGuyGuid)},
				},
			})
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(inigo_announcement_server.Announcements).Should(ContainElement(firstGuyGuid))

			err = receptorClient.CreateTask(receptor.TaskCreateRequest{
				TaskGuid: secondGuyGuid,
				Domain:   "inigo",
				Stack:    componentMaker.Stack,
				MemoryMB: 1024,
				DiskMB:   1024,
				Action: &models.RunAction{
					Path: "curl",
					Args: []string{inigo_announcement_server.AnnounceURL(secondGuyGuid)},
				},
			})
			Ω(err).ShouldNot(HaveOccurred())

			Consistently(inigo_announcement_server.Announcements).ShouldNot(ContainElement(secondGuyGuid))
		})
	})

	Describe("consistency", func() {
		Context("when a task is running and then something causes the container to go away (e.g. executor restart)", func() {
			var taskGuid string

			BeforeEach(func() {
				taskGuid = factories.GenerateGuid()

				err := receptorClient.CreateTask(receptor.TaskCreateRequest{
					TaskGuid: taskGuid,
					Domain:   "inigo",
					Stack:    componentMaker.Stack,
					Action: &models.RunAction{
						Path: "bash",
						Args: []string{"-c", "while true; do sleep 1; done"},
					},
				})
				Ω(err).ShouldNot(HaveOccurred())

				executorClient := componentMaker.ExecutorClient()

				Eventually(func() executor.State {
					container, err := executorClient.GetContainer(taskGuid)
					if err == nil {
						return container.State
					}
					return executor.StateInvalid
				}).Should(Equal(executor.StateCreated))

				// bounce executor
				executorProcess.Signal(syscall.SIGKILL)
				executorProcess = ginkgomon.Invoke(componentMaker.Executor("-memoryMB", "1024"))
			})

			It("eventually marks the task completed and failed", func() {
				var task receptor.TaskResponse

				Eventually(func() interface{} {
					var err error

					task, err = receptorClient.GetTask(taskGuid)
					Ω(err).ShouldNot(HaveOccurred())

					return task.State
				}).Should(Equal(receptor.TaskStateCompleted))

				Ω(task.Failed).Should(BeTrue())
			})
		})

		Context("when a lrp is running and then something causes the container to go away", func() {
			BeforeEach(func() {
				processGuid := factories.GenerateGuid()

				err := receptorClient.CreateDesiredLRP(receptor.DesiredLRPCreateRequest{
					Domain:      "inigo",
					ProcessGuid: processGuid,
					Instances:   1,
					Stack:       componentMaker.Stack,
					MemoryMB:    128,
					DiskMB:      1024,
					Ports:       []uint32{8080},
					Action: &models.RunAction{
						Path: "bash",
						Args: []string{
							"-c",
							"while true; do sleep 1; done",
						},
					},
					Monitor: &models.RunAction{
						Path: "bash",
						Args: []string{"-c", "echo all good"},
					},
				})
				Ω(err).ShouldNot(HaveOccurred())

				var actualLRPs []receptor.ActualLRPResponse
				Eventually(func() interface{} {
					actualLRPs, _ = receptorClient.ActualLRPsByProcessGuid(processGuid)
					return actualLRPs
				}).Should(HaveLen(1))

				instanceGuid := actualLRPs[0].InstanceGuid

				executorClient := componentMaker.ExecutorClient()

				Eventually(func() executor.State {
					container, err := executorClient.GetContainer(instanceGuid)
					if err == nil {
						return container.State
					}
					return executor.StateInvalid
				}).Should(Equal(executor.StateCreated))

				// bounce executor
				executorProcess.Signal(syscall.SIGKILL)
				executorProcess = ginkgomon.Invoke(componentMaker.Executor("-memoryMB", "1024"))
			})

			It("eventually deletes the lrp", func() {
				Eventually(receptorClient.ActualLRPs).Should(BeEmpty())
			})
		})
	})

	Describe("Stack", func() {
		var wrongStack = "penguin"

		It("should only pick up tasks if the stacks match", func() {
			matchingGuid := factories.GenerateGuid()
			nonMatchingGuid := factories.GenerateGuid()

			err := receptorClient.CreateTask(receptor.TaskCreateRequest{
				TaskGuid: matchingGuid,
				Domain:   "inigo",
				Stack:    componentMaker.Stack,
				Action: &models.RunAction{
					Path: "curl",
					Args: []string{inigo_announcement_server.AnnounceURL(matchingGuid)},
				},
			})
			Ω(err).ShouldNot(HaveOccurred())

			err = receptorClient.CreateTask(receptor.TaskCreateRequest{
				TaskGuid: nonMatchingGuid,
				Domain:   "inigo",
				Stack:    wrongStack,
				Action: &models.RunAction{
					Path: "curl",
					Args: []string{inigo_announcement_server.AnnounceURL(nonMatchingGuid)},
				},
			})
			Ω(err).ShouldNot(HaveOccurred())

			Consistently(inigo_announcement_server.Announcements).ShouldNot(ContainElement(nonMatchingGuid), "Did not expect to see this app running, as it has the wrong stack.")
			Eventually(inigo_announcement_server.Announcements).Should(ContainElement(matchingGuid))
		})
	})

	Describe("Running a task", func() {
		var guid string

		BeforeEach(func() {
			guid = factories.GenerateGuid()
		})

		It("runs the command with the provided environment", func() {
			err := receptorClient.CreateTask(receptor.TaskCreateRequest{
				TaskGuid: guid,
				Domain:   "inigo",
				Stack:    componentMaker.Stack,
				Action: &models.RunAction{
					Path: "bash",
					Args: []string{"-c", "[ $FOO = NEW-BAR -a $BAZ = WIBBLE ]"},
					Env: []models.EnvironmentVariable{
						{"FOO", "OLD-BAR"},
						{"BAZ", "WIBBLE"},
						{"FOO", "NEW-BAR"},
					},
				},
			})
			Ω(err).ShouldNot(HaveOccurred())

			var task receptor.TaskResponse

			Eventually(func() interface{} {
				var err error

				task, err = receptorClient.GetTask(guid)
				Ω(err).ShouldNot(HaveOccurred())

				return task.State
			}).Should(Equal(receptor.TaskStateCompleted))

			Ω(task.Failed).Should(BeFalse())
		})

		Context("when the command exceeds its memory limit", func() {
			It("should fail the Task", func() {
				err := receptorClient.CreateTask(receptor.TaskCreateRequest{
					Domain:   "inigo",
					TaskGuid: guid,
					Stack:    componentMaker.Stack,
					MemoryMB: 10,
					DiskMB:   1024,
					Action: models.Serial(
						&models.RunAction{
							Path: "curl",
							Args: []string{inigo_announcement_server.AnnounceURL("before-memory-overdose")},
						},
						&models.RunAction{
							Path: "ruby",
							Args: []string{"-e", "arr='m'*1024*1024*100"},
						},
						&models.RunAction{
							Path: "curl",
							Args: []string{inigo_announcement_server.AnnounceURL("after-memory-overdose")},
						},
					),
				})
				Ω(err).ShouldNot(HaveOccurred())

				Eventually(inigo_announcement_server.Announcements).Should(ContainElement("before-memory-overdose"))

				var task receptor.TaskResponse
				Eventually(func() interface{} {
					var err error

					task, err = receptorClient.GetTask(guid)
					Ω(err).ShouldNot(HaveOccurred())

					return task.State
				}).Should(Equal(receptor.TaskStateCompleted))

				Ω(task.Failed).Should(BeTrue())
				Ω(task.FailureReason).Should(ContainSubstring("out of memory"))

				Ω(inigo_announcement_server.Announcements()).ShouldNot(ContainElement("after-memory-overdose"))
			})
		})

		Context("when the command exceeds its file descriptor limit", func() {
			It("should fail the Task", func() {
				nofile := uint64(1)

				err := receptorClient.CreateTask(receptor.TaskCreateRequest{
					Domain:   "inigo",
					TaskGuid: guid,
					Stack:    componentMaker.Stack,
					Action: models.Serial(
						&models.RunAction{
							Path: "ruby",
							Args: []string{"-e", `10.times.each { |x| File.open("#{x}","w") }`},
							ResourceLimits: models.ResourceLimits{
								Nofile: &nofile,
							},
						},
					),
				})
				Ω(err).ShouldNot(HaveOccurred())

				var task receptor.TaskResponse
				Eventually(func() interface{} {
					var err error

					task, err = receptorClient.GetTask(guid)
					Ω(err).ShouldNot(HaveOccurred())

					return task.State
				}).Should(Equal(receptor.TaskStateCompleted))

				Ω(task.Failed).Should(BeTrue())
				Ω(task.FailureReason).Should(ContainSubstring("status 127"))
			})
		})

		Context("when the command times out", func() {
			It("should fail the Task", func() {
				err := receptorClient.CreateTask(receptor.TaskCreateRequest{
					Domain:   "inigo",
					TaskGuid: guid,
					Stack:    componentMaker.Stack,
					Action: models.Serial(
						models.Timeout(
							&models.RunAction{
								Path: "sleep",
								Args: []string{"1"},
							},
							500*time.Millisecond,
						),
					),
				})
				Ω(err).ShouldNot(HaveOccurred())

				var task receptor.TaskResponse
				Eventually(func() interface{} {
					var err error

					task, err = receptorClient.GetTask(guid)
					Ω(err).ShouldNot(HaveOccurred())

					return task.State
				}).Should(Equal(receptor.TaskStateCompleted))

				Ω(task.Failed).Should(BeTrue())
				Ω(task.FailureReason).Should(ContainSubstring("exceeded 500ms timeout"))
			})
		})
	})

	Describe("Running a privileged command", func() {
		var guid string
		var executorClient executor.Client

		BeforeEach(func() {
			guid = factories.GenerateGuid()

			env := []models.EnvironmentVariable{
				{"FOO", "OLD-BAR"},
				{"BAZ", "WIBBLE"},
				{"FOO", "NEW-BAR"},
			}

			taskRequest := receptor.TaskCreateRequest{
				Domain:   "inigo",
				TaskGuid: guid,
				Stack:    componentMaker.Stack,
				Action: &models.RunAction{
					Path:       "bash",
					Args:       []string{"-c", "while true; do sleep 1; done"},
					Env:        env,
					Privileged: true,
				},
			}

			err := receptorClient.CreateTask(taskRequest)
			Ω(err).ShouldNot(HaveOccurred())

			executorClient = componentMaker.ExecutorClient()
		})

		It("creates a container with a privileged run action", func() {
			Eventually(func() bool {
				container, err := executorClient.GetContainer(guid)
				if err != nil {
					return false
				}

				if action, ok := container.Action.(*models.RunAction); ok {
					return action.Privileged
				}

				return false
			}).Should(BeTrue())
		})

		It("correctly marshals the privileged flag back when querying the task through the receptor", func() {
			Eventually(func() bool {
				taskResponse, err := receptorClient.GetTask(guid)
				if err != nil {
					return false
				}

				if action, ok := taskResponse.Action.(*models.RunAction); ok {
					return action.Privileged
				}

				return false
			}).Should(BeTrue())
		})
	})

	Describe("Running a downloaded file", func() {
		var guid string

		BeforeEach(func() {
			guid = factories.GenerateGuid()

			test_helper.CreateTarGZArchive(filepath.Join(fileServerStaticDir, "curling.tar.gz"), []test_helper.ArchiveFile{
				{
					Name: "curling",
					Body: fmt.Sprintf("#!/bin/sh\n\ncurl %s", inigo_announcement_server.AnnounceURL(guid)),
					Mode: 0755,
				},
			})
		})

		It("downloads the file", func() {
			err := receptorClient.CreateTask(receptor.TaskCreateRequest{
				Domain:   "inigo",
				TaskGuid: guid,
				Stack:    componentMaker.Stack,
				Action: models.Serial(
					&models.DownloadAction{
						From: fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "curling.tar.gz"),
						To:   ".",
					},
					&models.RunAction{
						Path: "./curling",
					},
				),
			})
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(inigo_announcement_server.Announcements).Should(ContainElement(guid))
		})
	})

	Describe("Uploading from the container", func() {
		var guid string

		var server *httptest.Server
		var uploadAddr string

		var gotRequest chan struct{}

		BeforeEach(func() {
			guid = factories.GenerateGuid()

			gotRequest = make(chan struct{})

			server, uploadAddr = helpers.Callback(componentMaker.ExternalAddress, ghttp.CombineHandlers(
				ghttp.VerifyRequest("POST", "/thingy"),
				func(w http.ResponseWriter, r *http.Request) {
					contents, err := ioutil.ReadAll(r.Body)
					Ω(err).ShouldNot(HaveOccurred())

					Ω(string(contents)).Should(Equal("tasty thingy\n"))

					close(gotRequest)
				},
			))
		})

		AfterEach(func() {
			server.Close()
		})

		It("uploads the specified files", func() {
			err := receptorClient.CreateTask(receptor.TaskCreateRequest{
				Domain:   "inigo",
				TaskGuid: guid,
				Stack:    componentMaker.Stack,
				Action: models.Serial(
					&models.RunAction{
						Path: "bash",
						Args: []string{"-c", "echo tasty thingy > thingy"},
					},
					&models.UploadAction{
						From: "thingy",
						To:   fmt.Sprintf("http://%s/thingy", uploadAddr),
					},
					&models.RunAction{
						Path: "curl",
						Args: []string{inigo_announcement_server.AnnounceURL(guid)},
					},
				),
			})
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(gotRequest).Should(BeClosed())

			Eventually(inigo_announcement_server.Announcements).Should(ContainElement(guid))
		})
	})

	Describe("Fetching results", func() {
		It("should fetch the contents of the requested file and provide the content in the completed Task", func() {
			guid := factories.GenerateGuid()

			err := receptorClient.CreateTask(receptor.TaskCreateRequest{
				Domain:     "inigo",
				TaskGuid:   guid,
				Stack:      componentMaker.Stack,
				ResultFile: "thingy",
				Action: &models.RunAction{
					Path: "bash",
					Args: []string{"-c", "echo tasty thingy > thingy"},
				},
			})
			Ω(err).ShouldNot(HaveOccurred())

			var task receptor.TaskResponse
			Eventually(func() interface{} {
				var err error

				task, err = receptorClient.GetTask(guid)
				Ω(err).ShouldNot(HaveOccurred())

				return task.State
			}).Should(Equal(receptor.TaskStateCompleted))

			Ω(task.Result).Should(Equal("tasty thingy\n"))
		})
	})

	Describe("A Task with logging configured", func() {
		It("has its stdout, stderr, and exit status emitted to Loggregator", func() {
			logGuid := factories.GenerateGuid()

			outBuf := gbytes.NewBuffer()
			errBuf := gbytes.NewBuffer()

			stop := loggredile.StreamIntoGBuffer(
				componentMaker.Addresses.LoggregatorOut,
				"/apps/"+logGuid+"/stream",
				outBuf,
				errBuf,
			)
			defer close(stop)

			guid := factories.GenerateGuid()

			err := receptorClient.CreateTask(receptor.TaskCreateRequest{
				Domain:     "inigo",
				TaskGuid:   guid,
				Stack:      componentMaker.Stack,
				ResultFile: "thingy",
				Action: &models.RunAction{
					Path: "bash",
					Args: []string{
						"-c",
						"for i in $(seq 100); do echo $i; echo $i 1>&2; sleep 0.5; done; exit 34",
					},
				},
				LogGuid:   logGuid,
				LogSource: "APP",
			})
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(outBuf).Should(gbytes.Say(`(\d+\n){3}`))
			Eventually(errBuf).Should(gbytes.Say(`(\d+\n){3}`))

			Eventually(outBuf).Should(gbytes.Say("Exit status 34"))

			outReader := bytes.NewBuffer(outBuf.Contents())
			errReader := bytes.NewBuffer(errBuf.Contents())

			seenNum := -1

			for {
				var num int
				_, err := fmt.Fscanf(outReader, "%d\n", &num)
				if err != nil {
					break
				}

				Ω(num).Should(BeNumerically(">", seenNum))

				seenNum = num
			}

			Ω(seenNum).Should(BeNumerically(">=", 3))

			seenNum = -1

			for {
				var num int
				_, err := fmt.Fscanf(errReader, "%d\n", &num)
				if err != nil {
					break
				}

				Ω(num).Should(BeNumerically(">", seenNum))

				seenNum = num
			}

			Ω(seenNum).Should(BeNumerically(">=", 3))
		})
	})
})
