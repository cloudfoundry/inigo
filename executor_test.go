package inigo_test

import (
	"archive/tar"
	"compress/gzip"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudfoundry/gunk/timeprovider"

	"github.com/cloudfoundry-incubator/inigo/executor_runner"
	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	"github.com/cloudfoundry-incubator/inigo/loggredile"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	"github.com/cloudfoundry/loggregatorlib/logmessage"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/vito/cmdtest/matchers"
)

var _ = Describe("Executor", func() {
	var bbs *Bbs.BBS

	BeforeEach(func() {
		bbs = Bbs.New(suiteContext.EtcdRunner.Adapter(), timeprovider.NewTimeProvider())
	})

	Describe("starting without a snaphsot", func() {
		It("should come up, just fine", func() {
			suiteContext.ExecutorRunner.Start()
			suiteContext.ExecutorRunner.Stop()
		})
	})

	Describe("when starting with invalid memory/disk", func() {
		It("should exit with failure", func() {
			suiteContext.ExecutorRunner.StartWithoutCheck(executor_runner.Config{MemoryMB: -1, DiskMB: -1, SnapshotFile: "/tmp/i_dont_exist"})
			Ω(suiteContext.ExecutorRunner.Session).Should(SayWithTimeout("valid memory and disk capacity must be specified", time.Second))
			Ω(suiteContext.ExecutorRunner.Session).Should(ExitWith(1))
		})
	})

	Describe("when restarted with running tasks", func() {
		var tmpdir string
		var registrySnapshotFile string
		var executorConfig executor_runner.Config

		BeforeEach(func() {
			tmpdir, err := ioutil.TempDir(os.TempDir(), "executor-registry")
			Ω(err).ShouldNot(HaveOccurred())

			registrySnapshotFile = filepath.Join(tmpdir, "snapshot.json")

			executorConfig = executor_runner.Config{
				MemoryMB:     1024,
				DiskMB:       1024,
				SnapshotFile: registrySnapshotFile,
			}

			suiteContext.ExecutorRunner.Start(executorConfig)

			existingGuid := factories.GenerateGuid()

			existingRunOnce := factories.BuildRunOnceWithRunAction(
				suiteContext.ExecutorRunner.Config.Stack,
				1024,
				1024,
				inigo_server.CurlCommand(existingGuid)+"; sleep 60",
			)

			bbs.DesireRunOnce(existingRunOnce)

			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(existingGuid))

			suiteContext.ExecutorRunner.Stop()
		})

		AfterEach(func() {
			suiteContext.ExecutorRunner.Stop()

			os.RemoveAll(tmpdir)
		})

		Context("when the snapshot is corrupted", func() {
			It("should exit with failure", func() {
				file, err := ioutil.TempFile(os.TempDir(), "executor-invalid-snapshot")
				Ω(err).ShouldNot(HaveOccurred())

				_, err = file.Write([]byte("ß"))
				Ω(err).ShouldNot(HaveOccurred())

				executorConfig.SnapshotFile = file.Name()

				suiteContext.ExecutorRunner.StartWithoutCheck(executorConfig)

				Ω(suiteContext.ExecutorRunner.Session).Should(SayWithTimeout("corrupt registry", time.Second))
				Ω(suiteContext.ExecutorRunner.Session).Should(ExitWith(1))
			})
		})
	})

	Describe("Heartbeating", func() {
		It("should heartbeat its presence", func() {
			suiteContext.ExecutorRunner.Start()

			Eventually(func() interface{} {
				executors, _ := bbs.GetAllExecutors()
				return executors
			}).Should(HaveLen(1))
		})
	})

	Describe("Resource limits", func() {
		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
		})

		It("should only pick up tasks if it has capacity", func() {
			firstGuyGuid := factories.GenerateGuid()
			secondGuyGuid := factories.GenerateGuid()
			firstGuyRunOnce := factories.BuildRunOnceWithRunAction(suiteContext.ExecutorRunner.Config.Stack, 1024, 1024, inigo_server.CurlCommand(firstGuyGuid)+"; sleep 5")
			bbs.DesireRunOnce(firstGuyRunOnce)

			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(firstGuyGuid))

			secondGuyRunOnce := factories.BuildRunOnceWithRunAction(suiteContext.ExecutorRunner.Config.Stack, 1024, 1024, inigo_server.CurlCommand(secondGuyGuid))
			bbs.DesireRunOnce(secondGuyRunOnce)

			Consistently(inigo_server.ReportingGuids, SHORT_TIMEOUT).ShouldNot(ContainElement(secondGuyGuid))
		})
	})

	Describe("Stack", func() {
		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start(executor_runner.Config{Stack: "penguin"})
		})

		It("should only pick up tasks if the stacks match", func() {
			matchingGuid := factories.GenerateGuid()
			matchingRunOnce := factories.BuildRunOnceWithRunAction(suiteContext.ExecutorRunner.Config.Stack, 100, 100, inigo_server.CurlCommand(matchingGuid)+"; sleep 10")
			matchingRunOnce.Stack = "penguin"

			nonMatchingGuid := factories.GenerateGuid()
			nonMatchingRunOnce := factories.BuildRunOnceWithRunAction(suiteContext.ExecutorRunner.Config.Stack, 100, 100, inigo_server.CurlCommand(nonMatchingGuid)+"; sleep 10")
			nonMatchingRunOnce.Stack = "lion"

			bbs.DesireRunOnce(matchingRunOnce)
			bbs.DesireRunOnce(nonMatchingRunOnce)

			Consistently(inigo_server.ReportingGuids, SHORT_TIMEOUT).ShouldNot(ContainElement(nonMatchingGuid), "Did not expect to see this app running, as it has the wrong stack.")
			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(matchingGuid))
		})
	})

	Describe("Running a command", func() {
		var guid string
		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
			guid = factories.GenerateGuid()
		})

		It("should run the command with the provided environment", func() {
			env := [][]string{
				{"FOO", "BAR"},
				{"BAZ", "WIBBLE"},
				{"FOO", "$FOO-$BAZ"},
			}
			runOnce := &models.RunOnce{
				Guid:     factories.GenerateGuid(),
				Stack:    suiteContext.ExecutorRunner.Config.Stack,
				MemoryMB: 1024,
				DiskMB:   1024,
				Actions: []models.ExecutorAction{
					{Action: models.RunAction{Script: `test $FOO = "BAR-WIBBLE"`, Env: env}},
				},
			}

			bbs.DesireRunOnce(runOnce)

			Eventually(bbs.GetAllCompletedRunOnces, LONG_TIMEOUT).Should(HaveLen(1))

			runOnces, _ := bbs.GetAllCompletedRunOnces()
			Ω(runOnces[0].FailureReason).Should(BeEmpty())
			Ω(runOnces[0].Failed).Should(BeFalse())
		})

		Context("when the command exceeds its memory limit", func() {
			var otherGuid string

			It("should fail the RunOnce", func() {
				otherGuid = factories.GenerateGuid()
				runOnce := &models.RunOnce{
					Guid:     factories.GenerateGuid(),
					Stack:    suiteContext.ExecutorRunner.Config.Stack,
					MemoryMB: 10,
					DiskMB:   1024,
					Actions: []models.ExecutorAction{
						{Action: models.RunAction{Script: inigo_server.CurlCommand(guid)}},
						{Action: models.RunAction{Script: `ruby -e 'arr = "m"*1024*1024*100'`}},
						{Action: models.RunAction{Script: inigo_server.CurlCommand(otherGuid)}},
					},
				}

				bbs.DesireRunOnce(runOnce)

				Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(guid))

				Eventually(bbs.GetAllCompletedRunOnces, LONG_TIMEOUT).Should(HaveLen(1))
				runOnces, _ := bbs.GetAllCompletedRunOnces()
				Ω(runOnces[0].Failed).Should(BeTrue())
				Ω(runOnces[0].FailureReason).Should(ContainSubstring("out of memory"))

				Ω(inigo_server.ReportingGuids()).ShouldNot(ContainElement(otherGuid))
			})
		})

		Context("when the command exceeds its file descriptor limit", func() {
			It("should fail the RunOnce", func() {
				runOnce := &models.RunOnce{
					Guid:            factories.GenerateGuid(),
					Stack:           suiteContext.ExecutorRunner.Config.Stack,
					MemoryMB:        10,
					DiskMB:          1024,
					FileDescriptors: 1,
					Actions: []models.ExecutorAction{
						{Action: models.RunAction{Script: `ruby -e '10.times.each { |x| File.open("#{x}","w") }'`}},
					},
				}

				bbs.DesireRunOnce(runOnce)

				Eventually(bbs.GetAllCompletedRunOnces, LONG_TIMEOUT).Should(HaveLen(1))
				runOnces, _ := bbs.GetAllCompletedRunOnces()
				Ω(runOnces[0].Failed).Should(BeTrue())
				Ω(runOnces[0].FailureReason).Should(ContainSubstring("127"))
			})
		})

		Context("when the command times out", func() {
			It("should fail the RunOnce", func() {
				runOnce := &models.RunOnce{
					Guid:     factories.GenerateGuid(),
					Stack:    suiteContext.ExecutorRunner.Config.Stack,
					MemoryMB: 1024,
					DiskMB:   1024,
					Actions: []models.ExecutorAction{
						{Action: models.RunAction{Script: inigo_server.CurlCommand(guid)}},
						{Action: models.RunAction{Script: `sleep 0.8`, Timeout: 500 * time.Millisecond}},
					},
				}

				bbs.DesireRunOnce(runOnce)

				Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(guid))
				Eventually(bbs.GetAllCompletedRunOnces, LONG_TIMEOUT).Should(HaveLen(1))
				runOnces, _ := bbs.GetAllCompletedRunOnces()
				Ω(runOnces[0].Failed).Should(BeTrue())
				Ω(runOnces[0].FailureReason).Should(ContainSubstring("timed out"))
			})
		})
	})

	Describe("Running a downloaded file", func() {
		var guid string
		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()

			guid = factories.GenerateGuid()
			inigo_server.UploadFileString("curling.sh", inigo_server.CurlCommand(guid))
		})

		It("downloads the file", func() {
			runOnce := &models.RunOnce{
				Guid:     factories.GenerateGuid(),
				Stack:    suiteContext.ExecutorRunner.Config.Stack,
				MemoryMB: 1024,
				DiskMB:   1024,
				Actions: []models.ExecutorAction{
					{Action: models.DownloadAction{From: inigo_server.DownloadUrl("curling.sh"), To: "curling.sh", Extract: false}},
					{Action: models.RunAction{Script: "bash curling.sh"}},
				},
			}

			bbs.DesireRunOnce(runOnce)

			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(guid))
		})
	})

	Describe("Uploading from the container", func() {
		var guid string
		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()

			guid = factories.GenerateGuid()
		})

		It("uploads a tarball containing the specified files", func() {
			runOnce := &models.RunOnce{
				Guid:     factories.GenerateGuid(),
				Stack:    suiteContext.ExecutorRunner.Config.Stack,
				MemoryMB: 1024,
				DiskMB:   1024,
				Actions: []models.ExecutorAction{
					{Action: models.RunAction{Script: `echo "tasty thingy" > thingy`}},
					{Action: models.UploadAction{From: "thingy", To: inigo_server.UploadUrl("thingy")}},
					{Action: models.RunAction{Script: inigo_server.CurlCommand(guid)}},
				},
			}

			bbs.DesireRunOnce(runOnce)

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
		})

		It("should fetch the contents of the requested file and provide the content in the completed RunOnce", func() {
			runOnce := &models.RunOnce{
				Guid:     factories.GenerateGuid(),
				Stack:    suiteContext.ExecutorRunner.Config.Stack,
				MemoryMB: 1024,
				DiskMB:   1024,
				Actions: []models.ExecutorAction{
					{Action: models.RunAction{Script: `echo "tasty thingy" > thingy`}},
					{Action: models.FetchResultAction{File: "thingy"}},
				},
			}

			bbs.DesireRunOnce(runOnce)

			Eventually(bbs.GetAllCompletedRunOnces, LONG_TIMEOUT).Should(HaveLen(1))

			runOnces, _ := bbs.GetAllCompletedRunOnces()
			Ω(runOnces[0].Result).Should(Equal("tasty thingy\n"))
		})
	})

	Describe("A RunOnce with logging configured", func() {
		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
		})

		It("has its stdout and stderr emitted to Loggregator", func(done Done) {
			logGuid := factories.GenerateGuid()

			messages, stop := loggredile.StreamMessages(
				suiteContext.LoggregatorRunner.Config.OutgoingPort,
				"/tail/?app="+logGuid,
			)

			runOnce := factories.BuildRunOnceWithRunAction(
				suiteContext.ExecutorRunner.Config.Stack,
				1024,
				1024,
				"echo out A; echo out B; echo out C; echo err A 1>&2; echo err B 1>&2; echo err C 1>&2",
			)
			runOnce.Log.Guid = logGuid
			runOnce.Log.SourceName = "APP"

			bbs.DesireRunOnce(runOnce)

			outStream := []string{}
			errStream := []string{}

			for i := 0; i < 6; i++ {
				message := <-messages
				switch message.GetMessageType() {
				case logmessage.LogMessage_OUT:
					outStream = append(outStream, string(message.GetMessage()))
				case logmessage.LogMessage_ERR:
					errStream = append(errStream, string(message.GetMessage()))
				}
			}

			Ω(outStream).Should(Equal([]string{"out A", "out B", "out C"}))
			Ω(errStream).Should(Equal([]string{"err A", "err B", "err C"}))

			close(stop)
			close(done)
		}, LONG_TIMEOUT)
	})
})
