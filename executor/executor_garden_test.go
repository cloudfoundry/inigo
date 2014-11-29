package executor_test

import (
	"archive/tar"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/cloudfoundry-incubator/executor"
	httpClient "github.com/cloudfoundry-incubator/executor/http/client"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	uuid "github.com/nu7hatch/gouuid"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"

	garden "github.com/cloudfoundry-incubator/garden/api"
)

var _ = Describe("Executor/Garden", func() {
	const pruningInterval = 500 * time.Millisecond
	const ownerName = "executor"

	var (
		executorClient       executor.Client
		process              ifrit.Process
		runner               *ginkgomon.Runner
		gardenCapacity       garden.Capacity
		exportNetworkEnvVars bool
	)

	JustBeforeEach(func() {
		var err error

		runner = componentMaker.Executor(
			"-pruneInterval", pruningInterval.String(),
			"-healthyMonitoringInterval", "1s",
			"-gardenSyncInterval", "1s",
			"-unhealthyMonitoringInterval", "100ms",
			"-exportNetworkEnvVars="+strconv.FormatBool(exportNetworkEnvVars),
		)

		executorClient = httpClient.New(&http.Client{}, "http://"+componentMaker.Addresses.Executor)
		gardenCapacity, err = gardenClient.Capacity()
		Ω(err).ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		if process != nil {
			ginkgomon.Kill(process)
		}
	})

	generateGuid := func() string {
		id, err := uuid.NewV4()
		Ω(err).ShouldNot(HaveOccurred())

		return id.String()
	}

	allocNewContainer := func(request executor.Container) string {
		request.Guid = generateGuid()

		_, err := executorClient.AllocateContainer(request)
		Ω(err).ShouldNot(HaveOccurred())

		return request.Guid
	}

	getContainer := func(guid string) executor.Container {
		container, err := executorClient.GetContainer(guid)
		Ω(err).ShouldNot(HaveOccurred())

		return container
	}

	containerStatePoller := func(guid string) func() executor.State {
		return func() executor.State {
			return getContainer(guid).State
		}
	}

	containerHealthPoller := func(guid string) func() executor.Health {
		return func() executor.Health {
			return getContainer(guid).Health
		}
	}

	containerEventPoller := func(events <-chan executor.Event, event *executor.Event) func() executor.EventType {
		return func() executor.EventType {
			Eventually(events).Should(Receive(event))
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
		var workingDir string

		JustBeforeEach(func() {
			runner.StartCheck = ""
			process = ginkgomon.Invoke(runner)
		})

		BeforeEach(func() {
			workingDir = filepath.Join(componentMaker.ExecutorTmpDir, "executor-work")
			os.RemoveAll(workingDir)
		})

		Context("when the working directory exists and contains files", func() {
			BeforeEach(func() {
				err := os.MkdirAll(workingDir, 0755)
				Ω(err).ShouldNot(HaveOccurred())

				err = ioutil.WriteFile(filepath.Join(workingDir, "should-get-deleted"), []byte("some-contents"), 0755)
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("cleans up its working directory", func() {
				Eventually(func() bool {
					files, err := ioutil.ReadDir(workingDir)
					if err != nil {
						return false
					}
					return len(files) == 0
				}).Should(BeTrue())
			})
		})

		Context("when the working directory doesn't exist", func() {
			It("creates a new working directory", func() {
				Eventually(func() bool {
					workingDirInfo, err := os.Stat(workingDir)
					if err != nil {
						return false
					}

					return workingDirInfo.IsDir()
				}).Should(BeTrue())
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
				Ω(err).ShouldNot(HaveOccurred())

				container2, err = gardenClient.Create(garden.ContainerSpec{
					Properties: garden.Properties{
						"executor:owner": ownerName,
					},
				})
				Ω(err).ShouldNot(HaveOccurred())
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
					Ω(pingErr).ShouldNot(HaveOccurred())
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
					Ω(pingErr).Should(HaveOccurred())
					Ω(pingErr.Error()).Should(ContainSubstring("status: 502"))
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
				Ω(resourceErr).ShouldNot(HaveOccurred())
			})

			It("returns the preset capacity", func() {
				expectedResources := executor.ExecutorResources{
					MemoryMB:   int(gardenCapacity.MemoryInBytes / 1024 / 1024),
					DiskMB:     int(gardenCapacity.DiskInBytes / 1024 / 1024),
					Containers: int(gardenCapacity.MaxContainers),
				}
				Ω(resources).Should(Equal(expectedResources))
			})
		})

		Describe("allocating a container", func() {
			var (
				container executor.Container

				guid string

				allocatedContainer executor.Container
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
						Env: []models.EnvironmentVariable{
							{Name: "RUN_ENV1", Value: "run_val1"},
							{Name: "RUN_ENV2", Value: "run_val2"},
						},
					},
				}
			})

			JustBeforeEach(func() {
				allocatedContainer, allocErr = executorClient.AllocateContainer(container)
			})

			It("does not return an error", func() {
				Ω(allocErr).ShouldNot(HaveOccurred())
			})

			It("returns a container", func() {
				Ω(allocatedContainer.Guid).Should(Equal(guid))
				Ω(allocatedContainer.MemoryMB).Should(Equal(0))
				Ω(allocatedContainer.DiskMB).Should(Equal(0))
				Ω(allocatedContainer.Tags).Should(Equal(executor.Tags{"some-tag": "some-value"}))
				Ω(allocatedContainer.State).Should(Equal(executor.StateReserved))
				Ω(allocatedContainer.AllocatedAt).Should(BeNumerically("~", time.Now().UnixNano(), time.Second))
			})

			It("shows up in the container list", func() {
				containers, err := executorClient.ListContainers(nil)
				Ω(err).ShouldNot(HaveOccurred())
				Ω(containers).Should(HaveLen(1))
				Ω(containers[0].Guid).Should(Equal(allocatedContainer.Guid))
				Ω(containers[0].State).Should(Equal(executor.StateReserved))
			})

			Context("when allocated with memory and disk limits", func() {
				BeforeEach(func() {
					container.MemoryMB = 256
					container.DiskMB = 256
				})

				It("returns the limits on the container", func() {
					Ω(allocatedContainer.MemoryMB).Should(Equal(256))
					Ω(allocatedContainer.DiskMB).Should(Equal(256))
				})

				It("reduces the capacity by the amount reserved", func() {
					Ω(executorClient.RemainingResources()).Should(Equal(executor.ExecutorResources{
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
					Ω(allocErr).Should(HaveOccurred())
					Ω(allocErr).Should(Equal(executor.ErrLimitsInvalid))
				})
			})

			Context("when the guid is already taken", func() {
				JustBeforeEach(func() {
					Ω(allocErr).ShouldNot(HaveOccurred())
					_, allocErr = executorClient.AllocateContainer(container)
				})

				It("returns an error", func() {
					Ω(allocErr).Should(Equal(executor.ErrContainerGuidNotAvailable))
				})
			})

			Context("when a guid is not specified", func() {
				BeforeEach(func() {
					container.Guid = ""
				})

				It("returns an error", func() {
					Ω(allocErr).Should(Equal(executor.ErrGuidNotSpecified))
				})
			})

			Context("when there is no room", func() {
				BeforeEach(func() {
					container.MemoryMB = 999999999999999
					container.DiskMB = 999999999999999
				})

				It("returns an error", func() {
					Ω(allocErr).Should(Equal(executor.ErrInsufficientResourcesAvailable))
				})
			})

			Describe("running it", func() {
				var runErr error
				var events <-chan executor.Event

				JustBeforeEach(func() {
					var err error

					events, err = executorClient.SubscribeToEvents()
					Ω(err).ShouldNot(HaveOccurred())
					runErr = executorClient.RunContainer(guid)
				})

				itCompletesWithFailure := func(reason string) {
					It("eventually completes with failure", func() {
						Eventually(containerStatePoller(guid)).Should(Equal(executor.StateCompleted))

						container := getContainer(guid)
						Ω(container.RunResult.Failed).Should(BeTrue())
						Ω(container.RunResult.FailureReason).Should(Equal(reason))
					})
				}

				Context("when the container can be created", func() {
					var gardenContainer garden.Container

					JustBeforeEach(func() {
						gardenContainer = findGardenContainer(guid)
					})

					It("returns no error", func() {
						Ω(runErr).ShouldNot(HaveOccurred())
					})

					It("creates it with the configured owner", func() {
						info, err := gardenContainer.Info()
						Ω(err).ShouldNot(HaveOccurred())

						Ω(info.Properties["executor:owner"]).Should(Equal(ownerName))
					})

					It("sets global environment variables on the container", func() {
						output := gbytes.NewBuffer()

						process, err := gardenContainer.Run(garden.ProcessSpec{
							Path: "env",
						}, garden.ProcessIO{
							Stdout: output,
						})
						Ω(err).ShouldNot(HaveOccurred())
						Ω(process.Wait()).Should(Equal(0))

						Ω(output.Contents()).Should(ContainSubstring("ENV1=val1"))
						Ω(output.Contents()).Should(ContainSubstring("ENV2=val2"))
					})

					It("saves the succeeded run result", func() {
						Eventually(containerStatePoller(guid)).Should(Equal(executor.StateCompleted))

						container := getContainer(guid)
						Ω(container.RunResult.Failed).Should(BeFalse())
						Ω(container.RunResult.FailureReason).Should(BeEmpty())
					})

					Context("when listening for events", func() {
						It("emits a completed container event on completion", func() {
							var event executor.Event
							Eventually(containerEventPoller(events, &event), 5).Should(Equal(executor.EventTypeContainerComplete))

							completeEvent := event.(executor.ContainerCompleteEvent)
							Ω(completeEvent.Container.State).Should(Equal(executor.StateCompleted))
							Ω(completeEvent.Container.RunResult.Failed).Should(BeFalse())
						})

						Describe("shutting down", func() {
							It("exits and ends the event stream", func() {
								process.Signal(os.Interrupt)

								Eventually(events, 5).Should(BeClosed())
								Eventually(process.Wait(), 5).Should(Receive(BeNil()))
							})
						})
					})

					Context("when created without a monitor action", func() {
						Context("while the action is running", func() {
							BeforeEach(func() {
								container.Action = &models.RunAction{
									Path: "sh",
									Args: []string{"-c", "while true; do sleep 1; done"},
								}
							})

							It("reports the health as 'unmonitored'", func() {
								Eventually(containerHealthPoller(guid)).Should(Equal(executor.HealthUnmonitored))
								Consistently(containerHealthPoller(guid)).Should(Equal(executor.HealthUnmonitored))
							})
						})
					})

					Context("when created with a monitor action", func() {
						itFailsOnlyIfMonitoringSucceedsAndThenFails := func() {
							Context("when monitoring succeeds", func() {
								BeforeEach(func() {
									container.Monitor = &models.RunAction{
										Path: "true",
									}
								})

								It("reports the health as 'up'", func() {
									Eventually(containerHealthPoller(guid)).Should(Equal(executor.HealthUp))
									Consistently(containerHealthPoller(guid)).Should(Equal(executor.HealthUp))
								})

								It("does not stop the container", func() {
									Consistently(containerStatePoller(guid)).ShouldNot(Equal(executor.StateCompleted))
								})
							})

							Context("when monitoring fails", func() {
								BeforeEach(func() {
									container.Monitor = &models.RunAction{
										Path: "false",
									}
								})

								It("reports the health as 'down' and does not stop the container", func() {
									Eventually(containerHealthPoller(guid)).Should(Equal(executor.HealthDown))
									Consistently(containerHealthPoller(guid)).Should(Equal(executor.HealthDown))
								})

								It("does not stop the container", func() {
									Consistently(containerStatePoller(guid)).ShouldNot(Equal(executor.StateCompleted))
								})
							})

							Context("when monitoring succeeds and then fails", func() {
								BeforeEach(func() {
									container.Monitor = &models.RunAction{
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

								It("reports the health as 'up' then 'down'", func() {
									Eventually(containerHealthPoller(guid)).Should(Equal(executor.HealthUp))
									Eventually(containerHealthPoller(guid)).Should(Equal(executor.HealthDown))
								})

								It("stops the container", func() {
									Eventually(containerStatePoller(guid)).Should(Equal(executor.StateCompleted))
								})
							})
						}

						Context("when the action succeeds and exits immediately (daemonization)", func() {
							BeforeEach(func() {
								container.Action = &models.RunAction{
									Path: "true",
								}
							})

							itFailsOnlyIfMonitoringSucceedsAndThenFails()
						})

						Context("while the action does not stop running ", func() {
							BeforeEach(func() {
								container.Action = &models.RunAction{
									Path: "sh",
									Args: []string{"-c", "while true; do sleep 1; done"},
								}
							})

							itFailsOnlyIfMonitoringSucceedsAndThenFails()
						})

						Context("when the action fails", func() {
							BeforeEach(func() {
								container.Action = &models.RunAction{
									Path: "false",
								}
							})

							Context("even if the monitoring succeeds", func() {
								BeforeEach(func() {
									container.Monitor = &models.RunAction{
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
								Ω(err).ShouldNot(HaveOccurred())
							}, 5)
						})
					})

					Context("when running fails", func() {
						BeforeEach(func() {
							container.Action = &models.RunAction{
								Path: "false",
							}
						})

						It("saves the failed result and reason", func() {
							Eventually(containerStatePoller(guid)).Should(Equal(executor.StateCompleted))

							container := getContainer(guid)
							Ω(container.RunResult.Failed).Should(BeTrue())
							Ω(container.RunResult.FailureReason).Should(Equal("Exited with status 1"))
						})

						Context("when listening for events", func() {
							It("emits a completed container event", func() {
								var event executor.Event
								Eventually(containerEventPoller(events, &event), 5).Should(Equal(executor.EventTypeContainerComplete))

								completeEvent := event.(executor.ContainerCompleteEvent)
								Ω(completeEvent.Container.State).Should(Equal(executor.StateCompleted))
								Ω(completeEvent.Container.RunResult.Failed).Should(BeTrue())
								Ω(completeEvent.Container.RunResult.FailureReason).Should(Equal("Exited with status 1"))
							})
						})
					})
				})

				Context("when the container cannot be created", func() {
					BeforeEach(func() {
						container.RootFSPath = "gopher://example.com"
					})

					It("does not immediately return an error", func() {
						Ω(runErr).ShouldNot(HaveOccurred())
					})

					Context("when listening for events", func() {
						itCompletesWithFailure("failed to initialize container")
					})
				})
			})
		})

		Describe("running a bogus guid", func() {
			It("returns an error", func() {
				err := executorClient.RunContainer("bogus")
				Ω(err).Should(Equal(executor.ErrContainerNotFound))
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
					Ω(err).ShouldNot(HaveOccurred())

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
					Ω(err).ShouldNot(HaveOccurred())
					Ω(containers).Should(HaveLen(1))
					Ω(containers[0].Guid).Should(Equal(guid))
					Ω(containers[0].State).Should(Equal(executor.StateReserved))
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
						Args: []string{"-c", "while true; do sleep 1; done"},
					},
				})

				err := executorClient.RunContainer(guid)
				Ω(err).ShouldNot(HaveOccurred())

				Eventually(containerStatePoller(guid)).Should(Equal(executor.StateCreated))
			})

			Describe("deleting it", func() {
				It("does not return an error", func() {
					err := executorClient.DeleteContainer(guid)
					Ω(err).ShouldNot(HaveOccurred())
				})

				It("deletes the container", func() {
					err := executorClient.DeleteContainer(guid)
					Ω(err).ShouldNot(HaveOccurred())

					Eventually(func() error {
						_, err := gardenClient.Lookup(guid)
						return err
					}).Should(HaveOccurred())
				})
			})

			Describe("listing containers", func() {
				It("shows up in the container list in created state", func() {
					containers, err := executorClient.ListContainers(nil)
					Ω(err).ShouldNot(HaveOccurred())
					Ω(containers).Should(HaveLen(1))
					Ω(containers[0].Guid).Should(Equal(guid))
					Ω(containers[0].State).Should(Equal(executor.StateCreated))
				})
			})

			Describe("remaining resources", func() {
				It("has the container's reservation subtracted", func() {
					remaining, err := executorClient.RemainingResources()
					Ω(err).ShouldNot(HaveOccurred())

					Ω(remaining.MemoryMB).Should(Equal(int(gardenCapacity.MemoryInBytes/1024/1024) - 64))
					Ω(remaining.DiskMB).Should(Equal(int(gardenCapacity.DiskInBytes/1024/1024) - 64))
				})

				Context("when the container disappears", func() {
					It("eventually goes back to the total resources", func() {
						// wait for the container to be present
						findGardenContainer(guid)

						// kill it
						err := gardenClient.Destroy(guid)
						Ω(err).ShouldNot(HaveOccurred())

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
					Ω(streamErr).Should(HaveOccurred())
				})
			})

			Context("when the container is running", func() {
				var container garden.Container

				JustBeforeEach(func() {
					guid = allocNewContainer(executor.Container{
						Action: &models.RunAction{
							Path: "sh",
							Args: []string{
								"-c", `while true; do	sleep 1; done`,
							},
						},
					})

					err := executorClient.RunContainer(guid)
					Ω(err).ShouldNot(HaveOccurred())

					container = findGardenContainer(guid)

					process, err := container.Run(garden.ProcessSpec{
						Path: "sh",
						Args: []string{"-c", "mkdir some; echo hello > some/path"},
					}, garden.ProcessIO{})
					Ω(err).ShouldNot(HaveOccurred())
					Ω(process.Wait()).Should(Equal(0))

					stream, streamErr = executorClient.GetFiles(guid, "some/path")
				})

				It("does not error", func() {
					Ω(streamErr).ShouldNot(HaveOccurred())
				})

				It("returns a stream of the contents of the file", func() {
					tarReader := tar.NewReader(stream)

					header, err := tarReader.Next()
					Ω(err).ShouldNot(HaveOccurred())

					Ω(header.FileInfo().Name()).Should(Equal("path"))
					Ω(ioutil.ReadAll(tarReader)).Should(Equal([]byte("hello\n")))
				})
			})
		})

		Describe("pruning the registry", func() {
			It("continously prunes the registry", func() {
				_, err := executorClient.AllocateContainer(executor.Container{
					Guid: "some-handle",

					MemoryMB: 1024,
					DiskMB:   1024,
				})
				Ω(err).ShouldNot(HaveOccurred())

				Ω(executorClient.ListContainers(nil)).Should(HaveLen(1))

				Eventually(func() interface{} {
					containers, err := executorClient.ListContainers(nil)
					Ω(err).ShouldNot(HaveOccurred())

					return containers
				}, pruningInterval*3).Should(BeEmpty())
			})
		})

		Describe("when the executor receives the TERM signal", func() {
			It("exits successfully", func() {
				process.Signal(syscall.SIGTERM)
				Eventually(runner, 2).Should(gexec.Exit())
			})
		})

		Describe("when the executor receives the INT signal", func() {
			It("exits successfully", func() {
				process.Signal(syscall.SIGINT)
				Eventually(runner, 2).Should(gexec.Exit())
			})
		})

		Describe("listing containers", func() {
			Context("with no containers", func() {
				It("returns an empty set of containers", func() {
					Ω(executorClient.ListContainers(nil)).Should(BeEmpty())
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
						Ω(err).ShouldNot(HaveOccurred())
						Ω(containers).Should(HaveLen(1))
						Ω(containers[0].Guid).Should(Equal(guid))
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
							Ω(err).ShouldNot(HaveOccurred())
							Ω(containers).Should(HaveLen(1))
							Ω(containers[0].Guid).Should(Equal(guid))
						})

						It("filters by and-ing the requested tags", func() {
							Ω(executorClient.ListContainers(executor.Tags{
								"some-tag":  "some-value",
								"bogus-tag": "bogus-value",
							})).Should(BeEmpty())
						})
					})

					Describe("listing by non-matching tags", func() {
						It("does not include the allocated container", func() {
							Ω(executorClient.ListContainers(executor.Tags{
								"some-tag": "bogus-value",
							})).Should(BeEmpty())
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
							Path: "sh",
							Args: []string{"-c", "echo -n .$CF_INSTANCE_ADDR. | nc -l 8080"},
						},
					})

					err := executorClient.RunContainer(guid)
					Ω(err).ShouldNot(HaveOccurred())

					Eventually(containerStatePoller(guid)).Should(Equal(executor.StateCreated))

					container := getContainer(guid)

					externalAddr = fmt.Sprintf("%s:%d", container.ExternalIP, container.Ports[0].HostPort)

					var conn net.Conn
					Eventually(func() error {
						var err error
						conn, err = net.Dial("tcp", externalAddr)
						return err
					}).ShouldNot(HaveOccurred())

					containerResponse, err = ioutil.ReadAll(conn)
					Ω(err).ShouldNot(HaveOccurred())
				})

				Context("when exportNetworkEnvVars is set", func() {
					BeforeEach(func() {
						exportNetworkEnvVars = true
					})

					It("echoes back the correct CF_INSTANCE_ADDR", func() {
						Ω(string(containerResponse)).Should(Equal("." + externalAddr + "."))
					})
				})

				Context("when exportNetworkEnvVars is not set", func() {
					BeforeEach(func() {
						exportNetworkEnvVars = false
					})

					It("echoes back an empty CF_INSTANCE_ADDR", func() {
						Ω(string(containerResponse)).Should(Equal(".."))
					})
				})
			})
		})
	})

	Describe("when Garden is unavailable", func() {
		JustBeforeEach(func() {
			ginkgomon.Interrupt(gardenProcess)

			runner.StartCheck = ""
			process = ginkgomon.Invoke(runner)
		})

		Context("and gardenserver starts up later", func() {
			JustBeforeEach(func() {
				gardenProcess = ginkgomon.Invoke(componentMaker.GardenLinux())
			})

			It("should connect", func() {
				Eventually(runner.Buffer(), 5*time.Second).Should(gbytes.Say("started"))
			})
		})

		Context("and never starts", func() {
			AfterEach(func() {
				gardenProcess = ginkgomon.Invoke(componentMaker.GardenLinux())
			})

			It("should not exit and continue waiting for a connection", func() {
				Consistently(runner.Buffer()).ShouldNot(gbytes.Say("started"))
				Ω(runner).ShouldNot(gexec.Exit())
			})
		})
	})
})
