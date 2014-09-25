package inigo_test

import (
	"fmt"
	"net/http"

	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

var _ = Describe("Starting an arbitrary LRP", func() {
	var (
		processGuid string

		execRunner *ginkgomon.Runner
		runtime    ifrit.Process
	)

	BeforeEach(func() {
		processGuid = factories.GenerateGuid()

		// need a file server to be able to preprocess. not actually used.
		fileServer, _ := componentMaker.FileServer()

		execRunner = componentMaker.Executor()

		runtime = ifrit.Invoke(grouper.NewOrdered(nil, grouper.Members{
			{"exec", execRunner},
			{"rep", componentMaker.Rep()},
			{"converger", componentMaker.Converger()},
			{"auctioneer", componentMaker.Auctioneer()},
			{"file-server", fileServer},
			{"tps", componentMaker.TPS()},
			{"router", componentMaker.Router()},
			{"route-emitter", componentMaker.RouteEmitter()},
		}))
	})

	AfterEach(func() {
		helpers.StopProcess(runtime)
	})

	Context("when desiring a buildpack-based LRP", func() {
		It("eventually runs on an executor", func() {
			err := bbs.DesireLRP(models.DesiredLRP{
				Domain:      "inigo",
				ProcessGuid: processGuid,
				Instances:   1,
				Stack:       componentMaker.Stack,
				MemoryMB:    128,
				DiskMB:      1024,
				Ports: []models.PortMapping{
					{ContainerPort: 8080},
				},

				Actions: []models.ExecutorAction{
					models.Parallel(
						models.ExecutorAction{
							models.RunAction{
								Path: "bash",
								Args: []string{
									"-c",
									"while true; do sleep 2; done",
								},
							},
						},
						models.ExecutorAction{
							models.MonitorAction{
								Action: models.ExecutorAction{
									models.RunAction{
										Path: "bash",
										Args: []string{"-c", "echo all good"},
									},
								},

								HealthyThreshold:   1,
								UnhealthyThreshold: 1,

								HealthyHook: models.HealthRequest{
									Method: "PUT",
									URL: fmt.Sprintf(
										"http://%s/lrp_running/%s/PLACEHOLDER_INSTANCE_INDEX/PLACEHOLDER_INSTANCE_GUID",
										componentMaker.Addresses.Rep,
										processGuid,
									),
								},
							},
						},
					),
				},
			})
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid)).Should(HaveLen(1))
		})
	})

	Context("when desiring a docker-based LRP", func() {
		It("eventually runs on an executor", func() {
			err := bbs.DesireLRP(models.DesiredLRP{
				Domain:      "inigo",
				ProcessGuid: processGuid,
				Instances:   1,
				Stack:       componentMaker.Stack,
				RootFSPath:  "docker:///cloudfoundry/inigodockertest",
				Routes:      []string{"route-to-simple"},
				MemoryMB:    128,
				DiskMB:      1024,
				Ports: []models.PortMapping{
					{ContainerPort: 8080},
				},

				Actions: []models.ExecutorAction{
					models.Parallel(
						models.ExecutorAction{
							models.RunAction{
								Path: "/dockerapp",

								// app expects $VCAP_APPLICATION
								Env: []models.EnvironmentVariable{
									{Name: "VCAP_APPLICATION", Value: `{"instance_index":0}`},
								},
							},
						},
						models.ExecutorAction{
							models.MonitorAction{
								Action: models.ExecutorAction{
									models.RunAction{
										Path: "echo",
										Args: []string{"all good"},
									},
								},

								HealthyThreshold:   1,
								UnhealthyThreshold: 1,

								HealthyHook: models.HealthRequest{
									Method: "PUT",
									URL: fmt.Sprintf(
										"http://%s/lrp_running/%s/PLACEHOLDER_INSTANCE_INDEX/PLACEHOLDER_INSTANCE_GUID",
										componentMaker.Addresses.Rep,
										processGuid,
									),
								},
							},
						},
					),
				},
			})
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid), DOCKER_PULL_ESTIMATE).Should(HaveLen(1))
			Eventually(HelloWorld).ShouldNot(HaveOccurred())
			Ω(execRunner).ShouldNot(gbytes.Say("destroying-container-after-failed-init"))
		})
	})
})

func HelloWorld() error {
	body, statusCode, err := helpers.ResponseBodyAndStatusCodeFromHost(componentMaker.Addresses.Router, "route-to-simple")
	if err != nil {
		return err
	}
	if statusCode != http.StatusOK {
		return fmt.Errorf("Status code %d should be 200", statusCode)
	}
	if string(body) != "0" {
		return fmt.Errorf("Body Contains '%s' instead of '0'", string(body))
	}
	return nil
}
