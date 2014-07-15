package inigo_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"strings"
	"time"

	steno "github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/gunk/timeprovider"

	"github.com/cloudfoundry-incubator/executor/integration/executor_runner"
	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	"github.com/cloudfoundry-incubator/inigo/loggredile"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
)

var _ = Describe("Executor", func() {
	var bbs *Bbs.BBS

	BeforeEach(func() {
		logSink := steno.NewTestingSink()

		steno.Init(&steno.Config{
			Sinks: []steno.Sink{logSink},
		})

		logger := steno.NewLogger("the-logger")
		steno.EnterTestMode()

		bbs = Bbs.NewBBS(suiteContext.EtcdRunner.Adapter(), timeprovider.NewTimeProvider(), logger)
	})

	Describe("invalid memory and disk limit flags", func() {
		It("fails when the memory limit is not valid", func() {
			suiteContext.ExecutorRunner.StartWithoutCheck(executor_runner.Config{MemoryMB: "0", DiskMB: "256"})
			Eventually(suiteContext.ExecutorRunner.Session).Should(gbytes.Say("memory limit must be a positive number or 'auto'"))
			Eventually(suiteContext.ExecutorRunner.Session).Should(gexec.Exit(1))
		})

		It("fails when the disk limit is not valid", func() {
			suiteContext.ExecutorRunner.StartWithoutCheck(executor_runner.Config{MemoryMB: "256", DiskMB: "0"})
			Eventually(suiteContext.ExecutorRunner.Session).Should(gbytes.Say("disk limit must be a positive number or 'auto'"))
			Eventually(suiteContext.ExecutorRunner.Session).Should(gexec.Exit(1))
		})
	})

	Describe("Heartbeating", func() {
		It("should heartbeat its presence (through the rep)", func() {
			suiteContext.ExecutorRunner.Start()
			suiteContext.RepRunner.Start()

			Eventually(bbs.GetAllExecutors).Should(HaveLen(1))
		})
	})

	Describe("Resource limits", func() {
		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
			suiteContext.RepRunner.Start()
		})

		It("should only pick up tasks if it has capacity", func() {
			firstGuyGuid := factories.GenerateGuid()
			secondGuyGuid := factories.GenerateGuid()
			firstGuyTask := factories.BuildTaskWithRunAction(
				suiteContext.RepStack,
				1024,
				1024,
				"bash",
				[]string{"-c", fmt.Sprintf("curl %s; sleep 5", strings.Join(inigo_server.CurlArgs(firstGuyGuid), " "))},
			)
			bbs.DesireTask(firstGuyTask)

			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(firstGuyGuid))

			secondGuyTask := factories.BuildTaskWithRunAction(
				suiteContext.RepStack,
				1024,
				1024,
				"curl",
				inigo_server.CurlArgs(secondGuyGuid),
			)
			bbs.DesireTask(secondGuyTask)

			Consistently(inigo_server.ReportingGuids, SHORT_TIMEOUT).ShouldNot(ContainElement(secondGuyGuid))
		})
	})

	Describe("Stack", func() {
		var wrongStack = "penguin"

		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
			suiteContext.RepRunner.Start()
		})

		It("should only pick up tasks if the stacks match", func() {
			matchingGuid := factories.GenerateGuid()
			matchingTask := factories.BuildTaskWithRunAction(
				suiteContext.RepStack,
				100,
				100,
				"bash",
				[]string{"-c", fmt.Sprintf("curl %s; sleep 10", strings.Join(inigo_server.CurlArgs(matchingGuid), " "))},
			)

			nonMatchingGuid := factories.GenerateGuid()
			nonMatchingTask := factories.BuildTaskWithRunAction(
				wrongStack,
				100,
				100,
				"bash",
				[]string{"-c", fmt.Sprintf("curl %s; sleep 10", strings.Join(inigo_server.CurlArgs(nonMatchingGuid), " "))},
			)

			bbs.DesireTask(matchingTask)
			bbs.DesireTask(nonMatchingTask)

			Consistently(inigo_server.ReportingGuids, SHORT_TIMEOUT).ShouldNot(ContainElement(nonMatchingGuid), "Did not expect to see this app running, as it has the wrong stack.")
			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(matchingGuid))
		})
	})

	Describe("Running a command", func() {
		var guid string
		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
			suiteContext.RepRunner.Start()

			guid = factories.GenerateGuid()
		})

		It("should run the command with the provided environment", func() {
			env := []models.EnvironmentVariable{
				{"FOO", "OLD-BAR"},
				{"BAZ", "WIBBLE"},
				{"FOO", "NEW-BAR"},
			}
			task := models.Task{
				Guid:     factories.GenerateGuid(),
				Stack:    suiteContext.RepStack,
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

			Eventually(bbs.GetAllCompletedTasks, LONG_TIMEOUT).Should(HaveLen(1))

			tasks, _ := bbs.GetAllCompletedTasks()
			Ω(tasks[0].FailureReason).Should(BeEmpty())
			Ω(tasks[0].Failed).Should(BeFalse())
		})

		Context("when the command exceeds its memory limit", func() {
			var otherGuid string

			It("should fail the Task", func() {
				otherGuid = factories.GenerateGuid()
				task := models.Task{
					Guid:     factories.GenerateGuid(),
					Stack:    suiteContext.RepStack,
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

				Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(guid))

				Eventually(bbs.GetAllCompletedTasks, LONG_TIMEOUT).Should(HaveLen(1))
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
					Guid:     factories.GenerateGuid(),
					Stack:    suiteContext.RepStack,
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

				Eventually(bbs.GetAllCompletedTasks, LONG_TIMEOUT).Should(HaveLen(1))
				tasks, _ := bbs.GetAllCompletedTasks()
				Ω(tasks[0].Failed).Should(BeTrue())
				Ω(tasks[0].FailureReason).Should(ContainSubstring("127"))
			})
		})

		Context("when the command times out", func() {
			It("should fail the Task", func() {
				task := models.Task{
					Guid:     factories.GenerateGuid(),
					Stack:    suiteContext.RepStack,
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

				Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(guid))
				Eventually(bbs.GetAllCompletedTasks, LONG_TIMEOUT).Should(HaveLen(1))
				tasks, _ := bbs.GetAllCompletedTasks()
				Ω(tasks[0].Failed).Should(BeTrue())
				Ω(tasks[0].FailureReason).Should(ContainSubstring("Timed out after 500ms"))
			})
		})
	})

	Describe("Running a downloaded file", func() {
		var guid string
		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
			suiteContext.RepRunner.Start()

			guid = factories.GenerateGuid()
			inigo_server.UploadFileString("curling.sh", fmt.Sprintf("curl %s", strings.Join(inigo_server.CurlArgs(guid), " ")))
		})

		It("downloads the file", func() {
			task := models.Task{
				Guid:     factories.GenerateGuid(),
				Stack:    suiteContext.RepStack,
				MemoryMB: 1024,
				DiskMB:   1024,
				Actions: []models.ExecutorAction{
					{Action: models.DownloadAction{From: inigo_server.DownloadUrl("curling.sh"), To: "curling.sh", Extract: false}},
					{Action: models.RunAction{
						Path: "bash",
						Args: []string{"curling.sh"},
					}},
				},
			}

			err := bbs.DesireTask(task)
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(guid))
		})
	})

	Describe("Uploading from the container", func() {
		var guid string
		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
			suiteContext.RepRunner.Start()

			guid = factories.GenerateGuid()
		})

		It("uploads a tarball containing the specified files", func() {
			task := models.Task{
				Guid:     factories.GenerateGuid(),
				Stack:    suiteContext.RepStack,
				MemoryMB: 1024,
				DiskMB:   1024,
				Actions: []models.ExecutorAction{
					{Action: models.RunAction{
						Path: "bash",
						Args: []string{"-c", "echo tasty thingy > thingy"},
					}},
					{Action: models.UploadAction{From: "thingy", To: inigo_server.UploadUrl("thingy")}},
					{Action: models.RunAction{
						Path: "curl",
						Args: inigo_server.CurlArgs(guid),
					}},
				},
			}

			err := bbs.DesireTask(task)
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(guid))

			downloadStream := inigo_server.DownloadFile("thingy")

			gw, err := gzip.NewReader(downloadStream)
			Ω(err).ShouldNot(HaveOccurred())

			tw := tar.NewReader(gw)

			_, err = tw.Next()
			Ω(err).ShouldNot(HaveOccurred())
		})
	})

	Describe("Fetching results", func() {
		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
			suiteContext.RepRunner.Start()
		})

		It("should fetch the contents of the requested file and provide the content in the completed Task", func() {
			task := models.Task{
				Guid:     factories.GenerateGuid(),
				Stack:    suiteContext.RepStack,
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

			Eventually(bbs.GetAllCompletedTasks, LONG_TIMEOUT).Should(HaveLen(1))

			tasks, _ := bbs.GetAllCompletedTasks()
			Ω(tasks[0].Result).Should(Equal("tasty thingy\n"))
		})
	})

	Describe("A Task with logging configured", func() {
		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
			suiteContext.RepRunner.Start()
		})

		It("has its stdout and stderr emitted to Loggregator", func() {
			logGuid := factories.GenerateGuid()

			outBuf := gbytes.NewBuffer()
			errBuf := gbytes.NewBuffer()

			stop := loggredile.StreamIntoGBuffer(
				suiteContext.LoggregatorRunner.Config.OutgoingPort,
				"/tail/?app="+logGuid,
				"APP",
				outBuf,
				errBuf,
			)
			defer close(stop)

			task := factories.BuildTaskWithRunAction(
				suiteContext.RepStack,
				1024,
				1024,
				"bash",
				[]string{"-c", "for i in $(seq 100); do echo $i; echo $i 1>&2; sleep 0.5; done"},
			)
			task.Log.Guid = logGuid
			task.Log.SourceName = "APP"

			err := bbs.DesireTask(task)
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(outBuf, LONG_TIMEOUT).Should(gbytes.Say(`(\d+\n){3}`))
			Eventually(errBuf, LONG_TIMEOUT).Should(gbytes.Say(`(\d+\n){3}`))

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
