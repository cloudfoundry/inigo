package cell_test

import (
	"net/http"
	"os"

	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/route-emitter/cfroutes"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"
)

var _ = Describe("Privileges", func() {
	var runtime ifrit.Process

	BeforeEach(func() {
		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"exec", componentMaker.Executor("-allowPrivileged")},
			{"rep", componentMaker.Rep()},
			{"converger", componentMaker.Converger()},
			{"auctioneer", componentMaker.Auctioneer()},

			{"router", componentMaker.Router()},
			{"route-emitter", componentMaker.RouteEmitter()},
		}))
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime)
	})

	Context("when a task that tries to do privileged things is requested", func() {
		var taskRequest *receptor.TaskCreateRequest

		BeforeEach(func() {
			taskRequest = &receptor.TaskCreateRequest{
				Domain:   INIGO_DOMAIN,
				TaskGuid: factories.GenerateGuid(),
				RootFS:   componentMaker.PreloadedRootFS(),
				Action: &models.RunAction{
					Path: "sh",
					// always run as root; tests change task-level privileged
					Privileged: true,
					Args: []string{
						"-c",
						// writing to /proc/sysrq-trigger requires full privileges;
						// h is a safe thing to write
						"echo h > /proc/sysrq-trigger",
					},
				},
			}
		})

		JustBeforeEach(func() {
			err := receptorClient.CreateTask(*taskRequest)
			Ω(err).ShouldNot(HaveOccurred())
		})

		Context("when the task is privileged", func() {
			BeforeEach(func() {
				taskRequest.Privileged = true
			})

			It("succeeds", func() {
				var task receptor.TaskResponse
				Eventually(helpers.TaskStatePoller(receptorClient, taskRequest.TaskGuid, &task)).Should(Equal(receptor.TaskStateCompleted))
				Ω(task.Failed).Should(BeFalse())
			})
		})

		Context("when the task is not privileged", func() {
			BeforeEach(func() {
				taskRequest.Privileged = false
			})

			It("fails", func() {
				var task receptor.TaskResponse
				Eventually(helpers.TaskStatePoller(receptorClient, taskRequest.TaskGuid, &task)).Should(Equal(receptor.TaskStateCompleted))
				Ω(task.Failed).Should(BeTrue())
			})
		})
	})

	Context("when a LRP that tries to do privileged things is requested", func() {
		var lrpRequest *receptor.DesiredLRPCreateRequest

		BeforeEach(func() {
			routingInfo := cfroutes.CFRoutes{
				{Hostnames: []string{"lrp-route"}, Port: 8080},
			}.RoutingInfo()

			lrpRequest = &receptor.DesiredLRPCreateRequest{
				Domain:      INIGO_DOMAIN,
				ProcessGuid: factories.GenerateGuid(),
				Instances:   1,
				RootFS:      componentMaker.PreloadedRootFS(),

				Routes: routingInfo,
				Ports:  []uint16{8080},

				Action: &models.RunAction{
					Path: "bash",
					// always run as root; tests change task-level privileged
					Privileged: true,
					Args: []string{
						"-c",
						`
						mkfifo request

						while true; do
						{
							read < request

							status="200 OK"
							if ! echo h > /proc/sysrq-trigger; then
								status="500 Internal Server Error"
							fi
							
						  echo -n -e "HTTP/1.1 ${status}\r\n"
						  echo -n -e "Content-Length: 0\r\n\r\n"
						} | nc -l 0.0.0.0 8080 > request;
						done
						`,
					},
				},
			}
		})

		JustBeforeEach(func() {
			err := receptorClient.CreateDesiredLRP(*lrpRequest)
			Ω(err).ShouldNot(HaveOccurred())
		})

		Context("when the LRP is privileged", func() {
			BeforeEach(func() {
				lrpRequest.Privileged = true
			})

			It("succeeds", func() {
				Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "lrp-route")).Should(Equal(http.StatusOK))
			})
		})

		Context("when the LRP is not privileged", func() {
			BeforeEach(func() {
				lrpRequest.Privileged = false
			})

			It("fails", func() {
				Eventually(helpers.ResponseCodeFromHostPoller(componentMaker.Addresses.Router, "lrp-route")).Should(Equal(http.StatusInternalServerError))
			})
		})
	})
})
