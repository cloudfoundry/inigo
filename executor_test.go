package inigo_test

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudfoundry-incubator/inigo/executor_runner"
	"github.com/cloudfoundry-incubator/inigo/inigolistener"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/vito/cmdtest/matchers"
)

var _ = Describe("Executor", func() {
	var bbs *Bbs.BBS

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

			bbs = Bbs.New(etcdRunner.Adapter())

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
				inigolistener.CurlCommand(existingGuid)+"; sleep 60",
			)

			bbs.DesireRunOnce(existingRunOnce)

			Eventually(inigolistener.ReportingGuids, 5.0).Should(ContainElement(existingGuid))

			executorRunner.Stop()
		})

		AfterEach(func() {
			executorRunner.Stop()

			os.RemoveAll(tmpdir)
		})

		It("retains their resource usage", func() {
			executorRunner.Start(executorConfig)

			cantFitGuid := factories.GenerateGuid()

			cantFitRunOnce := factories.BuildRunOnceWithRunAction(
				1024,
				1024,
				inigolistener.CurlCommand(cantFitGuid)+"; sleep 60",
			)

			bbs.DesireRunOnce(cantFitRunOnce)

			Consistently(inigolistener.ReportingGuids, 5.0).ShouldNot(ContainElement(cantFitGuid))
		})

		Context("and we were previously running more than we can now handle", func() {
			Context("of memory", func() {
				It("fails to start with a helpful message", func() {
					executorConfig.MemoryMB = 512

					executorRunner.StartWithoutCheck(executorConfig)

					Ω(executorRunner.Session).Should(SayWithTimeout(
						"memory requirements in snapshot exceed",
						time.Second,
					))

					Ω(executorRunner.Session).Should(ExitWith(1))
				})
			})

			Context("of disk", func() {
				It("fails to start with a helpful message", func() {
					executorConfig.DiskMB = 512

					executorRunner.StartWithoutCheck(executorConfig)

					Ω(executorRunner.Session).Should(SayWithTimeout(
						"disk requirements in snapshot exceed",
						time.Second,
					))

					Ω(executorRunner.Session).Should(ExitWith(1))
				})
			})
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
			bbs = Bbs.New(etcdRunner.Adapter())
			executorRunner.Start()

			Eventually(func() interface{} {
				executors, _ := bbs.GetAllExecutors()
				return executors
			}).Should(HaveLen(1))
		})
	})

	Describe("Resource limits", func() {
		BeforeEach(func() {
			bbs = Bbs.New(etcdRunner.Adapter())
			executorRunner.Start()
		})

		It("should only pick up tasks if it has capacity", func() {
			firstGuyGuid := factories.GenerateGuid()
			secondGuyGuid := factories.GenerateGuid()
			firstGuyRunOnce := factories.BuildRunOnceWithRunAction(1024, 1024, inigolistener.CurlCommand(firstGuyGuid)+"; sleep 5")
			bbs.DesireRunOnce(firstGuyRunOnce)

			Eventually(inigolistener.ReportingGuids, 5.0).Should(ContainElement(firstGuyGuid))

			secondGuyRunOnce := factories.BuildRunOnceWithRunAction(1024, 1024, inigolistener.CurlCommand(secondGuyGuid))
			bbs.DesireRunOnce(secondGuyRunOnce)

			Consistently(inigolistener.ReportingGuids, 2.0).ShouldNot(ContainElement(secondGuyGuid))
		})
	})

	Describe("Stack", func() {
		BeforeEach(func() {
			bbs = Bbs.New(etcdRunner.Adapter())
			executorRunner.Start(executor_runner.Config{Stack: "penguin"})
		})

		It("should only pick up tasks if the stacks match", func() {
			matchingGuid := factories.GenerateGuid()
			matchingRunOnce := factories.BuildRunOnceWithRunAction(1, 1, inigolistener.CurlCommand(matchingGuid)+"; sleep 10")
			matchingRunOnce.Stack = "penguin"

			nonMatchingGuid := factories.GenerateGuid()
			nonMatchingRunOnce := factories.BuildRunOnceWithRunAction(1, 1, inigolistener.CurlCommand(nonMatchingGuid)+"; sleep 10")
			nonMatchingRunOnce.Stack = "lion"

			bbs.DesireRunOnce(matchingRunOnce)
			bbs.DesireRunOnce(nonMatchingRunOnce)

			Consistently(inigolistener.ReportingGuids, 2.0).ShouldNot(ContainElement(nonMatchingGuid), "Did not expect to see this app running, as it has the wrong stack.")
			Eventually(inigolistener.ReportingGuids, 5.0).Should(ContainElement(matchingGuid))
		})
	})
})
