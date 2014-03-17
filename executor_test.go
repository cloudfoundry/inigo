package inigo_test

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

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
		bbs = Bbs.New(etcdRunner.Adapter())
	})

	Describe("starting without a snaphsot", func() {
		It("should come up, just fine", func() {
			executorRunner.Start()
			executorRunner.Stop()
		})
	})

	Describe("when starting with invalid memory/disk", func() {
		It("should exit with failure", func() {
			executorRunner.StartWithoutCheck(executor_runner.Config{MemoryMB: -1, DiskMB: -1, SnapshotFile: "/tmp/i_dont_exist"})
			Ω(executorRunner.Session).Should(SayWithTimeout("valid memory and disk capacity must be specified", time.Second))
			Ω(executorRunner.Session).Should(ExitWith(1))
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

			executorRunner.Start(executorConfig)

			existingGuid := factories.GenerateGuid()

			existingRunOnce := factories.BuildRunOnceWithRunAction(
				1024,
				1024,
				inigoserver.CurlCommand(existingGuid)+"; sleep 60",
			)

			bbs.DesireRunOnce(existingRunOnce)

			Eventually(inigoserver.ReportingGuids, 5.0).Should(ContainElement(existingGuid))

			executorRunner.Stop()
		})

		AfterEach(func() {
			executorRunner.Stop()

			os.RemoveAll(tmpdir)
		})

		Context("when the snapshot is corrupted", func() {
			It("should exit with failure", func() {
				file, err := ioutil.TempFile(os.TempDir(), "executor-invalid-snapshot")
				Ω(err).ShouldNot(HaveOccurred())

				_, err = file.Write([]byte("ß"))
				Ω(err).ShouldNot(HaveOccurred())

				executorConfig.SnapshotFile = file.Name()

				executorRunner.StartWithoutCheck(executorConfig)

				Ω(executorRunner.Session).Should(SayWithTimeout("corrupt registry", time.Second))
				Ω(executorRunner.Session).Should(ExitWith(1))
			})
		})
	})

	Describe("Heartbeating", func() {
		It("should heartbeat its presence", func() {
			executorRunner.Start()

			Eventually(func() interface{} {
				executors, _ := bbs.GetAllExecutors()
				return executors
			}).Should(HaveLen(1))
		})
	})

	Describe("Resource limits", func() {
		BeforeEach(func() {
			executorRunner.Start()
		})

		It("should only pick up tasks if it has capacity", func() {
			firstGuyGuid := factories.GenerateGuid()
			secondGuyGuid := factories.GenerateGuid()
			firstGuyRunOnce := factories.BuildRunOnceWithRunAction(1024, 1024, inigoserver.CurlCommand(firstGuyGuid)+"; sleep 5")
			bbs.DesireRunOnce(firstGuyRunOnce)

			Eventually(inigoserver.ReportingGuids, 5.0).Should(ContainElement(firstGuyGuid))

			secondGuyRunOnce := factories.BuildRunOnceWithRunAction(1024, 1024, inigoserver.CurlCommand(secondGuyGuid))
			bbs.DesireRunOnce(secondGuyRunOnce)

			Consistently(inigoserver.ReportingGuids, 2.0).ShouldNot(ContainElement(secondGuyGuid))
		})
	})

	Describe("Stack", func() {
		BeforeEach(func() {
			executorRunner.Start(executor_runner.Config{Stack: "penguin"})
		})

		It("should only pick up tasks if the stacks match", func() {
			matchingGuid := factories.GenerateGuid()
			matchingRunOnce := factories.BuildRunOnceWithRunAction(100, 100, inigoserver.CurlCommand(matchingGuid)+"; sleep 10")
			matchingRunOnce.Stack = "penguin"

			nonMatchingGuid := factories.GenerateGuid()
			nonMatchingRunOnce := factories.BuildRunOnceWithRunAction(100, 100, inigoserver.CurlCommand(nonMatchingGuid)+"; sleep 10")
			nonMatchingRunOnce.Stack = "lion"

			bbs.DesireRunOnce(matchingRunOnce)
			bbs.DesireRunOnce(nonMatchingRunOnce)

			Consistently(inigoserver.ReportingGuids, 2.0).ShouldNot(ContainElement(nonMatchingGuid), "Did not expect to see this app running, as it has the wrong stack.")
			Eventually(inigoserver.ReportingGuids, 5.0).Should(ContainElement(matchingGuid))
		})
	})

	Describe("Running a command", func() {
		var guid string
		BeforeEach(func() {
			executorRunner.Start()
			guid = factories.GenerateGuid()
		})

		It("should run the command with the provided environment", func() {
			env := [][]string{
				{"FOO", "BAR"},
				{"BAZ", "WIBBLE"},
				{"FOO", "$FOO-$BAZ"},
			}
			runOnce := models.RunOnce{
				Guid:     factories.GenerateGuid(),
				MemoryMB: 1024,
				DiskMB:   1024,
				Actions: []models.ExecutorAction{
					{Action: models.RunAction{Script: `echo $FOO > out`, Env: env}},
					{Action: models.UploadAction{From: "out", To: inigoserver.UploadUrl("out")}},
					{Action: models.RunAction{Script: inigoserver.CurlCommand(guid)}},
				},
			}

			bbs.DesireRunOnce(runOnce)

			Eventually(inigoserver.ReportingGuids, 5.0).Should(ContainElement(guid))
			Ω(inigoserver.DownloadFileString("out")).Should(Equal("BAR-WIBBLE\n"))

			Eventually(bbs.GetAllCompletedRunOnces, 5.0).Should(HaveLen(1))
			runOnces, _ := bbs.GetAllCompletedRunOnces()
			Ω(runOnces[0].Failed).Should(BeFalse())
		})

		Context("when the command exceeds its memory limit", func() {
			var otherGuid string

			It("should fail the RunOnce", func() {
				otherGuid = factories.GenerateGuid()
				runOnce := models.RunOnce{
					Guid:     factories.GenerateGuid(),
					MemoryMB: 10,
					DiskMB:   1024,
					Actions: []models.ExecutorAction{
						{Action: models.RunAction{Script: inigoserver.CurlCommand(guid)}},
						{Action: models.RunAction{Script: `ruby -e 'arr = "m"*1024*1024*100'`}},
						{Action: models.RunAction{Script: inigoserver.CurlCommand(otherGuid)}},
					},
				}

				bbs.DesireRunOnce(runOnce)

				Eventually(inigoserver.ReportingGuids, 5.0).Should(ContainElement(guid))

				Eventually(bbs.GetAllCompletedRunOnces, 5.0).Should(HaveLen(1))
				runOnces, _ := bbs.GetAllCompletedRunOnces()
				Ω(runOnces[0].Failed).Should(BeTrue())
				Ω(runOnces[0].FailureReason).Should(ContainSubstring("out of memory"))

				Ω(inigoserver.ReportingGuids()).ShouldNot(ContainElement(otherGuid))
			})
		})

		Context("when the command exceeds its file descriptor limit", func() {
			It("should fail the RunOnce", func() {
				runOnce := models.RunOnce{
					Guid:            factories.GenerateGuid(),
					MemoryMB:        10,
					DiskMB:          1024,
					FileDescriptors: 1,
					Actions: []models.ExecutorAction{
						{Action: models.RunAction{Script: `ruby -e '10.times.each { |x| File.open("#{x}","w") }'`}},
					},
				}

				bbs.DesireRunOnce(runOnce)

				Eventually(bbs.GetAllCompletedRunOnces, 5.0).Should(HaveLen(1))
				runOnces, _ := bbs.GetAllCompletedRunOnces()
				Ω(runOnces[0].Failed).Should(BeTrue())
				Ω(runOnces[0].FailureReason).Should(ContainSubstring("127"))
			})
		})

		Context("when the command times out", func() {
			It("should fail the RunOnce", func() {
				runOnce := models.RunOnce{
					Guid:     factories.GenerateGuid(),
					MemoryMB: 1024,
					DiskMB:   1024,
					Actions: []models.ExecutorAction{
						{Action: models.RunAction{Script: inigoserver.CurlCommand(guid)}},
						{Action: models.RunAction{Script: `sleep 0.8`, Timeout: 500 * time.Millisecond}},
					},
				}

				bbs.DesireRunOnce(runOnce)

				Eventually(inigoserver.ReportingGuids, 5.0).Should(ContainElement(guid))
				Eventually(bbs.GetAllCompletedRunOnces, 5.0).Should(HaveLen(1))
				runOnces, _ := bbs.GetAllCompletedRunOnces()
				Ω(runOnces[0].Failed).Should(BeTrue())
				Ω(runOnces[0].FailureReason).Should(ContainSubstring("timed out"))
			})
		})
	})

	Describe("Running a downloaded file", func() {
		var guid string
		BeforeEach(func() {
			executorRunner.Start()

			guid = factories.GenerateGuid()
			inigoserver.UploadFileString("curling.sh", inigoserver.CurlCommand(guid))
		})

		It("downloads the file", func() {
			runOnce := models.RunOnce{
				Guid:     factories.GenerateGuid(),
				MemoryMB: 1024,
				DiskMB:   1024,
				Actions: []models.ExecutorAction{
					{Action: models.DownloadAction{From: inigoserver.DownloadUrl("curling.sh"), To: "curling.sh", Extract: false}},
					{Action: models.RunAction{Script: "bash curling.sh"}},
				},
			}

			bbs.DesireRunOnce(runOnce)

			Eventually(inigoserver.ReportingGuids, 5.0).Should(ContainElement(guid))
		})
	})

	Describe("Uploading a file", func() {
		var guid string
		BeforeEach(func() {
			executorRunner.Start()

			guid = factories.GenerateGuid()
		})

		It("uploads the file", func() {
			runOnce := models.RunOnce{
				Guid:     factories.GenerateGuid(),
				MemoryMB: 1024,
				DiskMB:   1024,
				Actions: []models.ExecutorAction{
					{Action: models.RunAction{Script: `echo "tasty thingy" > thingy`}},
					{Action: models.UploadAction{From: "thingy", To: inigoserver.UploadUrl("thingy")}},
					{Action: models.RunAction{Script: inigoserver.CurlCommand(guid)}},
				},
			}

			bbs.DesireRunOnce(runOnce)

			Eventually(inigoserver.ReportingGuids, 5.0).Should(ContainElement(guid))
			Ω(inigoserver.DownloadFileString("thingy")).Should(Equal("tasty thingy\n"))
		})
	})

	Describe("Fetching results", func() {
		BeforeEach(func() {
			executorRunner.Start()
		})

		It("should fetch the contents of the requested file and provide the content in the completed RunOnce", func() {
			runOnce := models.RunOnce{
				Guid:     factories.GenerateGuid(),
				MemoryMB: 1024,
				DiskMB:   1024,
				Actions: []models.ExecutorAction{
					{Action: models.RunAction{Script: `echo "tasty thingy" > thingy`}},
					{Action: models.FetchResultAction{File: "thingy"}},
				},
			}

			bbs.DesireRunOnce(runOnce)

			Eventually(bbs.GetAllCompletedRunOnces, 5.0).Should(HaveLen(1))

			runOnces, _ := bbs.GetAllCompletedRunOnces()
			Ω(runOnces[0].Result).Should(Equal("tasty thingy\n"))
		})
	})

	Describe("A RunOnce with logging configured", func() {
		BeforeEach(func() {
			executorRunner.Start()
		})

		It("has its stdout and stderr emitted to Loggregator", func(done Done) {
			logGuid := factories.GenerateGuid()

			messages, stop := loggredile.StreamMessages(
				loggregatorRunner.Config.OutgoingPort,
				"/tail/?app="+logGuid,
			)

			runOnce := factories.BuildRunOnceWithRunAction(
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
		}, 10.0)
	})
})
