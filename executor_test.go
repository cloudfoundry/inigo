package inigo_test

import (
	"fmt"
	"github.com/cloudfoundry-incubator/inigo/executor_runner"
	"io/ioutil"
	"os"
	"time"

	"github.com/cloudfoundry-incubator/executor/taskregistry"
	"github.com/cloudfoundry-incubator/inigo/inigolistener"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
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

	Describe("starting with a snapshot", func() {
		Context("when the stapshot is valid", func() {
			var registrySnapshotFile string

			BeforeEach(func() {
				registrySnapshotFile = fmt.Sprintf("/tmp/inigo_executor_registry_%d", config.GinkgoConfig.ParallelNode)

				registry := taskregistry.NewTaskRegistry(registrySnapshotFile, 256, 1024)
				runOnce := models.RunOnce{
					Guid:     "a guid",
					MemoryMB: 256,
					DiskMB:   1024,
				}
				registry.AddRunOnce(runOnce)

				err := registry.WriteToDisk()
				Ω(err).ShouldNot(HaveOccurred())
			})

			AfterEach(func() {
				os.Remove(registrySnapshotFile)
			})

			It("starts up happily", func() {
				executorRunner.Start()
				executorRunner.Stop()
			})

			Context("when the existing apps in the snapshot don't fit within the memory limits", func() {
				It("should exit with failure", func() {
					executorRunner.StartWithoutCheck(executor_runner.Config{
						MemoryMB:     255,
						DiskMB:       1024,
						SnapshotFile: registrySnapshotFile})
					Ω(executorRunner.Session).Should(SayWithTimeout("memory requirements in snapshot exceed", time.Second))
					Ω(executorRunner.Session).Should(ExitWith(1))
				})
			})

			Context("when the existing apps in the snapshot don't fit within the disk limits", func() {
				It("should exit with failure", func() {
					executorRunner.StartWithoutCheck(executor_runner.Config{
						MemoryMB:     256,
						DiskMB:       1023,
						SnapshotFile: registrySnapshotFile})
					Ω(executorRunner.Session).Should(SayWithTimeout("disk requirements in snapshot exceed", time.Second))
					Ω(executorRunner.Session).Should(ExitWith(1))
				})
			})
		})

		Context("when the snapshot is corrupted", func() {
			It("should exit with failure", func() {
				registryFileName := "/tmp/bad_registry"
				ioutil.WriteFile(registryFileName, []byte("ß"), os.ModePerm)
				executorRunner.StartWithoutCheck(executor_runner.Config{SnapshotFile: registryFileName})
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

			executorRunner.Stop()
		})
	})

	Describe("Resource limits", func() {
		BeforeEach(func() {
			bbs = Bbs.New(etcdRunner.Adapter())
			executorRunner.Start()
		})

		AfterEach(func() {
			executorRunner.Stop()
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
})
