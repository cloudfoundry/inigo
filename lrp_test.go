package inigo_test

import (
	"fmt"
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
	"github.com/tedsuo/ifrit/grouper"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pivotal-golang/lager/lagertest"
)

var _ = Describe("Starting an arbitrary LRP", func() {
	var (
		processGuid string
		bbs         *Bbs.BBS

		plumbing ifrit.Process
		runtime  ifrit.Process
	)

	BeforeEach(func() {
		processGuid = factories.GenerateGuid()

		plumbing = grouper.EnvokeGroup(grouper.RunGroup{
			"etcd":         componentMaker.Etcd(),
			"nats":         componentMaker.NATS(),
			"warden-linux": componentMaker.WardenLinux(),
		})

		runtime = grouper.EnvokeGroup(grouper.RunGroup{
			"exec":       componentMaker.Executor(),
			"rep":        componentMaker.Rep(),
			"auctioneer": componentMaker.Auctioneer(),
		})

		adapter := etcdstoreadapter.NewETCDStoreAdapter([]string{"http://" + componentMaker.Addresses.Etcd}, workerpool.NewWorkerPool(20))

		bbs = Bbs.NewBBS(adapter, timeprovider.NewTimeProvider(), lagertest.NewTestLogger("test"))
	})

	AfterEach(func() {
		runtime.Signal(syscall.SIGKILL)
		Eventually(runtime.Wait(), 5*time.Second).Should(Receive())

		plumbing.Signal(syscall.SIGKILL)
		Eventually(plumbing.Wait(), 5*time.Second).Should(Receive())
	})

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
