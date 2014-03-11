package inigo_test

import (
	"time"

	"github.com/cloudfoundry-incubator/inigo/executor_runner"
	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("RunOnce", func() {
	var bbs *Bbs.BBS

	BeforeEach(func() {
		bbs = Bbs.New(etcdRunner.Adapter())
	})

	Context("when there is an executor running and a RunOnce is registered", func() {
		BeforeEach(func() {
			executorRunner.Start()
		})

		It("eventually runs the RunOnce", func() {
			guid := factories.GenerateGuid()
			runOnce := factories.BuildRunOnceWithRunAction(100, 100, inigoserver.CurlCommand(guid))
			bbs.DesireRunOnce(runOnce)

			Eventually(inigoserver.ReportingGuids, 5.0).Should(ContainElement(guid))
		})
	})

	Context("when there are no executors listening when a RunOnce is registered", func() {
		It("eventually runs the RunOnce once an executor comes up", func() {
			guid := factories.GenerateGuid()
			runOnce := factories.BuildRunOnceWithRunAction(100, 100, inigoserver.CurlCommand(guid))
			bbs.DesireRunOnce(runOnce)

			executorRunner.Start()

			Eventually(inigoserver.ReportingGuids, 5.0).Should(ContainElement(guid))
		})
	})

	Context("when no one picks up the RunOnce", func() {
		BeforeEach(func() {
			executorRunner.Start(executor_runner.Config{
				Stack:               "mickey-mouse",
				TimeToClaim:         1 * time.Second,
				ConvergenceInterval: 1 * time.Second,
			})
		})

		It("should be marked as failed, eventually", func() {
			guid := factories.GenerateGuid()
			runOnce := factories.BuildRunOnceWithRunAction(100, 100, inigoserver.CurlCommand(guid))
			runOnce.Stack = "donald-duck"
			bbs.DesireRunOnce(runOnce)

			Eventually(bbs.GetAllCompletedRunOnces, 5).Should(HaveLen(1))
			runOnces, err := bbs.GetAllCompletedRunOnces()
			Ω(err).ShouldNot(HaveOccurred())
			Ω(runOnces[0].Failed).Should(BeTrue(), "RunOnce should have failed")
			Ω(runOnces[0].FailureReason).Should(ContainSubstring("not claimed within time limit"))

			Ω(inigoserver.ReportingGuids()).Should(BeEmpty())
		})
	})

	Context("when an executor disappears", func() {
		var secondExecutor *executor_runner.ExecutorRunner

		BeforeEach(func() {
			secondExecutor = executor_runner.New(
				executorPath,
				gardenRunner.Network,
				gardenRunner.Addr,
				etcdRunner.NodeURLS(),
				"",
				"",
			)

			executorRunner.Start(executor_runner.Config{MemoryMB: 100, DiskMB: 100, ConvergenceInterval: 1 * time.Second})
			secondExecutor.Start(executor_runner.Config{ConvergenceInterval: 1 * time.Second, HeartbeatInterval: 1 * time.Second})
		})

		It("eventually marks jobs running on that executor as failed", func() {
			guid := factories.GenerateGuid()
			runOnce := factories.BuildRunOnceWithRunAction(1024, 1024, inigoserver.CurlCommand(guid)+"; sleep 10")
			bbs.DesireRunOnce(runOnce)
			Eventually(inigoserver.ReportingGuids, 5.0).Should(ContainElement(guid))

			secondExecutor.KillWithFire()

			Eventually(func() interface{} {
				runOnces, _ := bbs.GetAllCompletedRunOnces()
				return runOnces
			}, 5.0).Should(HaveLen(1))
			runOnces, _ := bbs.GetAllCompletedRunOnces()

			completedRunOnce := runOnces[0]
			Ω(completedRunOnce.Guid).Should(Equal(runOnce.Guid))
			Ω(completedRunOnce.Failed).To(BeTrue())
		})
	})
})
