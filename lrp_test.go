package inigo_test

import (
	"fmt"
	"syscall"

	"github.com/cloudfoundry-incubator/inigo/helpers"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry/gunk/timeprovider"
	"github.com/nu7hatch/gouuid"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pivotal-golang/lager/lagertest"
)

var _ = Describe("Starting an arbitrary LRP", func() {
	var (
		processGroup ifrit.Process
		processGuid  string
		tpsAddr      string
		bbs          *Bbs.BBS
	)

	BeforeEach(func() {
		bbs = Bbs.NewBBS(
			suiteContext.EtcdRunner.Adapter(),
			timeprovider.NewTimeProvider(),
			lagertest.NewTestLogger("test"),
		)

		guid, err := uuid.NewV4()
		if err != nil {
			panic("Failed to generate AppID Guid")
		}

		processGuid = guid.String()

		suiteContext.ExecutorRunner.Start()
		suiteContext.RepRunner.Start()
		suiteContext.FileServerRunner.Start()
		suiteContext.AppManagerRunner.Start()
		suiteContext.AuctioneerRunner.Start(AUCTION_MAX_ROUNDS)

		processes := grouper.RunGroup{
			"tps": suiteContext.TPSRunner,
		}

		processGroup = ifrit.Envoke(processes)

		tpsAddr = fmt.Sprintf("http://%s", suiteContext.TPSAddress)
	})

	AfterEach(func() {
		processGroup.Signal(syscall.SIGKILL)
		Eventually(processGroup.Wait()).Should(Receive())
	})

	It("eventually runs on an executor", func() {
		err := bbs.DesireLRP(models.DesiredLRP{
			ProcessGuid: processGuid,
			Instances:   1,
			Stack:       suiteContext.RepStack,
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
								URL:    fmt.Sprintf("http://127.0.0.1:%d/lrp_running/%s/PLACEHOLDER_INSTANCE_INDEX/PLACEHOLDER_INSTANCE_GUID", suiteContext.RepPort, processGuid),
							},
						},
					},
				),
			},
		})
		Ω(err).ShouldNot(HaveOccurred())

		Eventually(helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)).Should(HaveLen(1))
	})
})
