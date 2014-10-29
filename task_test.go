package inigo_test

import (
	"fmt"
	"os"
	"time"

	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"
)

var _ = Describe("Task", func() {
	var executor ifrit.Process

	kickPendingDuration := 1 * time.Second

	Context("when an exec and rep are running", func() {
		BeforeEach(func() {
			executor = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
				{"exec", componentMaker.Executor("-memoryMB", "1024")},
				{"rep", componentMaker.Rep()},
			}))
		})

		AfterEach(func() {
			helpers.StopProcess(executor)
		})

		Context("and a standard Task is desired", func() {
			var task models.Task
			var thingWeRan string

			BeforeEach(func() {
				thingWeRan = "fake-" + factories.GenerateGuid()

				task = factories.BuildTaskWithRunAction(
					"inigo",
					componentMaker.Stack,
					512,
					512,
					"bash",
					[]string{
						"-c",
						// sleep a bit so that we can make assertions around behavior as it's
						// running
						fmt.Sprintf("curl %s; sleep 10", inigo_server.CurlArg(thingWeRan)),
					},
				)

				err := bbs.DesireTask(task)
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("eventually runs the Task", func() {
				Eventually(inigo_server.ReportingGuids).Should(ContainElement(thingWeRan))
			})

			Context("when a converger is running", func() {
				var converger ifrit.Process

				BeforeEach(func() {
					converger = ginkgomon.Invoke(componentMaker.Converger(
						"-convergeRepeatInterval", "1s",
						"-kickPendingTaskDuration", kickPendingDuration.String(),
					))
				})

				AfterEach(func() {
					helpers.StopProcess(converger)
				})

				Context("after the task starts", func() {
					BeforeEach(func() {
						Eventually(inigo_server.ReportingGuids).Should(ContainElement(thingWeRan))
					})

					Context("when the executor disappears", func() {
						BeforeEach(func() {
							helpers.StopProcess(executor)
						})

						It("eventually marks the task as failed", func() {
							// time is primarily influenced by rep's heartbeat interval
							Eventually(bbs.GetAllCompletedTasks, 10*time.Second).Should(HaveLen(1))

							tasks, err := bbs.GetAllCompletedTasks()
							Ω(err).ShouldNot(HaveOccurred())

							completedTask := tasks[0]
							Ω(completedTask.TaskGuid).Should(Equal(task.TaskGuid))
							Ω(completedTask.Failed).To(BeTrue())
						})
					})

					Context("and another task is desired, but cannot fit", func() {
						var secondTask models.Task
						var secondThingWeRan string

						BeforeEach(func() {
							secondThingWeRan = "fake-" + factories.GenerateGuid()

							secondTask = factories.BuildTaskWithRunAction(
								"inigo",
								componentMaker.Stack,
								768, // 768 + 512 is more than 1024, as we configured, so this won't fit
								512,
								"bash",
								[]string{"-c", fmt.Sprintf("curl %s && sleep 2", inigo_server.CurlArg(secondThingWeRan))},
							)

							err := bbs.DesireTask(secondTask)
							Ω(err).ShouldNot(HaveOccurred())
						})

						It("is executed once the first task completes, as its resources are cleared", func() {
							Eventually(bbs.GetAllCompletedTasks).Should(HaveLen(1)) // Wait for first task to complete
							Eventually(inigo_server.ReportingGuids, DEFAULT_EVENTUALLY_TIMEOUT+kickPendingDuration).Should(ContainElement(secondThingWeRan))
						})
					})
				})
			})
		})

		Context("and a docker-based Task is desired", func() {
			var announcement string
			var task models.Task

			BeforeEach(func() {
				announcement = "running-in-docker-" + factories.GenerateGuid()

				task = factories.BuildTaskWithRunAction(
					"inigo",
					componentMaker.Stack,
					100,
					100,
					"sh",
					[]string{
						"-c",
						// See github.com/cloudfoundry-incubator/diego-dockerfiles/blob/f9f1d75/inigodockertest/Dockerfile#L7
						"echo $SOME_VAR > /tmp/result.txt; wget " + inigo_server.CurlArg(announcement),
					},
				)
				task.ResultFile = "/tmp/result.txt"
				task.RootFSPath = "docker:///cloudfoundry/inigodockertest"

				err := bbs.DesireTask(task)
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("eventually runs and succeeds", func() {
				Eventually(bbs.GetAllCompletedTasks).Should(HaveLen(1))

				tasks, err := bbs.GetAllCompletedTasks()
				Ω(err).ShouldNot(HaveOccurred())

				firstTask := tasks[0]
				Ω(firstTask.TaskGuid).Should(Equal(task.TaskGuid))
				Ω(firstTask.Failed).Should(BeFalse(), "Task should not have failed")
				// See github.com/cloudfoundry-incubator/diego-dockerfiles/blob/f9f1d75/inigodockertest/Dockerfile#L7
				Ω(firstTask.Result).Should(Equal("some_docker_value\n"))
				Ω(inigo_server.ReportingGuids()).Should(ContainElement(announcement))
			})
		})
	})

	Context("when only a converger is running", func() {
		var converger ifrit.Process

		BeforeEach(func() {
			converger = ginkgomon.Invoke(componentMaker.Converger(
				"-convergeRepeatInterval", "1s",
				"-kickPendingTaskDuration", kickPendingDuration.String(),
			))
		})

		AfterEach(func() {
			helpers.StopProcess(converger)
		})

		Context("and a task is desired", func() {
			var thingWeRan string

			BeforeEach(func() {
				thingWeRan = "fake-" + factories.GenerateGuid()

				task := factories.BuildTaskWithRunAction(
					"inigo",
					componentMaker.Stack,
					512,
					512,
					"bash",
					[]string{"-c", fmt.Sprintf("curl %s && sleep 2", inigo_server.CurlArg(thingWeRan))},
				)

				err := bbs.DesireTask(task)
				Ω(err).ShouldNot(HaveOccurred())
			})

			Context("and then an exec and rep come up", func() {
				BeforeEach(func() {
					executor = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
						{"exec", componentMaker.Executor()},
						{"rep", componentMaker.Rep()},
					}))
				})

				AfterEach(func() {
					helpers.StopProcess(executor)
				})

				It("eventually runs the Task", func() {
					Eventually(inigo_server.ReportingGuids, DEFAULT_EVENTUALLY_TIMEOUT+kickPendingDuration).Should(ContainElement(thingWeRan))
				})
			})
		})
	})

	Context("when a very impatient converger is running", func() {
		var converger ifrit.Process

		BeforeEach(func() {
			converger = ginkgomon.Invoke(componentMaker.Converger(
				"-convergeRepeatInterval", "1s",
				"-expirePendingTaskDuration", "1s",
			))
		})

		AfterEach(func() {
			helpers.StopProcess(converger)
		})

		Context("and a task is desired", func() {
			var guid string

			BeforeEach(func() {
				guid = "fake-" + factories.GenerateGuid()

				task := factories.BuildTaskWithRunAction(
					"inigo",
					componentMaker.Stack,
					100,
					100,
					"curl",
					[]string{inigo_server.CurlArg(guid)},
				)

				err := bbs.DesireTask(task)
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("should be marked as failed after the expire duration", func() {
				Eventually(bbs.GetAllCompletedTasks).Should(HaveLen(1))

				tasks, err := bbs.GetAllCompletedTasks()
				Ω(err).ShouldNot(HaveOccurred())
				Ω(tasks[0].Failed).Should(BeTrue(), "Task should have failed")
				Ω(tasks[0].FailureReason).Should(ContainSubstring("not claimed within time limit"))

				Ω(inigo_server.ReportingGuids()).Should(BeEmpty())
			})
		})
	})
})
