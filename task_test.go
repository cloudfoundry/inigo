package inigo_test

import (
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/cloudfoundry-incubator/garden/warden"
	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	"github.com/cloudfoundry/gunk/timeprovider"
	"github.com/cloudfoundry/storeadapter/etcdstoreadapter"
	"github.com/cloudfoundry/storeadapter/workerpool"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"
)

var _ = Describe("Task", func() {
	var bbs *Bbs.BBS

	var wardenClient warden.Client

	var plumbing ifrit.Process
	var executor ifrit.Process

	BeforeEach(func() {
		wardenLinux := componentMaker.WardenLinux()
		wardenClient = wardenLinux.NewClient()

		plumbing = grouper.EnvokeGroup(grouper.RunGroup{
			"etcd":         componentMaker.Etcd(),
			"nats":         componentMaker.NATS(),
			"warden-linux": wardenLinux,
		})

		adapter := etcdstoreadapter.NewETCDStoreAdapter([]string{"http://" + componentMaker.Addresses.Etcd}, workerpool.NewWorkerPool(20))

		bbs = Bbs.NewBBS(adapter, timeprovider.NewTimeProvider(), lagertest.NewTestLogger("test"))

		err := adapter.Connect()
		Ω(err).ShouldNot(HaveOccurred())

		inigo_server.Start(wardenClient)
	})

	AfterEach(func() {
		inigo_server.Stop(wardenClient)

		if executor != nil {
			executor.Signal(syscall.SIGKILL)
			Eventually(executor.Wait(), 5*time.Second).Should(Receive())
		}

		plumbing.Signal(syscall.SIGKILL)
		Eventually(plumbing.Wait(), 5*time.Second).Should(Receive())
	})

	Context("when an exec and rep are running", func() {
		BeforeEach(func() {
			executor = grouper.EnvokeGroup(grouper.RunGroup{
				"exec": componentMaker.Executor("-memoryMB", "1024"),
				"rep":  componentMaker.Rep(),
			})
		})

		Context("and a Task is desired", func() {
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
						fmt.Sprintf("curl %s; sleep 10", strings.Join(inigo_server.CurlArgs(thingWeRan), " ")),
					},
				)

				err := bbs.DesireTask(task)
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("eventually runs the Task", func() {
				Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(thingWeRan))
			})

			Context("when a converger is running", func() {
				var converger ifrit.Process

				BeforeEach(func() {
					converger = ifrit.Envoke(componentMaker.Converger(
						"-convergeRepeatInterval", "1s",
						"-kickPendingTaskDuration", "1s",
					))
				})

				AfterEach(func() {
					converger.Signal(syscall.SIGKILL)
					Eventually(converger.Wait()).Should(Receive())
				})

				Context("after the task starts", func() {
					BeforeEach(func() {
						Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(thingWeRan))
					})

					Context("when the executor disappears", func() {
						BeforeEach(func() {
							executor.Signal(syscall.SIGKILL)
							Eventually(executor.Wait()).Should(Receive())
						})

						It("eventually marks the task as failed", func() {
							// time is primarily influenced by rep's heartbeat interval
							Eventually(bbs.GetAllCompletedTasks, 10*time.Second).Should(HaveLen(1))

							tasks, err := bbs.GetAllCompletedTasks()
							Ω(err).ShouldNot(HaveOccurred())

							completedTask := tasks[0]
							Ω(completedTask.Guid).Should(Equal(task.Guid))
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
								[]string{"-c", fmt.Sprintf("curl %s && sleep 2", strings.Join(inigo_server.CurlArgs(secondThingWeRan), " "))},
							)

							err := bbs.DesireTask(secondTask)
							Ω(err).ShouldNot(HaveOccurred())
						})

						It("is executed once the first task completes, as its resources are cleared", func() {
							Eventually(bbs.GetAllCompletedTasks).Should(HaveLen(1)) // Wait for first task to complete
							Eventually(inigo_server.ReportingGuids).Should(ContainElement(secondThingWeRan))
						})
					})
				})
			})
		})
	})

	Context("when only a converger is running", func() {
		var converger ifrit.Process

		BeforeEach(func() {
			converger = ifrit.Envoke(componentMaker.Converger(
				"-convergeRepeatInterval", "1s",
				"-kickPendingTaskDuration", "1s",
			))
		})

		AfterEach(func() {
			converger.Signal(syscall.SIGKILL)
			Eventually(converger.Wait()).Should(Receive())
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
					[]string{"-c", fmt.Sprintf("curl %s && sleep 2", strings.Join(inigo_server.CurlArgs(thingWeRan), " "))},
				)

				err := bbs.DesireTask(task)
				Ω(err).ShouldNot(HaveOccurred())
			})

			Context("and then an exec and rep come up", func() {
				BeforeEach(func() {
					executor = grouper.EnvokeGroup(grouper.RunGroup{
						"exec": componentMaker.Executor(),
						"rep":  componentMaker.Rep(),
					})
				})

				It("eventually runs the Task", func() {
					Eventually(inigo_server.ReportingGuids, LONG_TIMEOUT).Should(ContainElement(thingWeRan))
				})
			})
		})
	})

	Context("when a very impatient converger is running", func() {
		var converger ifrit.Process

		BeforeEach(func() {
			converger = ifrit.Envoke(componentMaker.Converger(
				"-convergeRepeatInterval", "1s",
				"-expireClaimedTaskDuration", "1s",
			))
		})

		AfterEach(func() {
			converger.Signal(syscall.SIGKILL)
			Eventually(converger.Wait()).Should(Receive())
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
					inigo_server.CurlArgs(guid),
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
