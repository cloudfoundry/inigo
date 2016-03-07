package volman_test

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudfoundry-incubator/bbs/models"
	"github.com/cloudfoundry-incubator/cf-test-helpers/generator"
	"github.com/cloudfoundry-incubator/executor"
	executorinit "github.com/cloudfoundry-incubator/executor/initializer"
	"github.com/cloudfoundry-incubator/garden"
	"github.com/nu7hatch/gouuid"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pivotal-golang/clock"
	"github.com/pivotal-golang/lager"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"
)

var _ = Describe("Executor/Garden/Volman", func() {
	const pruningInterval = 500 * time.Millisecond
	var (
		executorClient executor.Client
		process        ifrit.Process
		runner         ifrit.Runner
		gardenCapacity garden.Capacity
		// exportNetworkEnvVars bool
		cachePath string
		config    executorinit.Configuration
		logger    lager.Logger
		ownerName string
	)

	BeforeEach(func() {
		config = executorinit.DefaultConfiguration

		var err error
		cachePath, err = ioutil.TempDir("", "executor-tmp")
		Expect(err).NotTo(HaveOccurred())

		ownerName = "executor" + generator.RandomName()
	})

	JustBeforeEach(func() {
		var err error

		config.GardenNetwork = "tcp"
		config.GardenAddr = componentMaker.Addresses.GardenLinux
		// config.ReservedExpirationTime = pruningInterval
		// config.ContainerReapInterval = pruningInterval
		// config.HealthyMonitoringInterval = time.Second
		// config.UnhealthyMonitoringInterval = 100 * time.Millisecond
		// config.ExportNetworkEnvVars = exportNetworkEnvVars
		// config.ContainerOwnerName = ownerName
		// config.CachePath = cachePath
		config.GardenHealthcheckProcessPath = "/bin/sh"
		config.GardenHealthcheckProcessArgs = []string{"-c", "echo", "checking health"}
		config.GardenHealthcheckProcessUser = "vcap"
		logger = lagertest.NewTestLogger("test")
		var executorMembers grouper.Members
		executorClient, executorMembers, err = executorinit.Initialize(logger, config, clock.NewClock())
		Expect(err).NotTo(HaveOccurred())
		runner = grouper.NewParallel(os.Kill, executorMembers)

		gardenCapacity, err = gardenClient.Capacity()
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		if executorClient != nil {
			executorClient.Cleanup(logger)
		}

		if process != nil {
			ginkgomon.Kill(process)
		}

		os.RemoveAll(cachePath)
	})

	Context("when running an executor in front of garden and volman", func() {
		JustBeforeEach(func() {
			process = ginkgomon.Invoke(runner)
		})

		Context("when allocating a container with a volman mount", func() {
			var (
				allocationRequest  executor.AllocationRequest
				guid               string
				allocationFailures []executor.AllocationFailure
				volumeId           = "some-volumeId"
				mountPoint         = "/tmp/_fakedriver/" + volumeId
				fileName           = "some-file.txt"
			)

			BeforeEach(func() {
				id, err := uuid.NewV4()
				Expect(err).NotTo(HaveOccurred())
				guid = id.String()

				tags := executor.Tags{"some-tag": "some-value"}
				volumeMounts := []executor.VolumeMount{Driver: "fakedriver", VolumeId: volumeId, Config: "some-config"}
				allocationRequest = executor.NewAllocationRequest(guid, &executor.Resource{VolumeMounts: volumeMounts}, tags)

				allocationFailures, err = executorClient.AllocateContainers(logger, []executor.AllocationRequest{allocationRequest})

				Expect(err).NotTo(HaveOccurred())
				Expect(allocationFailures).To(BeEmpty())
			})

			Context("when running the container", func() {
				var runReq executor.RunRequest
				BeforeEach(func() {
					runInfo := executor.RunInfo{
						Action: models.WrapAction(&models.RunAction{
							Path: "touch",
							User: "vcap",
							Args: []string{"/mnt/testmount/" + fileName},
						}),
					}
					runReq = executor.NewRunRequest(guid, &runInfo, executor.Tags{})
				})

				Context("when successfully mounting the volume", func() {
					FIt("can write files to the mounted volume", func() {
						err := executorClient.RunContainer(logger, &runReq)
						Expect(err).NotTo(HaveOccurred())

						By("expecting the file it wrote to be available outside of the container")
						files, err := filepath.Glob(mountPoint + "/*")
						Expect(err).ToNot(HaveOccurred())
						Expect(len(files)).To(Equal(1))
						Expect(files[0]).To(Equal(mountPoint + fileName))
					})
				})
			})
		})
	})
})
