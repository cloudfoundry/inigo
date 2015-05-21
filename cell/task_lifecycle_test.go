package cell_test

import (
	"fmt"
	"os"

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

var _ = Describe("Task Lifecycle", func() {
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

	Context("when a rep, and auctioneer are running", func() {
		BeforeEach(func() {
			cellProcess = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
				{"rep", componentMaker.Rep("-memoryMB", "1024")},
			}))

			auctioneerProcess = ginkgomon.Invoke(componentMaker.Auctioneer())
		})

		Context("and a standard Task is desired", func() {
			var taskGuid string
			var taskSleepSeconds int

			var taskRequest receptor.TaskCreateRequest

			BeforeEach(func() {
				taskSleepSeconds = 10
				taskGuid = helpers.GenerateGuid()

				taskRequest = helpers.TaskCreateRequestWithMemory(
					taskGuid,
					&models.RunAction{
						Path: "sh",
						Args: []string{
							"-c",
							// sleep a bit so that we can make assertions around behavior as it's running
							fmt.Sprintf("curl %s; sleep %d", inigo_announcement_server.AnnounceURL(taskGuid), taskSleepSeconds),
						},
					},
					512,
				)
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
				err := receptorClient.CreateTask(taskRequest)
				Expect(err).NotTo(HaveOccurred())
			})

			Context("when there is a matching rootfs", func() {
				It("eventually runs the Task", func() {
					Eventually(inigo_announcement_server.Announcements).Should(ContainElement(taskGuid))
				})
			})

			Context("when there is no matching rootfs", func() {
				BeforeEach(func() {
					taskRequest = helpers.TaskCreateRequestWithRootFS(
						taskGuid,
						helpers.BogusPreloadedRootFS,
						&models.RunAction{
							Path: "true",
						},
					)
				})

				It("marks the task as complete, failed and cancelled", func() {
					Eventually(theFailureReason).Should(Equal(diego_errors.CELL_MISMATCH_MESSAGE))
				})
			})

			Context("when there is not enough resources", func() {
				BeforeEach(func() {
					taskRequest = helpers.TaskCreateRequestWithMemory(
						taskGuid,
						&models.RunAction{
							Path: "sh",
							Args: []string{
								"-c",
								// sleep a bit so that we can make assertions around behavior as it's running
								fmt.Sprintf("curl %s; sleep %d", inigo_announcement_server.AnnounceURL(taskGuid), taskSleepSeconds),
							},
						},
						2048,
					)
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
					Expect(err).NotTo(HaveOccurred())
				})

				It("marks the task as complete, failed and cancelled", func() {
					taskAfterCancel, err := receptorClient.GetTask(taskGuid)
					Expect(err).NotTo(HaveOccurred())

					Expect(taskAfterCancel.State).To(Equal(receptor.TaskStateCompleted))
					Expect(taskAfterCancel.Failed).To(BeTrue())
					Expect(taskAfterCancel.FailureReason).To(Equal("task was cancelled"))
				})
			})

			Context("when a converger is running", func() {
				BeforeEach(func() {
					convergerProcess = ginkgomon.Invoke(componentMaker.Converger(
						"-convergeRepeatInterval", "1s",
						"-kickTaskDuration", "1s",
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
								Expect(err).NotTo(HaveOccurred())

								return completedTask.State
							}).Should(Equal(receptor.TaskStateCompleted))

							Expect(completedTask.Failed).To(BeTrue())
						})
					})
				})
			})
		})

		Context("Egress Rules", func() {
			var (
				taskGuid          string
				taskCreateRequest receptor.TaskCreateRequest
			)

			BeforeEach(func() {
				taskGuid = helpers.GenerateGuid()
				taskCreateRequest = helpers.TaskCreateRequest(
					taskGuid,
					&models.RunAction{
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
				)
				taskCreateRequest.ResultFile = "/tmp/result"
			})

			JustBeforeEach(func() {
				err := receptorClient.CreateTask(taskCreateRequest)
				Expect(err).NotTo(HaveOccurred())
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
				"-kickTaskDuration", "1s",
			))

			cellProcess = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
				{"rep", componentMaker.Rep()},
			}))
		})

		Context("and a task is desired", func() {
			var taskGuid string

			BeforeEach(func() {
				taskGuid = helpers.GenerateGuid()

				err := receptorClient.CreateTask(helpers.TaskCreateRequest(
					taskGuid,
					&models.RunAction{
						Path: "curl",
						Args: []string{inigo_announcement_server.AnnounceURL(taskGuid)},
					},
				))
				Expect(err).NotTo(HaveOccurred())
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

				err := receptorClient.CreateTask(helpers.TaskCreateRequest(
					taskGuid,
					&models.RunAction{
						Path: "curl",
						Args: []string{inigo_announcement_server.AnnounceURL(taskGuid)},
					},
				))
				Expect(err).NotTo(HaveOccurred())
			})

			It("should be marked as failed after the expire duration", func() {
				var completedTask receptor.TaskResponse
				Eventually(func() interface{} {
					var err error

					completedTask, err = receptorClient.GetTask(taskGuid)
					Expect(err).NotTo(HaveOccurred())

					return completedTask.State
				}).Should(Equal(receptor.TaskStateCompleted))

				Expect(completedTask.Failed).To(BeTrue())
				Expect(completedTask.FailureReason).To(ContainSubstring("not started within time limit"))

				Expect(inigo_announcement_server.Announcements()).To(BeEmpty())
			})
		})
	})
})

func pollTaskStatus(taskGuid string, result string) {
	var completedTask receptor.TaskResponse
	Eventually(func() interface{} {
		var err error

		completedTask, err = receptorClient.GetTask(taskGuid)
		Expect(err).NotTo(HaveOccurred())

		return completedTask.State
	}).Should(Equal(receptor.TaskStateCompleted))

	Expect(completedTask.Result).To(Equal(result))
}
