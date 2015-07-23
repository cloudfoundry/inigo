package executor_test

import (
	"archive/tar"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudfoundry-incubator/executor"
	executorinit "github.com/cloudfoundry-incubator/executor/initializer"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	uuid "github.com/nu7hatch/gouuid"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	"github.com/cloudfoundry-incubator/garden"
)

var _ = Describe("Executor/Garden", func() {
	const pruningInterval = 500 * time.Millisecond
	const ownerName = "executor"

	var (
		executorClient       executor.Client
		process              ifrit.Process
		runner               ifrit.Runner
		gardenCapacity       garden.Capacity
		exportNetworkEnvVars bool
		cachePath            string
	)

	BeforeEach(func() {
		var err error
		cachePath, err = ioutil.TempDir("", "executor-tmp")
		Expect(err).NotTo(HaveOccurred())
	})

	JustBeforeEach(func() {
		var err error

		config := executorinit.DefaultConfiguration
		config.GardenNetwork = "tcp"
		config.GardenAddr = componentMaker.Addresses.GardenLinux
		config.RegistryPruningInterval = pruningInterval
		config.HealthyMonitoringInterval = time.Second
		config.UnhealthyMonitoringInterval = 100 * time.Millisecond
		config.ExportNetworkEnvVars = exportNetworkEnvVars
		config.CachePath = cachePath
		logger := lagertest.NewTestLogger("test")
		var executorMembers grouper.Members
		executorClient, executorMembers, err = executorinit.Initialize(logger, config)
		Expect(err).NotTo(HaveOccurred())
		runner = grouper.NewParallel(os.Kill, executorMembers)

		gardenCapacity, err = gardenClient.Capacity()
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		if executorClient != nil {
			executorClient.Cleanup()
		}

		if process != nil {
			ginkgomon.Kill(process)
		}

		os.RemoveAll(cachePath)
	})

	generateGuid := func() string {
		id, err := uuid.NewV4()
		Expect(err).NotTo(HaveOccurred())

		return id.String()
	}

	allocNewContainer := func(request executor.Container) string {
		request.Guid = generateGuid()

		_, err := executorClient.AllocateContainers([]executor.Container{request})
		Expect(err).NotTo(HaveOccurred())

		return request.Guid
	}

	getContainer := func(guid string) executor.Container {
		container, err := executorClient.GetContainer(guid)
		Expect(err).NotTo(HaveOccurred())

		return container
	}

	containerStatePoller := func(guid string) func() executor.State {
		return func() executor.State {
			return getContainer(guid).State
		}
	}

	containerEventPoller := func(eventSource executor.EventSource, event *executor.Event) func() executor.EventType {
		return func() executor.EventType {
			var err error
			*event, err = eventSource.Next()
			Expect(err).NotTo(HaveOccurred())
			return (*event).EventType()
		}
	}

	findGardenContainer := func(handle string) garden.Container {
		var container garden.Container

		Eventually(func() error {
			var err error

			container, err = gardenClient.Lookup(handle)
			return err
		}).ShouldNot(HaveOccurred())

		return container
	}

	Describe("starting up", func() {
		BeforeEach(func() {
			os.RemoveAll(cachePath)
		})

		JustBeforeEach(func() {
			process = ginkgomon.Invoke(runner)
		})

		Context("when the cache directory exists and contains files", func() {
			BeforeEach(func() {
				err := os.MkdirAll(cachePath, 0755)
				Expect(err).NotTo(HaveOccurred())

				err = ioutil.WriteFile(filepath.Join(cachePath, "should-get-deleted"), []byte("some-contents"), 0755)
				Expect(err).NotTo(HaveOccurred())
			})

			It("clears it out", func() {
				Eventually(func() []string {
					files, err := ioutil.ReadDir(cachePath)
					if err != nil {
						return nil
					}

					filenames := make([]string, len(files))
					for i := 0; i < len(files); i++ {
						filenames[i] = files[i].Name()
					}

					return filenames
				}, 10*time.Second).Should(BeEmpty())
			})
		})

		Context("when the cache directory doesn't exist", func() {
			It("creates a new cache directory", func() {
				Eventually(func() bool {
					dirInfo, err := os.Stat(cachePath)
					if err != nil {
						return false
					}

					return dirInfo.IsDir()
				}, 10*time.Second).Should(BeTrue())
			})
		})

		Context("when there are containers that are owned by the executor", func() {
			var container1, container2 garden.Container

			BeforeEach(func() {
				var err error

				container1, err = gardenClient.Create(garden.ContainerSpec{
					Properties: garden.Properties{
						"executor:owner": ownerName,
					},
				})
				Expect(err).NotTo(HaveOccurred())

				container2, err = gardenClient.Create(garden.ContainerSpec{
					Properties: garden.Properties{
						"executor:owner": ownerName,
					},
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("deletes those containers (and only those containers)", func() {
				Eventually(func() error {
					_, err := gardenClient.Lookup(container1.Handle())
					return err
				}).Should(HaveOccurred())

				Eventually(func() error {
					_, err := gardenClient.Lookup(container2.Handle())
					return err
				}).Should(HaveOccurred())
			})
		})
	})

	Describe("when started", func() {
		JustBeforeEach(func() {
			process = ginkgomon.Invoke(runner)
		})

		Describe("pinging the server", func() {
			var pingErr error

			Context("when Garden responds to ping", func() {
				JustBeforeEach(func() {
					pingErr = executorClient.Ping()
				})

				It("does not return an error", func() {
					Expect(pingErr).NotTo(HaveOccurred())
				})
			})

			Context("when Garden returns an error", func() {
				JustBeforeEach(func() {
					ginkgomon.Interrupt(gardenProcess)
					pingErr = executorClient.Ping()
				})

				AfterEach(func() {
					gardenProcess = ginkgomon.Invoke(componentMaker.GardenLinux())
				})

				It("should return an error", func() {
					Expect(pingErr).To(HaveOccurred())
					Expect(pingErr.Error()).To(ContainSubstring("connection refused"))
				})
			})
		})

		Describe("getting the total resources", func() {
			var resources executor.ExecutorResources
			var resourceErr error

			JustBeforeEach(func() {
				resources, resourceErr = executorClient.TotalResources()
			})

			It("not return an error", func() {
				Expect(resourceErr).NotTo(HaveOccurred())
			})

			It("returns the preset capacity", func() {
				expectedResources := executor.ExecutorResources{
					MemoryMB:   int(gardenCapacity.MemoryInBytes / 1024 / 1024),
					DiskMB:     int(gardenCapacity.DiskInBytes / 1024 / 1024),
					Containers: int(gardenCapacity.MaxContainers),
				}
				Expect(resources).To(Equal(expectedResources))
			})
		})

		Describe("allocating a container", func() {
			var (
				container executor.Container

				guid string

				allocationErrorMap map[string]string
				allocErr           error
			)

			BeforeEach(func() {
				guid = generateGuid()

				container = executor.Container{
					Guid: guid,

					Tags: executor.Tags{"some-tag": "some-value"},

					Env: []executor.EnvironmentVariable{
						{Name: "ENV1", Value: "val1"},
						{Name: "ENV2", Value: "val2"},
					},

					Action: &models.RunAction{
						Path: "true",
						User: "vcap",
						Env: []models.EnvironmentVariable{
							{Name: "RUN_ENV1", Value: "run_val1"},
							{Name: "RUN_ENV2", Value: "run_val2"},
						},
					},
				}
			})

			JustBeforeEach(func() {
				allocationErrorMap, allocErr = executorClient.AllocateContainers([]executor.Container{container})
			})

			It("does not return an error", func() {
				Expect(allocErr).NotTo(HaveOccurred())
			})

			It("returns an empty error map", func() {
				Expect(allocationErrorMap).To(BeEmpty())
			})

			It("shows up in the container list", func() {
				containers, err := executorClient.ListContainers(nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(containers).To(HaveLen(1))

				Expect(containers[0].State).To(Equal(executor.StateReserved))
				Expect(containers[0].Guid).To(Equal(guid))
				Expect(containers[0].MemoryMB).To(Equal(0))
				Expect(containers[0].DiskMB).To(Equal(0))
				Expect(containers[0].Tags).To(Equal(executor.Tags{"some-tag": "some-value"}))
				Expect(containers[0].State).To(Equal(executor.StateReserved))
				Expect(containers[0].AllocatedAt).To(BeNumerically("~", time.Now().UnixNano(), time.Second))

			})

			Context("when allocated with memory and disk limits", func() {
				BeforeEach(func() {
					container.MemoryMB = 256
					container.DiskMB = 256
				})

				It("returns the limits on the container", func() {
					containers, err := executorClient.ListContainers(nil)
					Expect(err).NotTo(HaveOccurred())
					Expect(containers).To(HaveLen(1))
					Expect(containers[0].MemoryMB).To(Equal(256))
					Expect(containers[0].DiskMB).To(Equal(256))
				})

				It("reduces the capacity by the amount reserved", func() {
					Expect(executorClient.RemainingResources()).To(Equal(executor.ExecutorResources{
						MemoryMB:   int(gardenCapacity.MemoryInBytes/1024/1024) - 256,
						DiskMB:     int(gardenCapacity.DiskInBytes/1024/1024) - 256,
						Containers: int(gardenCapacity.MaxContainers) - 1,
					}))
				})
			})

			Context("when the requested CPU weight is > 100", func() {
				BeforeEach(func() {
					container.CPUWeight = 101
				})

				It("returns an error", func() {
					Expect(allocErr).NotTo(HaveOccurred())
					Expect(allocationErrorMap[container.Guid]).To(Equal(executor.ErrLimitsInvalid.Error()))
				})
			})

			Context("when the guid is already taken", func() {
				JustBeforeEach(func() {
					Expect(allocErr).NotTo(HaveOccurred())
					allocationErrorMap, allocErr = executorClient.AllocateContainers([]executor.Container{container})
				})

				It("returns an error", func() {
					Expect(allocErr).NotTo(HaveOccurred())
					Expect(allocationErrorMap[container.Guid]).To(Equal(executor.ErrContainerGuidNotAvailable.Error()))
				})
			})

			Context("when a guid is not specified", func() {
				BeforeEach(func() {
					container.Guid = ""
				})

				It("returns an error", func() {
					Expect(allocErr).NotTo(HaveOccurred())
					Expect(allocationErrorMap[container.Guid]).To(Equal(executor.ErrGuidNotSpecified.Error()))
				})
			})

			Context("when there is no room", func() {
				BeforeEach(func() {
					container.MemoryMB = 999999999999999
					container.DiskMB = 999999999999999
				})

				It("returns an error", func() {
					Expect(allocErr).NotTo(HaveOccurred())
					Expect(allocationErrorMap[container.Guid]).To(Equal(executor.ErrInsufficientResourcesAvailable.Error()))
				})
			})

			Describe("running it", func() {
				var runErr error
				var eventSource executor.EventSource

				JustBeforeEach(func() {
					var err error

					eventSource, err = executorClient.SubscribeToEvents()
					Expect(err).NotTo(HaveOccurred())

					runErr = executorClient.RunContainer(guid)
				})

				AfterEach(func() {
					eventSource.Close()
				})

				Context("when the container can be created", func() {
					var gardenContainer garden.Container

					JustBeforeEach(func() {
						gardenContainer = findGardenContainer(guid)
					})

					It("returns no error", func() {
						Expect(runErr).NotTo(HaveOccurred())
					})

					It("creates it with the configured owner", func() {
						info, err := gardenContainer.Info()
						Expect(err).NotTo(HaveOccurred())

						Expect(info.Properties["executor:owner"]).To(Equal(ownerName))
					})

					It("sets global environment variables on the container", func() {
						output := gbytes.NewBuffer()

						process, err := gardenContainer.Run(garden.ProcessSpec{
							Path: "env",
							User: "vcap",
						}, garden.ProcessIO{
							Stdout: output,
						})
						Expect(err).NotTo(HaveOccurred())
						Expect(process.Wait()).To(Equal(0))

						Expect(output.Contents()).To(ContainSubstring("ENV1=val1"))
						Expect(output.Contents()).To(ContainSubstring("ENV2=val2"))
					})

					It("saves the succeeded run result", func() {
						Eventually(containerStatePoller(guid)).Should(Equal(executor.StateCompleted))

						container := getContainer(guid)
						Expect(container.RunResult.Failed).To(BeFalse())
						Expect(container.RunResult.FailureReason).To(BeEmpty())
					})

					Context("when created without a monitor action", func() {
						BeforeEach(func() {
							container.Action = &models.RunAction{
								Path: "sh",
								User: "vcap",
								Args: []string{"-c", "while true; do sleep 1; done"},
							}
						})

						It("reports the state as 'running'", func() {
							Eventually(containerStatePoller(guid)).Should(Equal(executor.StateRunning))
							Consistently(containerStatePoller(guid)).Should(Equal(executor.StateRunning))
						})
					})

					Context("when created with a monitor action", func() {
						itFailsOnlyIfMonitoringSucceedsAndThenFails := func() {
							Context("when monitoring succeeds", func() {
								BeforeEach(func() {
									container.Monitor = &models.RunAction{
										User: "vcap",
										Path: "true",
									}
								})

								It("emits a running container event", func() {
									var event executor.Event
									Eventually(containerEventPoller(eventSource, &event), 5).Should(Equal(executor.EventTypeContainerRunning))
								})

								It("reports the state as 'running'", func() {
									Eventually(containerStatePoller(guid)).Should(Equal(executor.StateRunning))
									Consistently(containerStatePoller(guid)).Should(Equal(executor.StateRunning))
								})

								It("does not stop the container", func() {
									Consistently(containerStatePoller(guid)).ShouldNot(Equal(executor.StateCompleted))
								})
							})

							Context("when monitoring persistently fails", func() {
								BeforeEach(func() {
									container.Monitor = &models.RunAction{
										User: "vcap",
										Path: "false",
									}
								})

								It("reports the state as 'created'", func() {
									Eventually(containerStatePoller(guid)).Should(Equal(executor.StateCreated))
									Consistently(containerStatePoller(guid)).Should(Equal(executor.StateCreated))
								})
							})

							Context("when monitoring succeeds and then fails", func() {
								BeforeEach(func() {
									container.Monitor = &models.RunAction{
										User: "vcap",
										Path: "sh",
										Args: []string{
											"-c",
											`
													if [ -f already_ran ]; then
														exit 1
													else
														touch already_ran
													fi
												`,
										},
									}
								})

								It("reports the container as 'running' and then as 'completed'", func() {
									Eventually(containerStatePoller(guid)).Should(Equal(executor.StateRunning))
									Eventually(containerStatePoller(guid)).Should(Equal(executor.StateCompleted))
								})
							})
						}

						Context("when the action succeeds and exits immediately (daemonization)", func() {
							BeforeEach(func() {
								container.Action = &models.RunAction{
									Path: "true",
									User: "vcap",
								}
							})

							itFailsOnlyIfMonitoringSucceedsAndThenFails()
						})

						Context("while the action does not stop running", func() {
							BeforeEach(func() {
								container.Action = &models.RunAction{
									Path: "sh",
									User: "vcap",
									Args: []string{"-c", "while true; do sleep 1; done"},
								}
							})

							itFailsOnlyIfMonitoringSucceedsAndThenFails()
						})

						Context("when the action fails", func() {
							BeforeEach(func() {
								container.Action = &models.RunAction{
									User: "vcap",
									Path: "false",
								}
							})

							Context("even if the monitoring succeeds", func() {
								BeforeEach(func() {
									container.Monitor = &models.RunAction{
										User: "vcap",
										Path: "true",
									}
								})

								It("stops the container", func() {
									Eventually(containerStatePoller(guid)).Should(Equal(executor.StateCompleted))
								})
							})
						})
					})

					Context("after running succeeds", func() {
						Describe("deleting the container", func() {
							It("works", func(done Done) {
								defer close(done)

								Eventually(containerStatePoller(guid)).Should(Equal(executor.StateCompleted))

								err := executorClient.DeleteContainer(guid)
								Expect(err).NotTo(HaveOccurred())
							}, 5)
						})
					})

					Context("when running fails", func() {
						BeforeEach(func() {
							container.Action = &models.RunAction{
								User: "vcap",
								Path: "false",
							}
						})

						It("saves the failed result and reason", func() {
							Eventually(containerStatePoller(guid)).Should(Equal(executor.StateCompleted))

							container := getContainer(guid)
							Expect(container.RunResult.Failed).To(BeTrue())
							Expect(container.RunResult.FailureReason).To(Equal("Exited with status 1"))
						})

						Context("when listening for events", func() {
							It("emits a completed container event", func() {
								var event executor.Event
								Eventually(containerEventPoller(eventSource, &event), 5).Should(Equal(executor.EventTypeContainerComplete))

								completeEvent := event.(executor.ContainerCompleteEvent)
								Expect(completeEvent.Container().State).To(Equal(executor.StateCompleted))
								Expect(completeEvent.Container().RunResult.Failed).To(BeTrue())
								Expect(completeEvent.Container().RunResult.FailureReason).To(Equal("Exited with status 1"))
							})
						})
					})
				})

				Context("when the container cannot be created", func() {
					BeforeEach(func() {
						container.RootFSPath = "gopher://example.com"
					})

					It("does not immediately return an error", func() {
						Expect(runErr).NotTo(HaveOccurred())
					})

					Context("when listening for events", func() {
						It("eventually completes with failure", func() {
							Eventually(containerStatePoller(guid)).Should(Equal(executor.StateCompleted))

							container := getContainer(guid)
							Expect(container.RunResult.Failed).To(BeTrue())
							Expect(container.RunResult.FailureReason).To(Equal("failed to initialize container"))
						})
					})
				})
			})
		})

		Describe("running a bogus guid", func() {
			It("returns an error", func() {
				err := executorClient.RunContainer("bogus")
				Expect(err).To(Equal(executor.ErrContainerNotFound))
			})
		})

		Context("when the container has been allocated", func() {
			var guid string

			JustBeforeEach(func() {
				guid = allocNewContainer(executor.Container{
					MemoryMB: 1024,
					DiskMB:   1024,
				})
			})

			Describe("deleting it", func() {
				It("makes the previously allocated resources available again", func() {
					err := executorClient.DeleteContainer(guid)
					Expect(err).NotTo(HaveOccurred())

					Eventually(executorClient.RemainingResources).Should(Equal(executor.ExecutorResources{
						MemoryMB:   int(gardenCapacity.MemoryInBytes / 1024 / 1024),
						DiskMB:     int(gardenCapacity.DiskInBytes / 1024 / 1024),
						Containers: int(gardenCapacity.MaxContainers),
					}))
				})
			})

			Describe("listing containers", func() {
				It("shows up in the container list in reserved state", func() {
					containers, err := executorClient.ListContainers(nil)
					Expect(err).NotTo(HaveOccurred())
					Expect(containers).To(HaveLen(1))
					Expect(containers[0].Guid).To(Equal(guid))
					Expect(containers[0].State).To(Equal(executor.StateReserved))
				})
			})
		})

		Context("while it is running", func() {
			var guid string

			JustBeforeEach(func() {
				guid = allocNewContainer(executor.Container{
					MemoryMB: 64,
					DiskMB:   64,

					Action: &models.RunAction{
						Path: "sh",
						User: "vcap",
						Args: []string{"-c", "while true; do sleep 1; done"},
					},
				})

				err := executorClient.RunContainer(guid)
				Expect(err).NotTo(HaveOccurred())

				Eventually(containerStatePoller(guid)).Should(Equal(executor.StateRunning))
			})

			Describe("StopContainer", func() {
				It("does not return an error", func() {
					err := executorClient.StopContainer(guid)
					Expect(err).NotTo(HaveOccurred())
				})

				It("stops the container but does not delete it", func() {
					err := executorClient.StopContainer(guid)
					Expect(err).NotTo(HaveOccurred())

					var container executor.Container
					Eventually(func() executor.State {
						container, err = executorClient.GetContainer(guid)
						Expect(err).NotTo(HaveOccurred())
						return container.State
					}).Should(Equal(executor.StateCompleted))

					Expect(container.RunResult.Stopped).To(BeTrue())

					_, err = gardenClient.Lookup(guid)
					Expect(err).NotTo(HaveOccurred())
				})
			})

			Describe("DeleteContainer", func() {
				It("deletes the container", func() {
					err := executorClient.DeleteContainer(guid)
					Expect(err).NotTo(HaveOccurred())

					Eventually(func() error {
						_, err := gardenClient.Lookup(guid)
						return err
					}).Should(HaveOccurred())
				})
			})

			Describe("listing containers", func() {
				It("shows up in the container list in running state", func() {
					containers, err := executorClient.ListContainers(nil)
					Expect(err).NotTo(HaveOccurred())
					Expect(containers).To(HaveLen(1))
					Expect(containers[0].Guid).To(Equal(guid))
					Expect(containers[0].State).To(Equal(executor.StateRunning))
				})
			})

			Describe("remaining resources", func() {
				It("has the container's reservation subtracted", func() {
					remaining, err := executorClient.RemainingResources()
					Expect(err).NotTo(HaveOccurred())

					Expect(remaining.MemoryMB).To(Equal(int(gardenCapacity.MemoryInBytes/1024/1024) - 64))
					Expect(remaining.DiskMB).To(Equal(int(gardenCapacity.DiskInBytes/1024/1024) - 64))
				})

				Context("when the container disappears", func() {
					It("eventually goes back to the total resources", func() {
						// wait for the container to be present
						findGardenContainer(guid)

						// kill it
						err := gardenClient.Destroy(guid)
						Expect(err).NotTo(HaveOccurred())

						Eventually(executorClient.RemainingResources).Should(Equal(executor.ExecutorResources{
							MemoryMB:   int(gardenCapacity.MemoryInBytes / 1024 / 1024),
							DiskMB:     int(gardenCapacity.DiskInBytes / 1024 / 1024),
							Containers: int(gardenCapacity.MaxContainers),
						}))
					})
				})
			})
		})

		Describe("getting files from a container", func() {
			var (
				guid string

				stream    io.ReadCloser
				streamErr error
			)

			Context("when the container hasn't been initialized", func() {
				JustBeforeEach(func() {
					guid = allocNewContainer(executor.Container{
						MemoryMB: 1024,
						DiskMB:   1024,
					})

					stream, streamErr = executorClient.GetFiles(guid, "some/path")
				})

				It("returns an error", func() {
					Expect(streamErr).To(HaveOccurred())
				})
			})

			Context("when the container is running", func() {
				var container garden.Container

				JustBeforeEach(func() {
					guid = allocNewContainer(executor.Container{
						Action: &models.RunAction{
							Path: "sh",
							User: "vcap",
							Args: []string{
								"-c", `while true; do	sleep 1; done`,
							},
						},
					})

					err := executorClient.RunContainer(guid)
					Expect(err).NotTo(HaveOccurred())

					container = findGardenContainer(guid)

					process, err := container.Run(garden.ProcessSpec{
						Path: "sh",
						Args: []string{"-c", "mkdir some; echo hello > some/path"},
						User: "vcap",
					}, garden.ProcessIO{})
					Expect(err).NotTo(HaveOccurred())
					Expect(process.Wait()).To(Equal(0))

					stream, streamErr = executorClient.GetFiles(guid, "/home/vcap/some/path")
				})

				It("does not error", func() {
					Expect(streamErr).NotTo(HaveOccurred())
				})

				It("returns a stream of the contents of the file", func() {
					tarReader := tar.NewReader(stream)

					header, err := tarReader.Next()
					Expect(err).NotTo(HaveOccurred())

					Expect(header.FileInfo().Name()).To(Equal("path"))
					Expect(ioutil.ReadAll(tarReader)).To(Equal([]byte("hello\n")))
				})
			})
		})

		Describe("pruning the registry", func() {
			It("continously prunes the registry", func() {
				_, err := executorClient.AllocateContainers([]executor.Container{
					{
						Guid: "some-handle",

						MemoryMB: 1024,
						DiskMB:   1024,
					},
				})
				Expect(err).NotTo(HaveOccurred())

				Expect(executorClient.ListContainers(nil)).To(HaveLen(1))

				Eventually(func() interface{} {
					containers, err := executorClient.ListContainers(nil)
					Expect(err).NotTo(HaveOccurred())

					return containers
				}, pruningInterval*3).Should(BeEmpty())
			})
		})

		Describe("listing containers", func() {
			Context("with no containers", func() {
				It("returns an empty set of containers", func() {
					Expect(executorClient.ListContainers(nil)).To(BeEmpty())
				})
			})

			Context("when a container has been allocated", func() {
				var (
					container executor.Container

					guid string
				)

				JustBeforeEach(func() {
					guid = allocNewContainer(container)
				})

				Context("without tags", func() {
					It("includes the allocated container", func() {
						containers, err := executorClient.ListContainers(nil)
						Expect(err).NotTo(HaveOccurred())
						Expect(containers).To(HaveLen(1))
						Expect(containers[0].Guid).To(Equal(guid))
					})
				})

				Context("with tags", func() {
					BeforeEach(func() {
						container.Tags = executor.Tags{
							"some-tag": "some-value",
						}
					})

					Describe("listing by matching tags", func() {
						It("includes the allocated container", func() {
							containers, err := executorClient.ListContainers(executor.Tags{
								"some-tag": "some-value",
							})
							Expect(err).NotTo(HaveOccurred())
							Expect(containers).To(HaveLen(1))
							Expect(containers[0].Guid).To(Equal(guid))
						})

						It("filters by and-ing the requested tags", func() {
							Expect(executorClient.ListContainers(executor.Tags{
								"some-tag":  "some-value",
								"bogus-tag": "bogus-value",
							})).To(BeEmpty())
						})
					})

					Describe("listing by non-matching tags", func() {
						It("does not include the allocated container", func() {
							Expect(executorClient.ListContainers(executor.Tags{
								"some-tag": "bogus-value",
							})).To(BeEmpty())
						})
					})
				})
			})
		})

		Describe("container networking", func() {
			Context("when a container listens on the local end of CF_INSTANCE_ADDR", func() {
				var guid string
				var containerResponse []byte
				var externalAddr string

				JustBeforeEach(func() {
					guid = allocNewContainer(executor.Container{
						Ports: []executor.PortMapping{
							{ContainerPort: 8080},
						},

						Action: &models.RunAction{
							User: "vcap",
							Path: "sh",
							Args: []string{"-c", "echo -n .$CF_INSTANCE_ADDR. | nc -l 8080"},
						},
					})

					err := executorClient.RunContainer(guid)
					Expect(err).NotTo(HaveOccurred())

					Eventually(containerStatePoller(guid)).Should(Equal(executor.StateRunning))

					container := getContainer(guid)

					externalAddr = fmt.Sprintf("%s:%d", container.ExternalIP, container.Ports[0].HostPort)

					var conn net.Conn
					Eventually(func() error {
						var err error
						conn, err = net.Dial("tcp", externalAddr)
						return err
					}).ShouldNot(HaveOccurred())

					containerResponse, err = ioutil.ReadAll(conn)
					Expect(err).NotTo(HaveOccurred())
				})

				Context("when exportNetworkEnvVars is set", func() {
					BeforeEach(func() {
						exportNetworkEnvVars = true
					})

					It("echoes back the correct CF_INSTANCE_ADDR", func() {
						Expect(string(containerResponse)).To(Equal("." + externalAddr + "."))
					})
				})

				Context("when exportNetworkEnvVars is not set", func() {
					BeforeEach(func() {
						exportNetworkEnvVars = false
					})

					It("echoes back an empty CF_INSTANCE_ADDR", func() {
						Expect(string(containerResponse)).To(Equal(".."))
					})
				})
			})
		})
	})
})
