package ccbridge_test

import (
	"fmt"
	"net/http"
	"os"

	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Starting an arbitrary LRP", func() {
	var (
		processGuid string

		runtime ifrit.Process
		bridge  ifrit.Process
	)

	BeforeEach(func() {
		processGuid = factories.GenerateGuid()

		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"router", componentMaker.Router()},
			{"exec", componentMaker.Executor()},
			{"rep", componentMaker.Rep()},
			{"converger", componentMaker.Converger()},
			{"auctioneer", componentMaker.Auctioneer()},
			{"receptor", componentMaker.Receptor()},
			{"route-emitter", componentMaker.RouteEmitter()},
		}))

		bridge = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"tps", componentMaker.TPS()},
		}))
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime, bridge)
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
				Ports: []uint32{
					8080,
				},

				Action: &models.RunAction{
					Path: "bash",
					Args: []string{
						"-c",
						"while true; do sleep 2; done",
					},
				},

				Monitor: &models.RunAction{
					Path: "bash",
					Args: []string{"-c", "echo all good"},
				},
			})
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid)).Should(HaveLen(1))
		})

		Context("and its unhealthy and its start timeout is exceeded", func() {
			It("eventually restarts the LRP", func() {
				err := bbs.DesireLRP(models.DesiredLRP{
					Domain:      "inigo",
					ProcessGuid: processGuid,
					Instances:   1,
					Stack:       componentMaker.Stack,
					MemoryMB:    128,
					DiskMB:      1024,
					Ports: []uint32{
						8080,
					},
					StartTimeout: 1,
					Action: &models.RunAction{
						Path: "bash",
						Args: []string{
							"-c",
							"while true; do sleep 2; done",
						},
					},

					Monitor: &models.RunAction{
						Path: "bash",
						Args: []string{"-c", "exit -1"},
					},
				})
				Ω(err).ShouldNot(HaveOccurred())

				Eventually(helpers.GetLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid)).Should(HaveLen(1))
				Eventually(helpers.GetLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid)).Should(HaveLen(0))
			})
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
				Ports: []uint32{
					8080,
				},

				Action: &models.RunAction{
					Path: "/dockerapp",

					// app expects $VCAP_APPLICATION
					Env: []models.EnvironmentVariable{
						{Name: "VCAP_APPLICATION", Value: `{"instance_index":0}`},
					},
				},

				Monitor: &models.RunAction{
					Path: "echo",
					Args: []string{"all good"},
				},
			})
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid), DOCKER_PULL_ESTIMATE).Should(HaveLen(1))
			Eventually(HelloWorld).ShouldNot(HaveOccurred())
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
