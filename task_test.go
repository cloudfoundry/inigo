package inigo_test

import (
	"fmt"
	"net/http"
	"os"
	"time"

	executor_api "github.com/cloudfoundry-incubator/executor"
	executor_client "github.com/cloudfoundry-incubator/executor/http/client"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/inigo_announcement_server"
	receptor_api "github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"
)

var _ = Describe("Task", func() {
	var cell ifrit.Process
	var diegoClient receptor_api.Client
	var executorClient executor_api.Client

	kickPendingDuration := 1 * time.Second

	Context("when an exec and rep are running", func() {
		BeforeEach(func() {
			cell = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
				{"exec", componentMaker.Executor("-memoryMB", "1024")},
				{"rep", componentMaker.Rep()},
				{"receptor", componentMaker.Receptor()},
			}))
			diegoClient = receptor_api.NewClient("http://" + componentMaker.Addresses.Receptor)
			executorClient = executor_client.New(http.DefaultClient, "http://"+componentMaker.Addresses.Executor)
		})

		AfterEach(func() {
			helpers.StopProcesses(cell)
		})

		Context("and a standard Task is desired", func() {
			var task models.Task
			var thingWeRan string
			var taskSleepSeconds int

			BeforeEach(func() {
				taskSleepSeconds = 10
			})

			JustBeforeEach(func() {
				thingWeRan = "fake-" + factories.GenerateGuid()

				task = factories.BuildTaskWithRunAction(
					"inigo",
					componentMaker.Stack,
					512,
					512,
					"bash",
					[]string{
						"-c",
						// sleep a bit so that we can make assertions around behavior as it's running
						fmt.Sprintf("curl %s; sleep %d", inigo_announcement_server.AnnounceURL(thingWeRan), taskSleepSeconds),
					},
				)

				err := bbs.DesireTask(task)
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("eventually runs the Task", func() {
				Eventually(inigo_announcement_server.Announcements).Should(ContainElement(thingWeRan))
			})

			Context("and then the task is cancelled", func() {
				BeforeEach(func() {
					taskSleepSeconds = 9999 // ensure task never completes on its own
				})

				JustBeforeEach(func() {
					Eventually(inigo_announcement_server.Announcements).Should(ContainElement(thingWeRan))
					err := diegoClient.CancelTask(task.TaskGuid)
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("marks the task as complete, failed and cancelled in BBS", func() {
					taskAfterCancel, err := bbs.TaskByGuid(task.TaskGuid)
					Ω(err).ShouldNot(HaveOccurred())
					Ω(taskAfterCancel.State).Should(Equal(models.TaskStateCompleted))
					Ω(taskAfterCancel.Failed).Should(BeTrue())
					Ω(taskAfterCancel.FailureReason).Should(Equal("task was cancelled"))
				})

				It("cleans up the task's container before the task completes on its own", func() {
					Eventually(func() error {
						_, err := executorClient.GetContainer(task.TaskGuid)
						return err
					}).Should(Equal(executor_api.ErrContainerNotFound))
				})
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
					helpers.StopProcesses(converger)
				})

				Context("after the task starts", func() {
					JustBeforeEach(func() {
						Eventually(inigo_announcement_server.Announcements).Should(ContainElement(thingWeRan))
					})

					Context("when the cell disappears", func() {
						JustBeforeEach(func() {
							helpers.StopProcesses(cell)
						})

						It("eventually marks the task as failed", func() {
							// time is primarily influenced by rep's heartbeat interval
							Eventually(bbs.CompletedTasks, 10*time.Second).Should(HaveLen(1))

							tasks, err := bbs.CompletedTasks()
							Ω(err).ShouldNot(HaveOccurred())

							completedTask := tasks[0]
							Ω(completedTask.TaskGuid).Should(Equal(task.TaskGuid))
							Ω(completedTask.Failed).To(BeTrue())
						})
					})

					Context("and another task is desired, but cannot fit", func() {
						var secondTask models.Task
						var secondThingWeRan string

						JustBeforeEach(func() {
							secondThingWeRan = "fake-" + factories.GenerateGuid()

							secondTask = factories.BuildTaskWithRunAction(
								"inigo",
								componentMaker.Stack,
								768, // 768 + 512 is more than 1024, as we configured, so this won't fit
								512,
								"bash",
								[]string{"-c", fmt.Sprintf("curl %s && sleep 2", inigo_announcement_server.AnnounceURL(secondThingWeRan))},
							)

							err := bbs.DesireTask(secondTask)
							Ω(err).ShouldNot(HaveOccurred())
						})

						It("is executed once the first task completes, as its resources are cleared", func() {
							Eventually(bbs.CompletedTasks).Should(HaveLen(1)) // Wait for first task to complete
							Eventually(inigo_announcement_server.Announcements, DEFAULT_EVENTUALLY_TIMEOUT+kickPendingDuration).Should(ContainElement(secondThingWeRan))
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
						"echo $SOME_VAR > /tmp/result.txt; wget " + inigo_announcement_server.AnnounceURL(announcement),
					},
				)
				task.ResultFile = "/tmp/result.txt"
				task.RootFSPath = "docker:///cloudfoundry/inigodockertest"

				err := bbs.DesireTask(task)
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("eventually runs and succeeds", func() {
				Eventually(bbs.CompletedTasks).Should(HaveLen(1))

				tasks, err := bbs.CompletedTasks()
				Ω(err).ShouldNot(HaveOccurred())

				firstTask := tasks[0]
				Ω(firstTask.TaskGuid).Should(Equal(task.TaskGuid))
				Ω(firstTask.Failed).Should(BeFalse(), "Task should not have failed")
				// See github.com/cloudfoundry-incubator/diego-dockerfiles/blob/f9f1d75/inigodockertest/Dockerfile#L7
				Ω(firstTask.Result).Should(Equal("some_docker_value\n"))
				Ω(inigo_announcement_server.Announcements()).Should(ContainElement(announcement))
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
			helpers.StopProcesses(converger)
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
					[]string{"-c", fmt.Sprintf("curl %s && sleep 2", inigo_announcement_server.AnnounceURL(thingWeRan))},
				)

				err := bbs.DesireTask(task)
				Ω(err).ShouldNot(HaveOccurred())
			})

			Context("and then an exec and rep come up", func() {
				BeforeEach(func() {
					cell = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
						{"exec", componentMaker.Executor()},
						{"rep", componentMaker.Rep()},
					}))
				})

				AfterEach(func() {
					helpers.StopProcesses(cell)
				})

				It("eventually runs the Task", func() {
					Eventually(inigo_announcement_server.Announcements, DEFAULT_EVENTUALLY_TIMEOUT+kickPendingDuration).Should(ContainElement(thingWeRan))
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
			helpers.StopProcesses(converger)
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
					[]string{inigo_announcement_server.AnnounceURL(guid)},
				)

				err := bbs.DesireTask(task)
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("should be marked as failed after the expire duration", func() {
				Eventually(bbs.CompletedTasks).Should(HaveLen(1))

				tasks, err := bbs.CompletedTasks()
				Ω(err).ShouldNot(HaveOccurred())
				Ω(tasks[0].Failed).Should(BeTrue(), "Task should have failed")
				Ω(tasks[0].FailureReason).Should(ContainSubstring("not claimed within time limit"))

				Ω(inigo_announcement_server.Announcements()).Should(BeEmpty())
			})
		})
	})
})
