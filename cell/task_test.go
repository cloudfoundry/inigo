package cell_test

import (
	"fmt"
	"os"

	executor_api "github.com/cloudfoundry-incubator/executor"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/inigo_announcement_server"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/runtime-schema/diego_errors"
	"github.com/cloudfoundry-incubator/runtime-schema/models"

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
	)

	BeforeEach(func() {
		auctioneerProcess = nil
		cellProcess = nil
		convergerProcess = nil
	})

	AfterEach(func() {
		helpers.StopProcesses(
			auctioneerProcess,
			cellProcess,
			convergerProcess,
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

			executorClient = componentMaker.ExecutorClient()
		})

		Context("and a standard Task is desired", func() {
			var taskGuid string
			var taskSleepSeconds int
			var rootfs string
			var memory int

			BeforeEach(func() {
				taskSleepSeconds = 10
				taskGuid = helpers.GenerateGuid()
				rootfs = componentMaker.PreloadedRootFS()
				memory = 512
			})

			theFailureReason := func() string {
				taskAfterCancel, err := receptorClient.GetTask(taskGuid)
				if err != nil {
					return ""
				}

				if taskAfterCancel.State != receptor.TaskStateCompleted {
					return ""
				}

				if taskAfterCancel.Failed != true {
					return ""
				}

				return taskAfterCancel.FailureReason
			}

			JustBeforeEach(func() {
				err := receptorClient.CreateTask(receptor.TaskCreateRequest{
					Domain:   INIGO_DOMAIN,
					TaskGuid: taskGuid,
					MemoryMB: memory,
					RootFS:   rootfs,
					Action: &models.RunAction{
						Path: "sh",
						Args: []string{
							"-c",
							// sleep a bit so that we can make assertions around behavior as it's running
							fmt.Sprintf("curl %s; sleep %d", inigo_announcement_server.AnnounceURL(taskGuid), taskSleepSeconds),
						},
					},
				})
				Ω(err).ShouldNot(HaveOccurred())
			})

			Context("when there is a matching rootfs", func() {
				It("eventually runs the Task", func() {
					Eventually(inigo_announcement_server.Announcements).Should(ContainElement(taskGuid))
				})
			})

			Context("when there is no matching rootfs", func() {
				BeforeEach(func() {
					rootfs = "preloaded:bogus-stack"
				})

				It("marks the task as complete, failed and cancelled", func() {
					Eventually(theFailureReason).Should(Equal(diego_errors.CELL_MISMATCH_MESSAGE))
				})
			})

			Context("when there is not enough resources", func() {
				BeforeEach(func() {
					memory = 2048
				})

				It("marks the task as complete, failed and cancelled", func() {
					Eventually(theFailureReason).Should(Equal(diego_errors.INSUFFICIENT_RESOURCES_MESSAGE))
				})
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

		Context("Egress Rules", func() {
			var (
				taskGuid          string
				taskSleepSeconds  int
				announcement      string
				taskCreateRequest receptor.TaskCreateRequest
			)

			BeforeEach(func() {
				taskGuid = helpers.GenerateGuid()
				announcement = fmt.Sprintf("%s-0", taskGuid)
				taskSleepSeconds = 10
				taskCreateRequest = receptor.TaskCreateRequest{
					Domain:   INIGO_DOMAIN,
					TaskGuid: taskGuid,
					MemoryMB: 512,
					RootFS:   componentMaker.PreloadedRootFS(),
					Action: &models.RunAction{
						Path: "sh",
						Args: []string{
							"-c",
							`
curl -s --connect-timeout 5 http://www.example.com -o /dev/null
echo $? >> /tmp/result
exit 0
					`,
						},
					},
					ResultFile: "/tmp/result",
				}
			})

			JustBeforeEach(func() {
				err := receptorClient.CreateTask(taskCreateRequest)
				Ω(err).ShouldNot(HaveOccurred())
			})

			Context("default networking", func() {
				It("rejects outbound tcp traffic", func() {
					// Failed to connect to host
					pollTaskStatus(taskGuid, "28\n")
				})
			})

			Context("with appropriate security group setting", func() {
				BeforeEach(func() {
					taskCreateRequest.EgressRules = []models.SecurityGroupRule{
						{
							Protocol:     models.TCPProtocol,
							Destinations: []string{"9.0.0.0-89.255.255.255", "90.0.0.0-94.0.0.0"},
							Ports:        []uint16{80, 443},
						},
						{
							Protocol:     models.UDPProtocol,
							Destinations: []string{"0.0.0.0/0"},
							PortRange: &models.PortRange{
								Start: 53,
								End:   53,
							},
						},
					}
				})

				It("allows outbound tcp traffic", func() {
					pollTaskStatus(taskGuid, "0\n")
				})
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
				taskGuid = helpers.GenerateGuid()

				err := receptorClient.CreateTask(receptor.TaskCreateRequest{
					Domain:   INIGO_DOMAIN,
					TaskGuid: taskGuid,
					RootFS:   componentMaker.PreloadedRootFS(),
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
				})

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
				taskGuid = helpers.GenerateGuid()

				err := receptorClient.CreateTask(receptor.TaskCreateRequest{
					Domain:   INIGO_DOMAIN,
					TaskGuid: taskGuid,
					RootFS:   componentMaker.PreloadedRootFS(),
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

func pollTaskStatus(taskGuid string, result string) {
	var completedTask receptor.TaskResponse
	Eventually(func() interface{} {
		var err error

		completedTask, err = receptorClient.GetTask(taskGuid)
		Ω(err).ShouldNot(HaveOccurred())

		return completedTask.State
	}).Should(Equal(receptor.TaskStateCompleted))

	Ω(completedTask.Result).Should(Equal(result))
}
