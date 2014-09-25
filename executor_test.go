package inigo_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"time"

	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"

	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	"github.com/cloudfoundry-incubator/inigo/loggredile"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/ghttp"
)

var _ = Describe("Executor", func() {
	var executor ifrit.Process

	var fileServerStaticDir string

	BeforeEach(func() {
		var fileServer ifrit.Runner

		fileServer, fileServerStaticDir = componentMaker.FileServer()

		executor = ifrit.Invoke(grouper.NewOrdered(nil, grouper.Members{
			{"file-server", fileServer},
			{"exec", componentMaker.Executor("-memoryMB", "1024")},
			{"rep", componentMaker.Rep()},
			{"loggregator", componentMaker.Loggregator()},
		}))
	})

	AfterEach(func() {
		helpers.StopProcess(executor)
	})

	Describe("Heartbeating", func() {
		It("should heartbeat its presence (through the rep)", func() {
			Eventually(bbs.GetAllExecutors).Should(HaveLen(1))
		})
	})

	Describe("Resource limits", func() {
		It("should only pick up tasks if it has capacity", func() {
			firstGuyGuid := factories.GenerateGuid()
			secondGuyGuid := factories.GenerateGuid()

			firstGuyTask := factories.BuildTaskWithRunAction(
				"inigo",
				componentMaker.Stack,
				1024,
				1024,
				"bash",
				[]string{"-c", fmt.Sprintf("curl %s; sleep 5", strings.Join(inigo_server.CurlArgs(firstGuyGuid), " "))},
			)

			err := bbs.DesireTask(firstGuyTask)
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(inigo_server.ReportingGuids).Should(ContainElement(firstGuyGuid))

			secondGuyTask := factories.BuildTaskWithRunAction(
				"inigo",
				componentMaker.Stack,
				1024,
				1024,
				"curl",
				inigo_server.CurlArgs(secondGuyGuid),
			)

			err = bbs.DesireTask(secondGuyTask)
			Ω(err).ShouldNot(HaveOccurred())

			Consistently(inigo_server.ReportingGuids).ShouldNot(ContainElement(secondGuyGuid))
		})
	})

	Describe("Stack", func() {
		var wrongStack = "penguin"

		It("should only pick up tasks if the stacks match", func() {
			matchingGuid := factories.GenerateGuid()
			matchingTask := factories.BuildTaskWithRunAction(
				"inigo",
				componentMaker.Stack,
				100,
				100,
				"bash",
				[]string{"-c", fmt.Sprintf("curl %s; sleep 10", strings.Join(inigo_server.CurlArgs(matchingGuid), " "))},
			)

			nonMatchingGuid := factories.GenerateGuid()
			nonMatchingTask := factories.BuildTaskWithRunAction(
				"inigo",
				wrongStack,
				100,
				100,
				"bash",
				[]string{"-c", fmt.Sprintf("curl %s; sleep 10", strings.Join(inigo_server.CurlArgs(nonMatchingGuid), " "))},
			)

			err := bbs.DesireTask(matchingTask)
			Ω(err).ShouldNot(HaveOccurred())

			err = bbs.DesireTask(nonMatchingTask)
			Ω(err).ShouldNot(HaveOccurred())

			Consistently(inigo_server.ReportingGuids).ShouldNot(ContainElement(nonMatchingGuid), "Did not expect to see this app running, as it has the wrong stack.")
			Eventually(inigo_server.ReportingGuids).Should(ContainElement(matchingGuid))
		})
	})

	Describe("Running a command", func() {
		var guid string

		BeforeEach(func() {
			guid = factories.GenerateGuid()
		})

		It("should run the command with the provided environment", func() {
			env := []models.EnvironmentVariable{
				{"FOO", "OLD-BAR"},
				{"BAZ", "WIBBLE"},
				{"FOO", "NEW-BAR"},
			}
			task := models.Task{
				Domain:   "inigo",
				Guid:     factories.GenerateGuid(),
				Stack:    componentMaker.Stack,
				MemoryMB: 1024,
				DiskMB:   1024,
				Actions: []models.ExecutorAction{
					{Action: models.RunAction{
						Path: "bash",
						Args: []string{"-c", "test $FOO = NEW-BAR && test $BAZ = WIBBLE"},
						Env:  env,
					}},
				},
			}

			err := bbs.DesireTask(task)
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(bbs.GetAllCompletedTasks).Should(HaveLen(1))

			tasks, _ := bbs.GetAllCompletedTasks()
			Ω(tasks[0].FailureReason).Should(BeEmpty())
			Ω(tasks[0].Failed).Should(BeFalse())
		})

		Context("when the command exceeds its memory limit", func() {
			var otherGuid string

			It("should fail the Task", func() {
				otherGuid = factories.GenerateGuid()
				task := models.Task{
					Domain:   "inigo",
					Guid:     factories.GenerateGuid(),
					Stack:    componentMaker.Stack,
					MemoryMB: 10,
					DiskMB:   1024,
					Actions: []models.ExecutorAction{
						{Action: models.RunAction{
							Path: "curl",
							Args: inigo_server.CurlArgs(guid),
						}},
						{Action: models.RunAction{
							Path: "ruby",
							Args: []string{"-e", "arr='m'*1024*1024*100"},
						}},
						{Action: models.RunAction{
							Path: "curl",
							Args: inigo_server.CurlArgs(otherGuid),
						}},
					},
				}

				err := bbs.DesireTask(task)
				Ω(err).ShouldNot(HaveOccurred())

				Eventually(inigo_server.ReportingGuids).Should(ContainElement(guid))

				Eventually(bbs.GetAllCompletedTasks).Should(HaveLen(1))
				tasks, _ := bbs.GetAllCompletedTasks()
				Ω(tasks[0].Failed).Should(BeTrue())
				Ω(tasks[0].FailureReason).Should(ContainSubstring("out of memory"))

				Ω(inigo_server.ReportingGuids()).ShouldNot(ContainElement(otherGuid))
			})
		})

		Context("when the command exceeds its file descriptor limit", func() {
			It("should fail the Task", func() {
				nofile := uint64(1)

				task := models.Task{
					Domain:   "inigo",
					Guid:     factories.GenerateGuid(),
					Stack:    componentMaker.Stack,
					MemoryMB: 10,
					DiskMB:   1024,
					Actions: []models.ExecutorAction{
						{
							models.RunAction{
								Path: "ruby",
								Args: []string{"-e", `10.times.each { |x| File.open("#{x}","w") }`},
								ResourceLimits: models.ResourceLimits{
									Nofile: &nofile,
								},
							},
						},
					},
				}

				err := bbs.DesireTask(task)
				Ω(err).ShouldNot(HaveOccurred())

				Eventually(bbs.GetAllCompletedTasks).Should(HaveLen(1))
				tasks, _ := bbs.GetAllCompletedTasks()
				Ω(tasks[0].Failed).Should(BeTrue())
				Ω(tasks[0].FailureReason).Should(ContainSubstring("127"))
			})
		})

		Context("when the command times out", func() {
			It("should fail the Task", func() {
				task := models.Task{
					Domain:   "inigo",
					Guid:     factories.GenerateGuid(),
					Stack:    componentMaker.Stack,
					MemoryMB: 1024,
					DiskMB:   1024,
					Actions: []models.ExecutorAction{
						{Action: models.RunAction{
							Path: "curl",
							Args: inigo_server.CurlArgs(guid),
						}},
						{Action: models.RunAction{
							Path:    "sleep",
							Args:    []string{"0.8"},
							Timeout: 500 * time.Millisecond,
						}},
					},
				}

				err := bbs.DesireTask(task)
				Ω(err).ShouldNot(HaveOccurred())

				Eventually(inigo_server.ReportingGuids).Should(ContainElement(guid))
				Eventually(bbs.GetAllCompletedTasks).Should(HaveLen(1))
				tasks, _ := bbs.GetAllCompletedTasks()
				Ω(tasks[0].Failed).Should(BeTrue())
				Ω(tasks[0].FailureReason).Should(ContainSubstring("Timed out after 500ms"))
			})
		})
	})

	Describe("Running a downloaded file", func() {
		var guid string

		BeforeEach(func() {
			guid = factories.GenerateGuid()

			err := ioutil.WriteFile(
				filepath.Join(fileServerStaticDir, "curling.sh"),
				[]byte(fmt.Sprintf("curl %s", strings.Join(inigo_server.CurlArgs(guid), " "))),
				0644,
			)
			Ω(err).ShouldNot(HaveOccurred())
		})

		It("downloads the file", func() {
			task := models.Task{
				Domain:   "inigo",
				Guid:     factories.GenerateGuid(),
				Stack:    componentMaker.Stack,
				MemoryMB: 1024,
				DiskMB:   1024,
				Actions: []models.ExecutorAction{
					{
						models.DownloadAction{
							From:    fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "curling.sh"),
							To:      "curling.sh",
							Extract: false,
						},
					},
					{
						models.RunAction{
							Path: "bash",
							Args: []string{"curling.sh"},
						},
					},
				},
			}

			err := bbs.DesireTask(task)
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(inigo_server.ReportingGuids).Should(ContainElement(guid))
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
					gw, err := gzip.NewReader(r.Body)
					Ω(err).ShouldNot(HaveOccurred())

					tw := tar.NewReader(gw)

					_, err = tw.Next()
					Ω(err).ShouldNot(HaveOccurred())

					contents, err := ioutil.ReadAll(tw)
					Ω(err).ShouldNot(HaveOccurred())

					Ω(string(contents)).Should(Equal("tasty thingy\n"))

					close(gotRequest)
				},
			))
		})

		AfterEach(func() {
			server.Close()
		})

		It("uploads a tarball containing the specified files", func() {
			task := models.Task{
				Domain:   "inigo",
				Guid:     factories.GenerateGuid(),
				Stack:    componentMaker.Stack,
				MemoryMB: 1024,
				DiskMB:   1024,
				Actions: []models.ExecutorAction{
					{
						models.RunAction{
							Path: "bash",
							Args: []string{"-c", "echo tasty thingy > thingy"},
						},
					},
					{
						models.UploadAction{
							From: "thingy",
							To:   fmt.Sprintf("http://%s/thingy", uploadAddr),
						},
					},
					{
						models.RunAction{
							Path: "curl",
							Args: inigo_server.CurlArgs(guid),
						},
					},
				},
			}

			err := bbs.DesireTask(task)
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(gotRequest).Should(BeClosed())

			Eventually(inigo_server.ReportingGuids).Should(ContainElement(guid))
		})
	})

	Describe("Fetching results", func() {
		It("should fetch the contents of the requested file and provide the content in the completed Task", func() {
			task := models.Task{
				Domain:   "inigo",
				Guid:     factories.GenerateGuid(),
				Stack:    componentMaker.Stack,
				MemoryMB: 1024,
				DiskMB:   1024,
				Actions: []models.ExecutorAction{
					{Action: models.RunAction{
						Path: "bash",
						Args: []string{"-c", "echo tasty thingy > thingy"},
					}},
					{Action: models.FetchResultAction{File: "thingy"}},
				},
			}

			err := bbs.DesireTask(task)
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(bbs.GetAllCompletedTasks).Should(HaveLen(1))

			tasks, _ := bbs.GetAllCompletedTasks()
			Ω(tasks[0].Result).Should(Equal("tasty thingy\n"))
		})
	})

	Describe("A Task with logging configured", func() {
		It("has its stdout and stderr emitted to Loggregator", func() {
			logGuid := factories.GenerateGuid()

			outBuf := gbytes.NewBuffer()
			errBuf := gbytes.NewBuffer()

			stop := loggredile.StreamIntoGBuffer(
				componentMaker.Addresses.LoggregatorOut,
				"/tail/?app="+logGuid,
				"APP",
				outBuf,
				errBuf,
			)
			defer close(stop)

			task := factories.BuildTaskWithRunAction(
				"inigo",
				componentMaker.Stack,
				1024,
				1024,
				"bash",
				[]string{"-c", "for i in $(seq 100); do echo $i; echo $i 1>&2; sleep 0.5; done"},
			)
			task.Log.Guid = logGuid
			task.Log.SourceName = "APP"

			err := bbs.DesireTask(task)
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(outBuf).Should(gbytes.Say(`(\d+\n){3}`))
			Eventually(errBuf).Should(gbytes.Say(`(\d+\n){3}`))

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
