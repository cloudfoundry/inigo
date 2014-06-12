package inigo_test

import (
	"fmt"
	"syscall"
	"time"

	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/loggredile"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/nu7hatch/gouuid"
	"github.com/tedsuo/ifrit"

	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

var _ = Describe("Convergence to desired state", func() {
	var desiredAppRequest models.DesireAppRequestFromCC
	var appId string
	var processGuid string

	var tpsProcess ifrit.Process
	var tpsAddr string

	var logOutput *gbytes.Buffer
	var stop chan<- bool

	CONVERGE_REPEAT_INTERVAL := time.Second
	PENDING_AUCTION_KICK_THRESHOLD := time.Second
	CLAIMED_AUCTION_REAP_THRESHOLD := 5 * time.Second
	WAIT_FOR_MULTIPLE_CONVERGE_INTERVAL := CONVERGE_REPEAT_INTERVAL * 3

	Describe("Executor fault tolerance", func() {
		BeforeEach(func() {
			guid, err := uuid.NewV4()
			if err != nil {
				panic("Failed to generate App ID")
			}
			appId = guid.String()

			guid, err = uuid.NewV4()
			if err != nil {
				panic("Failed to generate Process Guid")
			}
			processGuid = guid.String()

			suiteContext.FileServerRunner.Start()
			suiteContext.AuctioneerRunner.Start(AUCTION_MAX_ROUNDS)
			suiteContext.AppManagerRunner.Start()
			suiteContext.RouteEmitterRunner.Start()
			suiteContext.RouterRunner.Start()
			suiteContext.ConvergerRunner.Start(CONVERGE_REPEAT_INTERVAL, 30*time.Second, 5*time.Minute, PENDING_AUCTION_KICK_THRESHOLD, CLAIMED_AUCTION_REAP_THRESHOLD)

			tpsProcess = ifrit.Envoke(suiteContext.TPSRunner)
			tpsAddr = fmt.Sprintf("http://127.0.0.1:%d", suiteContext.TPSPort)

			archive_helper.CreateZipArchive("/tmp/simple-echo-droplet.zip", fixtures.HelloWorldIndexApp())
			inigo_server.UploadFile("simple-echo-droplet.zip", "/tmp/simple-echo-droplet.zip")

			suiteContext.FileServerRunner.ServeFile("some-lifecycle-bundle.tgz", suiteContext.SharedContext.CircusZipPath)

			logOutput, stop = loggredile.StreamIntoGBuffer(
				suiteContext.LoggregatorRunner.Config.OutgoingPort,
				fmt.Sprintf("/tail/?app=%s", appId),
				"App",
			)
		})

		AfterEach(func() {
			tpsProcess.Signal(syscall.SIGKILL)
			Eventually(tpsProcess.Wait()).Should(Receive())
			close(stop)
		})

		Context("When starting a long-running process and then bouncing the executor", func() {
			BeforeEach(func() {
				suiteContext.ExecutorRunner.Start()
				suiteContext.RepRunner.Start()

				desiredAppRequest = models.DesireAppRequestFromCC{
					ProcessGuid:  processGuid,
					DropletUri:   inigo_server.DownloadUrl("simple-echo-droplet.zip"),
					Stack:        suiteContext.RepStack,
					Environment:  []models.EnvironmentVariable{{Key: "VCAP_APPLICATION", Value: "{}"}},
					NumInstances: 1,
					Routes:       []string{"route-to-simple"},
					StartCommand: "./run",
					LogGuid:      appId,
				}

				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
				Ω(err).ShouldNot(HaveOccurred())

				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(HaveLen(1))
				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(Equal([]string{"0"}))
			})

			It("Eventually brings the long-running process up", func() {
				suiteContext.ExecutorRunner.Stop()

				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(BeEmpty())
				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(BeEmpty())

				suiteContext.ExecutorRunner.Start()

				running_lrps_poller = helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
				hello_world_instance_poller = helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(HaveLen(1))
				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(Equal([]string{"0"}))
			})
		})

		Context("When trying to start a long-running process before the executor is up", func() {
			BeforeEach(func() {
				suiteContext.RepRunner.Start()

				desiredAppRequest = models.DesireAppRequestFromCC{
					ProcessGuid:  processGuid,
					DropletUri:   inigo_server.DownloadUrl("simple-echo-droplet.zip"),
					Stack:        suiteContext.RepStack,
					Environment:  []models.EnvironmentVariable{{Key: "VCAP_APPLICATION", Value: "{}"}},
					NumInstances: 1,
					Routes:       []string{"route-to-simple"},
					StartCommand: "./run",
					LogGuid:      appId,
				}

				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
				Ω(err).ShouldNot(HaveOccurred())

				time.Sleep(WAIT_FOR_MULTIPLE_CONVERGE_INTERVAL)

				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Ω(running_lrps_poller()).Should(BeEmpty())
				Ω(hello_world_instance_poller()).Should(BeEmpty())
			})

			It("Eventually brings the long-running process up", func() {
				suiteContext.ExecutorRunner.Start()

				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(HaveLen(1))
				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(Equal([]string{"0"}))
			})
		})

		Context("When there is a runaway long-running process with no corresponding desired process", func() {
			BeforeEach(func() {
				suiteContext.RepRunner.Start()
				suiteContext.ExecutorRunner.Start()

				desiredAppRequest = models.DesireAppRequestFromCC{
					ProcessGuid:  processGuid,
					DropletUri:   inigo_server.DownloadUrl("simple-echo-droplet.zip"),
					Stack:        suiteContext.RepStack,
					Environment:  []models.EnvironmentVariable{{Key: "VCAP_APPLICATION", Value: "{}"}},
					NumInstances: 1,
					Routes:       []string{"route-to-simple"},
					StartCommand: "./run",
					LogGuid:      appId,
				}

				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
				Ω(err).ShouldNot(HaveOccurred())

				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(HaveLen(1))
				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(Equal([]string{"0"}))
			})

			It("Eventually brings the long-running process down", func() {
				suiteContext.RepRunner.Stop()

				desiredAppStopRequest := models.DesireAppRequestFromCC{
					ProcessGuid:  processGuid,
					DropletUri:   inigo_server.DownloadUrl("simple-echo-droplet.zip"),
					Stack:        suiteContext.RepStack,
					Environment:  []models.EnvironmentVariable{{Key: "VCAP_APPLICATION", Value: "{}"}},
					NumInstances: 0,
					Routes:       []string{"route-to-simple"},
					StartCommand: "./run",
					LogGuid:      appId,
				}

				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppStopRequest.ToJSON())
				Ω(err).ShouldNot(HaveOccurred())

				time.Sleep(WAIT_FOR_MULTIPLE_CONVERGE_INTERVAL)
				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
				Ω(running_lrps_poller()).Should(HaveLen(1))

				suiteContext.RepRunner.Start()

				running_lrps_poller = helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(BeEmpty())
				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(BeEmpty())
			})
		})

		Context("When a stop message for an instance of a long-running process is lost", func() {
			BeforeEach(func() {
				suiteContext.RepRunner.Start()
				suiteContext.ExecutorRunner.Start()

				desiredAppRequest = models.DesireAppRequestFromCC{
					ProcessGuid:  processGuid,
					DropletUri:   inigo_server.DownloadUrl("simple-echo-droplet.zip"),
					Stack:        suiteContext.RepStack,
					Environment:  []models.EnvironmentVariable{{Key: "VCAP_APPLICATION", Value: "{}"}},
					NumInstances: 2,
					Routes:       []string{"route-to-simple"},
					StartCommand: "./run",
					LogGuid:      appId,
				}

				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
				Ω(err).ShouldNot(HaveOccurred())

				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(HaveLen(2))
				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(Equal([]string{"0", "1"}))
			})

			It("Eventually brings the long-running process down", func() {
				suiteContext.RepRunner.Stop()

				desiredAppStopRequest := models.DesireAppRequestFromCC{
					ProcessGuid:  processGuid,
					DropletUri:   inigo_server.DownloadUrl("simple-echo-droplet.zip"),
					Stack:        suiteContext.RepStack,
					Environment:  []models.EnvironmentVariable{{Key: "VCAP_APPLICATION", Value: "{}"}},
					NumInstances: 1,
					Routes:       []string{"route-to-simple"},
					StartCommand: "./run",
					LogGuid:      appId,
				}

				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppStopRequest.ToJSON())
				Ω(err).ShouldNot(HaveOccurred())

				time.Sleep(WAIT_FOR_MULTIPLE_CONVERGE_INTERVAL)
				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
				Ω(running_lrps_poller()).Should(HaveLen(2))

				suiteContext.RepRunner.Start()

				running_lrps_poller = helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(HaveLen(1))
				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(Equal([]string{"0"}))
			})
		})
	})

	Describe("Auctioneer Fault Tolerance", func() {
		BeforeEach(func() {
			guid, err := uuid.NewV4()
			if err != nil {
				panic("Failed to generate App ID")
			}
			appId = guid.String()

			guid, err = uuid.NewV4()
			if err != nil {
				panic("Failed to generate Process Guid")
			}
			processGuid = guid.String()

			suiteContext.FileServerRunner.Start()
			suiteContext.AppManagerRunner.Start()
			suiteContext.RouteEmitterRunner.Start()
			suiteContext.RouterRunner.Start()
			suiteContext.ConvergerRunner.Start(CONVERGE_REPEAT_INTERVAL, 30*time.Second, 5*time.Minute, PENDING_AUCTION_KICK_THRESHOLD, CLAIMED_AUCTION_REAP_THRESHOLD)
			suiteContext.RepRunner.Start()

			tpsProcess = ifrit.Envoke(suiteContext.TPSRunner)
			tpsAddr = fmt.Sprintf("http://127.0.0.1:%d", suiteContext.TPSPort)

			archive_helper.CreateZipArchive("/tmp/simple-echo-droplet.zip", fixtures.HelloWorldIndexApp())
			inigo_server.UploadFile("simple-echo-droplet.zip", "/tmp/simple-echo-droplet.zip")

			suiteContext.FileServerRunner.ServeFile("some-lifecycle-bundle.tgz", suiteContext.SharedContext.CircusZipPath)

			logOutput, stop = loggredile.StreamIntoGBuffer(
				suiteContext.LoggregatorRunner.Config.OutgoingPort,
				fmt.Sprintf("/tail/?app=%s", appId),
				"App",
			)
		})

		AfterEach(func() {
			tpsProcess.Signal(syscall.SIGKILL)
			Eventually(tpsProcess.Wait()).Should(Receive())
			close(stop)
		})

		Context("When trying to start an auction before an Auctioneer is up", func() {
			BeforeEach(func() {
				suiteContext.ExecutorRunner.Start()

				desiredAppRequest = models.DesireAppRequestFromCC{
					ProcessGuid:  processGuid,
					DropletUri:   inigo_server.DownloadUrl("simple-echo-droplet.zip"),
					Stack:        suiteContext.RepStack,
					Environment:  []models.EnvironmentVariable{{Key: "VCAP_APPLICATION", Value: "{}"}},
					NumInstances: 1,
					Routes:       []string{"route-to-simple"},
					StartCommand: "./run",
					LogGuid:      appId,
				}

				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
				Ω(err).ShouldNot(HaveOccurred())

				time.Sleep(PENDING_AUCTION_KICK_THRESHOLD)
				time.Sleep(WAIT_FOR_MULTIPLE_CONVERGE_INTERVAL)

				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Ω(running_lrps_poller()).Should(BeEmpty())
				Ω(hello_world_instance_poller()).Should(BeEmpty())
			})

			It("Eventually brings the long-running process up", func() {
				suiteContext.AuctioneerRunner.Start(AUCTION_MAX_ROUNDS)

				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(HaveLen(1))
				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(Equal([]string{"0"}))
			})
		})

		Context("When an auctioneer dies before the auction completes", func() {
			BeforeEach(func() {
				//start the auctioneer and have it keep trying the auction for ever
				//note: there is no executor running so the auction will not succeed
				suiteContext.AuctioneerRunner.Start(100000000)

				desiredAppRequest = models.DesireAppRequestFromCC{
					ProcessGuid:  processGuid,
					DropletUri:   inigo_server.DownloadUrl("simple-echo-droplet.zip"),
					Stack:        suiteContext.RepStack,
					Environment:  []models.EnvironmentVariable{{Key: "VCAP_APPLICATION", Value: "{}"}},
					NumInstances: 1,
					Routes:       []string{"route-to-simple"},
					StartCommand: "./run",
					LogGuid:      appId,
				}

				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
				Ω(err).ShouldNot(HaveOccurred())

				time.Sleep(PENDING_AUCTION_KICK_THRESHOLD)
				time.Sleep(WAIT_FOR_MULTIPLE_CONVERGE_INTERVAL)

				suiteContext.AuctioneerRunner.Stop()

				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Ω(running_lrps_poller()).Should(BeEmpty())
				Ω(hello_world_instance_poller()).Should(BeEmpty())
			})

			It("Eventually brings the long-running process up", func() {
				suiteContext.ExecutorRunner.Start()
				suiteContext.AuctioneerRunner.Start(AUCTION_MAX_ROUNDS)

				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(HaveLen(1))
				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(Equal([]string{"0"}))
			})
		})
	})
})
