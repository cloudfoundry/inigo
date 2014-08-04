package inigo_test

import (
	"fmt"
	"net/http"
	"syscall"
	"time"

	"github.com/cloudfoundry-incubator/inigo/helpers"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	"github.com/cloudfoundry/gunk/timeprovider"
	"github.com/cloudfoundry/storeadapter/etcdstoreadapter"
	"github.com/cloudfoundry/storeadapter/workerpool"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/pivotal-golang/lager/lagertest"
)

var _ = Describe("Starting an arbitrary LRP", func() {
	var (
		processGuid string
		bbs         *Bbs.BBS

		execRunner *ginkgomon.Runner
		plumbing   ifrit.Process
		runtime    ifrit.Process
	)

	BeforeEach(func() {
		processGuid = factories.GenerateGuid()

		plumbing = grouper.EnvokeGroup(grouper.RunGroup{
			"etcd":         componentMaker.Etcd(),
			"nats":         componentMaker.NATS(),
			"warden-linux": componentMaker.WardenLinux(),
		})

		// need a file server to be able to preprocess. not actually used.
		fileServer, _ := componentMaker.FileServer()
		execRunner = componentMaker.Executor()
		runtime = grouper.EnvokeGroup(grouper.RunGroup{
			"exec":          execRunner,
			"rep":           componentMaker.Rep(),
			"converger":     componentMaker.Converger(),
			"auctioneer":    componentMaker.Auctioneer(),
			"file-server":   fileServer,
			"tps":           componentMaker.TPS(),
			"router":        componentMaker.Router(),
			"route-emitter": componentMaker.RouteEmitter(),
		})

		adapter := etcdstoreadapter.NewETCDStoreAdapter([]string{"http://" + componentMaker.Addresses.Etcd}, workerpool.NewWorkerPool(20))

		err := adapter.Connect()
		Ω(err).ShouldNot(HaveOccurred())

		bbs = Bbs.NewBBS(adapter, timeprovider.NewTimeProvider(), lagertest.NewTestLogger("test"))
	})

	AfterEach(func() {
		runtime.Signal(syscall.SIGKILL)
		Eventually(runtime.Wait()).Should(Receive())

		plumbing.Signal(syscall.SIGKILL)
		Eventually(plumbing.Wait()).Should(Receive())
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
								Path: "bash",
								Args: []string{
									"-c",
									"/dockerapp",
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

			Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid), 5*time.Minute).Should(HaveLen(1))
			Eventually(HelloWorld, DEFAULT_EVENTUALLY_TIMEOUT).ShouldNot(HaveOccurred())
			Ω(<-execRunner.BufferChan).ShouldNot(gbytes.Say("destroying-container-after-failed-init"))
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
	if string(body) != "Hello World\n" {
		return fmt.Errorf("Body Contains '%s' instead of 'Hello World'", string(body))
	}
	return nil
}
