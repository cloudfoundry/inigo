package inigo_test

import (
	"fmt"
	"strings"
	"time"

	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	steno "github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/gunk/timeprovider"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Task", func() {
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

	Context("when there is an executor running and a Task is registered", func() {
		var guid string

		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
			suiteContext.RepRunner.Start()
			suiteContext.ConvergerRunner.Start(5*time.Second, 5*time.Second, 30*time.Minute, 30*time.Second, 300*time.Second)

			guid = factories.GenerateGuid()
			task := factories.BuildTaskWithRunAction(
				suiteContext.RepStack,
				512,
				512,
				"curl",
				inigo_server.CurlArgs(guid),
			)
			bbs.DesireTask(task)
		})

		It("eventually runs the Task", func() {
			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(guid))
		})
	})

	Context("when there is no room for a desired task, but room becomes available eventually", func() {
		var firstTaskGuid string
		var secondTaskGuid string

		BeforeEach(func() {
			suiteContext.ExecutorRunner.Start()
			suiteContext.RepRunner.Start()
			suiteContext.ConvergerRunner.Start(5*time.Second, 5*time.Second, 30*time.Minute, 30*time.Second, 300*time.Second)

			firstTaskGuid = factories.GenerateGuid()

			task := factories.BuildTaskWithRunAction(
				suiteContext.RepStack,
				512,
				512,
				"bash",
				[]string{"-c", fmt.Sprintf("curl %s && sleep 2", strings.Join(inigo_server.CurlArgs(firstTaskGuid), " "))},
			)
			bbs.DesireTask(task)

			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(firstTaskGuid))

			secondTaskGuid = factories.GenerateGuid()

			task = factories.BuildTaskWithRunAction(
				suiteContext.RepStack,
				768,
				768,
				"curl",
				inigo_server.CurlArgs(secondTaskGuid),
			)

			bbs.DesireTask(task)
		})

		It("is executed, as the previous task's resources are cleared", func() {
			Eventually(bbs.GetAllCompletedTasks, LONG_TIMEOUT).Should(HaveLen(1))

			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(secondTaskGuid))
		})
	})

	Context("when there are no executors listening when a Task is registered", func() {
		BeforeEach(func() {
			suiteContext.ConvergerRunner.Start(5*time.Second, 5*time.Second, 60*time.Second, 30*time.Second, 300*time.Second)
		})

		It("eventually runs the Task once an executor comes up", func() {
			guid := factories.GenerateGuid()
			task := factories.BuildTaskWithRunAction(
				suiteContext.RepStack,
				100,
				100,
				"curl",
				inigo_server.CurlArgs(guid),
			)
			bbs.DesireTask(task)

			suiteContext.ExecutorRunner.Start()
			suiteContext.RepRunner.Start()

			Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(guid))
		})
	})

	Context("when no one picks up the Task", func() {
		BeforeEach(func() {
			suiteContext.ConvergerRunner.Start(5*time.Second, 5*time.Second, 1*time.Second, 30*time.Second, 300*time.Second)
		})

		It("should be marked as failed, eventually", func() {
			guid := factories.GenerateGuid()
			task := factories.BuildTaskWithRunAction(
				suiteContext.RepStack,
				100,
				100,
				"curl",
				inigo_server.CurlArgs(guid),
			)
			task.Stack = "donald-duck"
			bbs.DesireTask(task)

			Eventually(bbs.GetAllCompletedTasks, LONG_TIMEOUT).Should(HaveLen(1))
			tasks, err := bbs.GetAllCompletedTasks()
			Ω(err).ShouldNot(HaveOccurred())
			Ω(tasks[0].Failed).Should(BeTrue(), "Task should have failed")
			Ω(tasks[0].FailureReason).Should(ContainSubstring("not claimed within time limit"))

			Ω(inigo_server.ReportingGuids()).Should(BeEmpty())
		})
	})

	Context("when an executor disappears", func() {
		BeforeEach(func() {
			suiteContext.ConvergerRunner.Start(5*time.Second, 5*time.Second, 10*time.Second, 30*time.Second, 300*time.Second)
			suiteContext.ExecutorRunner.Start()
			suiteContext.RepRunner.Start()
		})

		It("eventually marks jobs running on that executor as failed", func() {
			guid := factories.GenerateGuid()
			task := factories.BuildTaskWithRunAction(
				suiteContext.RepStack,
				1024,
				1024,
				"bash",
				[]string{"-c", fmt.Sprintf("curl %s; sleep 10", strings.Join(inigo_server.CurlArgs(guid), " "))},
			)
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
