package cell_test

import (
	"fmt"
	"net/http"
	"os"

	executor_api "github.com/cloudfoundry-incubator/executor"
	executor_client "github.com/cloudfoundry-incubator/executor/http/client"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/inigo_announcement_server"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"
)

var _ = Describe("Task", func() {
	var (
		auctioneerProcess ifrit.Process
		cellProcess       ifrit.Process
		convergerProcess  ifrit.Process
		receptorProcess   ifrit.Process
	)

	BeforeEach(func() {
		auctioneerProcess = nil
		cellProcess = nil
		convergerProcess = nil
		receptorProcess = ifrit.Invoke(componentMaker.Receptor())
	})

	AfterEach(func() {
		helpers.StopProcesses(
			auctioneerProcess,
			cellProcess,
			convergerProcess,
			receptorProcess,
		)
	})

	Context("when an exec, rep, and auctioneer are running", func() {
		var executorClient executor_api.Client

		BeforeEach(func() {
			cellProcess = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
				{"exec", componentMaker.Executor("-memoryMB", "1024")},
				{"rep", componentMaker.Rep()},
			}))

			auctioneerProcess = ginkgomon.Invoke(componentMaker.Auctioneer())

			executorClient = executor_client.New(http.DefaultClient, "http://"+componentMaker.Addresses.Executor)
		})

		Context("and a standard Task is desired", func() {
			var taskGuid string
			var taskSleepSeconds int

			BeforeEach(func() {
				taskGuid = factories.GenerateGuid()

				taskSleepSeconds = 10
			})

			JustBeforeEach(func() {
				err := receptorClient.CreateTask(receptor.TaskCreateRequest{
					Domain:   "inigo",
					TaskGuid: taskGuid,
					MemoryMB: 512,
					Stack:    componentMaker.Stack,
					Action: &models.RunAction{
						Path: "bash",
						Args: []string{
							"-c",
							// sleep a bit so that we can make assertions around behavior as it's running
							fmt.Sprintf("curl %s; sleep %d", inigo_announcement_server.AnnounceURL(taskGuid), taskSleepSeconds),
						},
					},
				})
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("eventually runs the Task", func() {
				Eventually(inigo_announcement_server.Announcements).Should(ContainElement(taskGuid))
			})

			Context("and then the task is cancelled", func() {
				BeforeEach(func() {
					taskSleepSeconds = 9999 // ensure task never completes on its own
				})

				JustBeforeEach(func() {
					Eventually(inigo_announcement_server.Announcements).Should(ContainElement(taskGuid))

					err := receptorClient.CancelTask(taskGuid)
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("marks the task as complete, failed and cancelled", func() {
					taskAfterCancel, err := receptorClient.GetTask(taskGuid)
					Ω(err).ShouldNot(HaveOccurred())

					Ω(taskAfterCancel.State).Should(Equal(receptor.TaskStateCompleted))
					Ω(taskAfterCancel.Failed).Should(BeTrue())
					Ω(taskAfterCancel.FailureReason).Should(Equal("task was cancelled"))
				})

				It("cleans up the task's container before the task completes on its own", func() {
					Eventually(func() error {
						_, err := executorClient.GetContainer(taskGuid)
						return err
					}).Should(Equal(executor_api.ErrContainerNotFound))
				})
			})

			Context("when a converger is running", func() {
				BeforeEach(func() {
					convergerProcess = ginkgomon.Invoke(componentMaker.Converger(
						"-convergeRepeatInterval", "1s",
						"-kickPendingTaskDuration", "1s",
					))
				})

				Context("after the task starts", func() {
					JustBeforeEach(func() {
						Eventually(inigo_announcement_server.Announcements).Should(ContainElement(taskGuid))
					})

					Context("when the cellProcess disappears", func() {
						JustBeforeEach(func() {
							helpers.StopProcesses(cellProcess)
						})

						It("eventually marks the task as failed", func() {
							// time is primarily influenced by rep's heartbeat interval
							var completedTask receptor.TaskResponse
							Eventually(func() interface{} {
								var err error

								completedTask, err = receptorClient.GetTask(taskGuid)
								Ω(err).ShouldNot(HaveOccurred())

								return completedTask.State
							}).Should(Equal(receptor.TaskStateCompleted))

							Ω(completedTask.Failed).To(BeTrue())
						})
					})
				})
			})
		})

		Context("and a docker-based Task is desired", func() {
			var taskGuid string

			BeforeEach(func() {
				taskGuid = factories.GenerateGuid()

				err := receptorClient.CreateTask(receptor.TaskCreateRequest{
					Domain:     "inigo",
					TaskGuid:   taskGuid,
					Stack:      componentMaker.Stack,
					MemoryMB:   768,
					ResultFile: "/tmp/result.txt",
					RootFSPath: "docker:///cloudfoundry/inigodockertest",
					Action: &models.RunAction{
						Path: "sh",
						Args: []string{
							"-c",
							// See github.com/cloudfoundry-incubator/diego-dockerfiles/blob/f9f1d75/inigodockertest/Dockerfile#L7
							"echo $SOME_VAR > /tmp/result.txt",
						},
					},
				})
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("eventually runs and succeeds", func() {
				var completedTask receptor.TaskResponse
				Eventually(func() interface{} {
					var err error

					completedTask, err = receptorClient.GetTask(taskGuid)
					Ω(err).ShouldNot(HaveOccurred())

					return completedTask.State
				}).Should(Equal(receptor.TaskStateCompleted))

				Ω(completedTask.Failed).Should(BeFalse())
				Ω(completedTask.Result).Should(Equal("some_docker_value\n"))
			})
		})
	})

	Context("when an auctioneer is not running", func() {
		BeforeEach(func() {
			convergerProcess = ginkgomon.Invoke(componentMaker.Converger(
				"-convergeRepeatInterval", "1s",
				"-kickPendingTaskDuration", "1s",
			))

			cellProcess = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
				{"exec", componentMaker.Executor()},
				{"rep", componentMaker.Rep()},
			}))
		})

		Context("and a task is desired", func() {
			var taskGuid string

			BeforeEach(func() {
				taskGuid = factories.GenerateGuid()

				err := receptorClient.CreateTask(receptor.TaskCreateRequest{
					Domain:   "inigo",
					TaskGuid: taskGuid,
					Stack:    componentMaker.Stack,
					Action: &models.RunAction{
						Path: "curl",
						Args: []string{inigo_announcement_server.AnnounceURL(taskGuid)},
					},
				})
				Ω(err).ShouldNot(HaveOccurred())
			})

			Context("and then an auctioneer come up", func() {
				BeforeEach(func() {
					auctioneerProcess = ginkgomon.Invoke(componentMaker.Auctioneer())

				AfterEach(func() {
					helpers.StopProcesses(cellProcess)
				})

				It("eventually runs the Task", func() {
					Eventually(inigo_announcement_server.Announcements).Should(ContainElement(taskGuid))
				})
			})
		})
	})

	Context("when a very impatient converger is running", func() {
		BeforeEach(func() {
			convergerProcess = ginkgomon.Invoke(componentMaker.Converger(
				"-convergeRepeatInterval", "1s",
				"-expirePendingTaskDuration", "1s",
			))
		})

		Context("and a task is desired", func() {
			var taskGuid string

			BeforeEach(func() {
				taskGuid = factories.GenerateGuid()

				err := receptorClient.CreateTask(receptor.TaskCreateRequest{
					Domain:   "inigo",
					TaskGuid: taskGuid,
					Stack:    componentMaker.Stack,
					Action: &models.RunAction{
						Path: "curl",
						Args: []string{inigo_announcement_server.AnnounceURL(taskGuid)},
					},
				})
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("should be marked as failed after the expire duration", func() {
				var completedTask receptor.TaskResponse
				Eventually(func() interface{} {
					var err error

					completedTask, err = receptorClient.GetTask(taskGuid)
					Ω(err).ShouldNot(HaveOccurred())

					return completedTask.State
				}).Should(Equal(receptor.TaskStateCompleted))

				Ω(completedTask.Failed).Should(BeTrue())
				Ω(completedTask.FailureReason).Should(ContainSubstring("not started within time limit"))

				Ω(inigo_announcement_server.Announcements()).Should(BeEmpty())
			})
		})
	})
})
