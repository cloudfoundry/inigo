package inigo_test

import (
	"time"

	"github.com/cloudfoundry/gunk/timeprovider"

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
		bbs = Bbs.New(suiteContext.EtcdRunner.Adapter(), timeprovider.NewTimeProvider())
	})

	Context("when there is an executor running and a RunOnce is registered", func() {
		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
		})

		It("eventually runs the RunOnce", func() {
			guid := factories.GenerateGuid()
			runOnce := factories.BuildRunOnceWithRunAction(suiteContext.ExecutorRunner.Config.Stack, 100, 100, inigo_server.CurlCommand(guid))
			bbs.DesireRunOnce(runOnce)

			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(guid))
		})
	})

	Context("when there are no executors listening when a RunOnce is registered", func() {
		It("eventually runs the RunOnce once an executor comes up", func() {
			guid := factories.GenerateGuid()
			runOnce := factories.BuildRunOnceWithRunAction(suiteContext.ExecutorRunner.Config.Stack, 100, 100, inigo_server.CurlCommand(guid))
			bbs.DesireRunOnce(runOnce)

			suiteContext.ExecutorRunner.Start()

			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(guid))
		})
	})

	Context("when no one picks up the RunOnce", func() {
		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start(executor_runner.Config{
				Stack:               "mickey-mouse",
				TimeToClaim:         1 * time.Second,
				ConvergenceInterval: 1 * time.Second,
			})
		})

		It("should be marked as failed, eventually", func() {
			guid := factories.GenerateGuid()
			runOnce := factories.BuildRunOnceWithRunAction(suiteContext.ExecutorRunner.Config.Stack, 100, 100, inigo_server.CurlCommand(guid))
			runOnce.Stack = "donald-duck"
			bbs.DesireRunOnce(runOnce)

			Eventually(bbs.GetAllCompletedRunOnces, 5).Should(HaveLen(1))
			runOnces, err := bbs.GetAllCompletedRunOnces()
			Ω(err).ShouldNot(HaveOccurred())
			Ω(runOnces[0].Failed).Should(BeTrue(), "RunOnce should have failed")
			Ω(runOnces[0].FailureReason).Should(ContainSubstring("not claimed within time limit"))

			Ω(inigo_server.ReportingGuids()).Should(BeEmpty())
		})
	})

	Context("when an executor disappears", func() {
		var secondExecutor *executor_runner.ExecutorRunner

		BeforeEach(func() {
			secondExecutor = executor_runner.New(
				suiteContext.SharedContext.ExecutorPath,
				suiteContext.SharedContext.WardenNetwork,
				suiteContext.SharedContext.WardenAddr,
				suiteContext.EtcdRunner.NodeURLS(),
				"",
				"",
			)

			suiteContext.ExecutorRunner.Start(executor_runner.Config{MemoryMB: 100, DiskMB: 100, ConvergenceInterval: 1 * time.Second})
			secondExecutor.Start(executor_runner.Config{ConvergenceInterval: 1 * time.Second, HeartbeatInterval: 1 * time.Second})
		})

		It("eventually marks jobs running on that executor as failed", func() {
			guid := factories.GenerateGuid()
			runOnce := factories.BuildRunOnceWithRunAction(suiteContext.ExecutorRunner.Config.Stack, 1024, 1024, inigo_server.CurlCommand(guid)+"; sleep 10")
			bbs.DesireRunOnce(runOnce)
			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(guid))

			secondExecutor.KillWithFire()

			Eventually(func() interface{} {
				runOnces, _ := bbs.GetAllCompletedRunOnces()
				return runOnces
			}, LONG_TIMEOUT).Should(HaveLen(1))
			runOnces, _ := bbs.GetAllCompletedRunOnces()

			completedRunOnce := runOnces[0]
			Ω(completedRunOnce.Guid).Should(Equal(runOnce.Guid))
			Ω(completedRunOnce.Failed).To(BeTrue())
		})
	})
})
