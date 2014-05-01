package inigo_test

import (
	"time"

	"github.com/cloudfoundry/gunk/timeprovider"

	"github.com/cloudfoundry-incubator/executor/integration/executor_runner"
	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Task", func() {
	var bbs *Bbs.BBS

	BeforeEach(func() {
		bbs = Bbs.New(suiteContext.EtcdRunner.Adapter(), timeprovider.NewTimeProvider())
	})

	Context("when there is an executor running and a Task is registered", func() {
		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
		})

		It("eventually runs the Task", func() {
			guid := factories.GenerateGuid()
			task := factories.BuildTaskWithRunAction(suiteContext.ExecutorRunner.Config.Stack, 100, 100, inigo_server.CurlCommand(guid))
			bbs.DesireTask(task)

			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(guid))
		})
	})

	Context("when there are no executors listening when a Task is registered", func() {
		It("eventually runs the Task once an executor comes up", func() {
			guid := factories.GenerateGuid()
			task := factories.BuildTaskWithRunAction(suiteContext.ExecutorRunner.Config.Stack, 100, 100, inigo_server.CurlCommand(guid))
			bbs.DesireTask(task)

			suiteContext.ExecutorRunner.Start()

			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(guid))
		})
	})

	Context("when no one picks up the Task", func() {
		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start(executor_runner.Config{
				Stack:               "mickey-mouse",
				TimeToClaim:         1 * time.Second,
				ConvergenceInterval: 1 * time.Second,
			})
		})

		It("should be marked as failed, eventually", func() {
			guid := factories.GenerateGuid()
			task := factories.BuildTaskWithRunAction(suiteContext.ExecutorRunner.Config.Stack, 100, 100, inigo_server.CurlCommand(guid))
			task.Stack = "donald-duck"
			bbs.DesireTask(task)

			Eventually(bbs.GetAllCompletedTasks, 5).Should(HaveLen(1))
			tasks, err := bbs.GetAllCompletedTasks()
			Ω(err).ShouldNot(HaveOccurred())
			Ω(tasks[0].Failed).Should(BeTrue(), "Task should have failed")
			Ω(tasks[0].FailureReason).Should(ContainSubstring("not claimed within time limit"))

			Ω(inigo_server.ReportingGuids()).Should(BeEmpty())
		})
	})

	Context("when an executor disappears", func() {
		var convergingExecutor *executor_runner.ExecutorRunner

		BeforeEach(func() {
			convergingExecutor = executor_runner.New(
				suiteContext.SharedContext.ExecutorPath,
				suiteContext.SharedContext.WardenNetwork,
				suiteContext.SharedContext.WardenAddr,
				suiteContext.EtcdRunner.NodeURLS(),
				"",
				"",
			)

			convergingExecutor.Start(executor_runner.Config{
				// so the task does not get scheduled on the converger
				MemoryMB: "1",
				DiskMB:   "1",

				ConvergenceInterval: 1 * time.Second,
				ContainerOwnerName:  "converging-executor",
			})

			suiteContext.ExecutorRunner.Start(executor_runner.Config{
				ConvergenceInterval: 1 * time.Second,
				HeartbeatInterval:   1 * time.Second,
			})
		})

		It("eventually marks jobs running on that executor as failed", func() {
			guid := factories.GenerateGuid()
			task := factories.BuildTaskWithRunAction(suiteContext.ExecutorRunner.Config.Stack, 1024, 1024, inigo_server.CurlCommand(guid)+"; sleep 10")
			bbs.DesireTask(task)
			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(guid))

			suiteContext.ExecutorRunner.KillWithFire()

			Eventually(func() interface{} {
				tasks, _ := bbs.GetAllCompletedTasks()
				return tasks
			}, LONG_TIMEOUT).Should(HaveLen(1))
			tasks, _ := bbs.GetAllCompletedTasks()

			completedTask := tasks[0]
			Ω(completedTask.Guid).Should(Equal(task.Guid))
			Ω(completedTask.Failed).To(BeTrue())
		})
	})
})
