package volman_test

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/cloudfoundry-incubator/bbs/models"
	"github.com/cloudfoundry-incubator/cf-test-helpers/generator"
	"github.com/cloudfoundry-incubator/executor"
	executorinit "github.com/cloudfoundry-incubator/executor/initializer"
	"github.com/cloudfoundry-incubator/garden"
	"github.com/nu7hatch/gouuid"
	. "github.com/onsi/ginkgo"
	ginkgoconfig "github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"
	"github.com/pivotal-golang/clock"
	"github.com/pivotal-golang/lager"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"
)

// these tests could eventually be folded into ../executor/executor_garden_test.go
var _ = Describe("Executor/Garden/Volman", func() {

	var (
		executorClient executor.Client
		process        ifrit.Process
		runner         ifrit.Runner
		gardenCapacity garden.Capacity
		cachePath      string
		config         executorinit.Configuration
		logger         lager.Logger
	)

	getContainer := func(guid string) executor.Container {
		container, err := executorClient.GetContainer(logger, guid)
		Expect(err).NotTo(HaveOccurred())

		return container
	}

	containerStatePoller := func(guid string) func() executor.State {
		return func() executor.State {
			return getContainer(guid).State
		}
	}

	Context("when volman is not correctly configured", func() {
		BeforeEach(func() {
			logger = lagertest.NewTestLogger("volman-executor-tests")
			config = executorinit.DefaultConfiguration

			var err error
			cachePath, err = ioutil.TempDir("", "executor-tmp")
			Expect(err).NotTo(HaveOccurred())

			config = executorinit.DefaultConfiguration
			config.VolmanDriverPath = path.Join(componentMaker.VolmanDriverConfigDir, fmt.Sprintf("node-%d", ginkgoconfig.GinkgoConfig.ParallelNode))
			config.GardenNetwork = "tcp"
			config.GardenAddr = componentMaker.Addresses.GardenLinux
			config.HealthyMonitoringInterval = time.Second
			config.UnhealthyMonitoringInterval = 100 * time.Millisecond
			config.ContainerOwnerName = "executor" + generator.RandomName()
			config.GardenHealthcheckProcessPath = "/bin/sh"
			config.GardenHealthcheckProcessArgs = []string{"-c", "echo", "checking health"}
			config.GardenHealthcheckProcessUser = "vcap"

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
		Context("when allocating a container without any volman mounts", func() {
			JustBeforeEach(func() {
				process = ginkgomon.Invoke(runner)
			})
			var (
				guid               string
				allocationRequest  executor.AllocationRequest
				allocationFailures []executor.AllocationFailure
			)

			BeforeEach(func() {
				id, err := uuid.NewV4()
				Expect(err).NotTo(HaveOccurred())
				guid = id.String()

				tags := executor.Tags{"some-tag": "some-value"}

				allocationRequest = executor.NewAllocationRequest(guid, &executor.Resource{}, tags)

				allocationFailures, err = executorClient.AllocateContainers(logger, []executor.AllocationRequest{allocationRequest})

				Expect(err).NotTo(HaveOccurred())
				Expect(allocationFailures).To(BeEmpty())
			})

			Context("when running the container", func() {
				var (
					runReq executor.RunRequest
				)

				BeforeEach(func() {
					runInfo := executor.RunInfo{
						Action: models.WrapAction(&models.RunAction{
							Path: "/bin/touch",
							User: "root",
							Args: []string{"/tmp"},
						}),
					}
					runReq = executor.NewRunRequest(guid, &runInfo, executor.Tags{})
				})
				It("container start should succeed", func() {
					err := executorClient.RunContainer(logger, &runReq)
					Expect(err).NotTo(HaveOccurred())
					Eventually(containerStatePoller(guid)).Should(Equal(executor.StateCompleted))
					Expect(getContainer(guid).RunResult.Failed).Should(BeFalse())
				})
			})
		})
	})
	Context("when volman is correctly configured", func() {
		BeforeEach(func() {
			config = executorinit.DefaultConfiguration

			var err error
			cachePath, err = ioutil.TempDir("", "executor-tmp")
			Expect(err).NotTo(HaveOccurred())

			ownerName = "executor" + generator.RandomName()

			config.VolmanDriverPath = componentMaker.VolmanDriverConfigDir
			config.GardenNetwork = "tcp"
			config.GardenAddr = componentMaker.Addresses.GardenLinux
			config.HealthyMonitoringInterval = time.Second
			config.UnhealthyMonitoringInterval = 100 * time.Millisecond
			config.ContainerOwnerName = ownerName
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

			BeforeEach(func() {
				process = ginkgomon.Invoke(runner)
			})

			Context("when allocating a container", func() {
				var (
					guid               string
					allocationRequest  executor.AllocationRequest
					allocationFailures []executor.AllocationFailure
				)

				BeforeEach(func() {
					id, err := uuid.NewV4()
					Expect(err).NotTo(HaveOccurred())
					guid = id.String()

					tags := executor.Tags{"some-tag": "some-value"}

					allocationRequest = executor.NewAllocationRequest(guid, &executor.Resource{}, tags)

					allocationFailures, err = executorClient.AllocateContainers(logger, []executor.AllocationRequest{allocationRequest})

					Expect(err).NotTo(HaveOccurred())
					Expect(allocationFailures).To(BeEmpty())
				})

				Context("when running the container", func() {
					var (
						runReq   executor.RunRequest
						volumeId string
						fileName string
					)

					BeforeEach(func() {
						fileName = fmt.Sprintf("testfile-%d.txt", time.Now().UnixNano())
						volumeId = fmt.Sprintf("some-volumeID-%d", time.Now().UnixNano())
						someConfig := map[string]interface{}{"volume_id": volumeId}
						volumeMounts := []executor.VolumeMount{executor.VolumeMount{ContainerPath: "/testmount", Driver: "fakedriver", VolumeId: volumeId, Config: someConfig, Mode: executor.BindMountModeRW}}
						runInfo := executor.RunInfo{
							VolumeMounts: volumeMounts,
							Action: models.WrapAction(&models.RunAction{
								Path: "/bin/touch",
								User: "root",
								Args: []string{"/testmount/" + fileName},
							}),
						}
						runReq = executor.NewRunRequest(guid, &runInfo, executor.Tags{})
					})
					Context("when successfully mounting a RW Mode volume", func() {
						BeforeEach(func() {
							err := executorClient.RunContainer(logger, &runReq)
							Expect(err).NotTo(HaveOccurred())
							Eventually(containerStatePoller(guid)).Should(Equal(executor.StateCompleted))
							Expect(getContainer(guid).RunResult.Failed).Should(BeFalse())
						})

						It("can write files to the mounted volume", func() {
							By("we expect the file it wrote to be available outside of the container")
							volmanPath := path.Join(componentMaker.VolmanDriverConfigDir, "_volumes", volumeId, fileName)
							files, err := filepath.Glob(volmanPath)
							Expect(err).ToNot(HaveOccurred())
							Expect(len(files)).To(Equal(1))
						})

						It("cleans up the mounts afterwards", func() {
							err := executorClient.DeleteContainer(logger, guid)
							Expect(err).NotTo(HaveOccurred())

							files, err := filepath.Glob(path.Join(componentMaker.VolmanDriverConfigDir, "_volumes", volumeId, fileName))
							Expect(err).ToNot(HaveOccurred())
							Expect(len(files)).To(Equal(0))
						})
					})
				})
			})
		})
	})
})
