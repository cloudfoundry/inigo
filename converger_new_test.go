package inigo_test

import (
	"fmt"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cloudfoundry-incubator/garden/warden"
	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/world"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"

	"github.com/cloudfoundry/yagnats"

	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"

	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

// import (
// 	"fmt"
// 	"syscall"
// 	"time"

// 	"github.com/cloudfoundry-incubator/inigo/fixtures"
// 	"github.com/cloudfoundry-incubator/inigo/helpers"
// 	"github.com/cloudfoundry-incubator/inigo/loggredile"
// 	"github.com/cloudfoundry-incubator/runtime-schema/models"
// 	"github.com/nu7hatch/gouuid"
// 	"github.com/tedsuo/ifrit"
// 	"github.com/tedsuo/ifrit/grouper"

// 	"github.com/cloudfoundry-incubator/inigo/inigo_server"
// 	. "github.com/onsi/ginkgo"
// 	. "github.com/onsi/gomega"
// 	"github.com/onsi/gomega/gbytes"
// 	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
// )

var _ = XDescribe("Convergence to desired state", func() {
	// 	var desiredAppRequest models.DesireAppRequestFromCC
	// 	var appId string
	// 	var processGuid string

	// 	var processGroup ifrit.Process

	// 	var tpsAddr string

	// 	var logOutput *gbytes.Buffer
	// 	var stop chan<- bool

	// 	CONVERGE_REPEAT_INTERVAL := time.Second
	// 	PENDING_AUCTION_KICK_THRESHOLD := time.Second
	// 	CLAIMED_AUCTION_REAP_THRESHOLD := 5 * time.Second
	// 	WAIT_FOR_MULTIPLE_CONVERGE_INTERVAL := CONVERGE_REPEAT_INTERVAL * 3

	// 	constructDesiredAppRequest := func(numInstances int) models.DesireAppRequestFromCC {
	// 		return models.DesireAppRequestFromCC{
	// 			ProcessGuid:  processGuid,
	// 			DropletUri:   inigo_server.DownloadUrl("simple-echo-droplet.zip"),
	// 			Stack:        suiteContext.RepStack,
	// 			Environment:  []models.EnvironmentVariable{{Name: "VCAP_APPLICATION", Value: "{}"}},
	// 			NumInstances: numInstances,
	// 			Routes:       []string{"route-to-simple"},
	// 			StartCommand: "./run",
	// 			LogGuid:      appId,
	// 		}
	// 	}
	var appId string

	var wardenClient warden.Client

	var natsClient yagnats.NATSClient

	var fileServerStaticDir string

	var plumbing ifrit.Process
	var runtime ifrit.Process
	var execProcess ifrit.Process

	var runningMessage []byte

	BeforeEach(func() {
		appId = factories.GenerateGuid()

		wardenLinux := componentMaker.WardenLinux()
		wardenClient = wardenLinux.NewClient()

		fileServer, dir := componentMaker.FileServer()
		fileServerStaticDir = dir

		natsClient = yagnats.NewClient()

		plumbing = grouper.EnvokeGroup(grouper.RunGroup{
			"etcd":         componentMaker.Etcd(),
			"nats":         componentMaker.NATS(),
			"warden-linux": wardenLinux,
		})

		// 	convergeRepeatInterval,   kickPendingTaskDuration, expireClaimedTaskDuration, kickPendingLRPStartAuctionDuration, expireClaimedLRPStartAuctionDuration time.Duration,
		// 	CONVERGE_REPEAT_INTERVAL, 30*time.Second,          5*time.Minute,             PENDING_AUCTION_KICK_THRESHOLD,     CLAIMED_AUCTION_REAP_THRESHOLD)

		convergerArgs := []string{
			"-convergeRepeatInterval", (time.Second).String(),
			"-kickPendingTaskDuration", (30 * time.Second).String(),
			"-kickPendingLRPStartAuctionDuration", (time.Second).String(),
			"-expireClaimedTaskDuration", (5 * time.Minute).String(),
			"-expireClaimedLRPStartAuctionDuration", (5 * time.Second).String(),
		}

		runtime = grouper.EnvokeGroup(grouper.RunGroup{
			"cc":             componentMaker.FakeCC(),
			"tps":            componentMaker.TPS(),
			"nsync-listener": componentMaker.NsyncListener(),
			"rep":            componentMaker.Rep(),
			"file-server":    fileServer,
			"auctioneer":     componentMaker.Auctioneer(),
			"route-emitter":  componentMaker.RouteEmitter(),
			"router":         componentMaker.Router(),
			"loggregator":    componentMaker.Loggregator(),
			"converger":      componentMaker.Converger(convergerArgs...),
		})

		err := natsClient.Connect(&yagnats.ConnectionInfo{
			Addr: componentMaker.Addresses.NATS,
		})
		Ω(err).ShouldNot(HaveOccurred())

		inigo_server.Start(wardenClient)

		archive_helper.CreateZipArchive("/tmp/simple-echo-droplet.zip", fixtures.HelloWorldIndexApp())
		inigo_server.UploadFile("simple-echo-droplet.zip", "/tmp/simple-echo-droplet.zip")

		cp(
			componentMaker.Artifacts.Circuses[componentMaker.Stack],
			filepath.Join(fileServerStaticDir, world.CircusZipFilename),
		)
	})

	AfterEach(func() {
		inigo_server.Stop(wardenClient)

		execProcess.Signal(syscall.SIGKILL)
		Eventually(execProcess.Wait(), 5*time.Second).Should(Receive())

		runtime.Signal(syscall.SIGKILL)
		Eventually(runtime.Wait(), 5*time.Second).Should(Receive())

		plumbing.Signal(syscall.SIGKILL)
		Eventually(plumbing.Wait(), 5*time.Second).Should(Receive())
	})

	Describe("Executor fault tolerance", func() {
		Context("When starting a long-running process", func() {
			BeforeEach(func() {
				execProcess = ifrit.Envoke(componentMaker.Executor())

				runningMessage = []byte(
					fmt.Sprintf(
						`
            {
              "process_guid": "process-guid",
              "droplet_uri": "%s",
              "stack": "%s",
              "start_command": "./run",
              "num_instances": 1,
              "environment":[{"name":"VCAP_APPLICATION", "value":"{}"}],
              "routes": ["route-to-simple"],
              "log_guid": "%s"
            }
          `,
						inigo_server.DownloadUrl("simple-echo-droplet.zip"),
						componentMaker.Stack,
						appId,
					),
				)

				err := natsClient.Publish("diego.desire.app", runningMessage)
				Ω(err).ShouldNot(HaveOccurred())

				running_lrps_poller := helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, "process-guid")
				hello_world_instance_poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-to-simple")
				Eventually(running_lrps_poller, LONG_TIMEOUT*3).Should(HaveLen(1))
				Eventually(hello_world_instance_poller, LONG_TIMEOUT*3, 1).Should(Equal([]string{"0"}))
			})

			Context("And then bouncing the executor", func() {
				BeforeEach(func() {
					execProcess.Signal(syscall.SIGKILL)
					Eventually(execProcess.Wait(), 5*time.Second).Should(Receive())

					running_lrps_poller := helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, "process-guid")
					hello_world_instance_poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-to-simple")
					Eventually(running_lrps_poller, LONG_TIMEOUT*3).Should(BeEmpty())
					Eventually(hello_world_instance_poller, LONG_TIMEOUT*3, 1).Should(BeEmpty())

					execProcess = ifrit.Envoke(componentMaker.Executor())
				})

				It("Eventually brings the long-running process up", func() {
					running_lrps_poller := helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, "process-guid")
					hello_world_instance_poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-to-simple")
					Eventually(running_lrps_poller, LONG_TIMEOUT*3).Should(HaveLen(1))
					Eventually(hello_world_instance_poller, LONG_TIMEOUT*3, 1).Should(Equal([]string{"0"}))
				})
			})
		})

		// 		Context("When trying to start a long-running process before the executor is up", func() {
		// 			BeforeEach(func() {
		// 				suiteContext.RepRunner.Start()

		// 				desiredAppRequest = constructDesiredAppRequest(1)

		// 				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
		// 				Ω(err).ShouldNot(HaveOccurred())

		// 				time.Sleep(WAIT_FOR_MULTIPLE_CONVERGE_INTERVAL)

		// 				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
		// 				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
		// 				Ω(running_lrps_poller()).Should(BeEmpty())
		// 				Ω(hello_world_instance_poller()).Should(BeEmpty())
		// 			})

		// 			It("Eventually brings the long-running process up", func() {
		// 				suiteContext.ExecutorRunner.Start()

		// 				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
		// 				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
		// 				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(HaveLen(1))
		// 				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(Equal([]string{"0"}))
		// 			})
		// 		})

		// 		Context("When there is a runaway long-running process with no corresponding desired process", func() {
		// 			BeforeEach(func() {
		// 				suiteContext.RepRunner.Start()
		// 				suiteContext.ExecutorRunner.Start()

		// 				desiredAppRequest = constructDesiredAppRequest(1)

		// 				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
		// 				Ω(err).ShouldNot(HaveOccurred())

		// 				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
		// 				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
		// 				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(HaveLen(1))
		// 				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(Equal([]string{"0"}))
		// 			})

		// 			It("Eventually brings the long-running process down", func() {
		// 				suiteContext.ConvergerRunner.Stop()
		// 				suiteContext.RepRunner.Stop()

		// 				desiredAppStopRequest := constructDesiredAppRequest(0)

		// 				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppStopRequest.ToJSON())
		// 				Ω(err).ShouldNot(HaveOccurred())

		// 				time.Sleep(WAIT_FOR_MULTIPLE_CONVERGE_INTERVAL)
		// 				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
		// 				Ω(running_lrps_poller()).Should(HaveLen(1))

		// 				suiteContext.RepRunner.Start()
		// 				suiteContext.ConvergerRunner.Start(CONVERGE_REPEAT_INTERVAL, 30*time.Second, 5*time.Minute, PENDING_AUCTION_KICK_THRESHOLD, CLAIMED_AUCTION_REAP_THRESHOLD)

		// 				running_lrps_poller = helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
		// 				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
		// 				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(BeEmpty())
		// 				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(BeEmpty())
		// 			})
		// 		})

		// 		Context("When a stop message for an instance of a long-running process is lost", func() {
		// 			BeforeEach(func() {
		// 				suiteContext.RepRunner.Start()
		// 				suiteContext.ExecutorRunner.Start()

		// 				desiredAppRequest = constructDesiredAppRequest(2)

		// 				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
		// 				Ω(err).ShouldNot(HaveOccurred())

		// 				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
		// 				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
		// 				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(HaveLen(2))
		// 				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(Equal([]string{"0", "1"}))
		// 			})

		// 			It("Eventually brings the long-running process down", func() {
		// 				suiteContext.ConvergerRunner.Stop()
		// 				suiteContext.RepRunner.Stop()

		// 				desiredAppScaleDownRequest := constructDesiredAppRequest(1)

		// 				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppScaleDownRequest.ToJSON())
		// 				Ω(err).ShouldNot(HaveOccurred())

		// 				time.Sleep(WAIT_FOR_MULTIPLE_CONVERGE_INTERVAL)
		// 				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
		// 				Ω(running_lrps_poller()).Should(HaveLen(2))

		// 				suiteContext.RepRunner.Start()
		// 				suiteContext.ConvergerRunner.Start(CONVERGE_REPEAT_INTERVAL, 30*time.Second, 5*time.Minute, PENDING_AUCTION_KICK_THRESHOLD, CLAIMED_AUCTION_REAP_THRESHOLD)

		// 				running_lrps_poller = helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
		// 				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
		// 				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(HaveLen(1))
		// 				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(Equal([]string{"0"}))
		// 			})
		// 		})
	})

	// 	Describe("Auctioneer Fault Tolerance", func() {
	// 		BeforeEach(func() {
	// 			suiteContext.RepRunner.Start()
	// 		})

	// 		Context("When trying to start an auction before an Auctioneer is up", func() {
	// 			BeforeEach(func() {
	// 				suiteContext.ExecutorRunner.Start()

	// 				desiredAppRequest = constructDesiredAppRequest(1)

	// 				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
	// 				Ω(err).ShouldNot(HaveOccurred())

	// 				time.Sleep(PENDING_AUCTION_KICK_THRESHOLD)
	// 				time.Sleep(WAIT_FOR_MULTIPLE_CONVERGE_INTERVAL)

	// 				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
	// 				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
	// 				Ω(running_lrps_poller()).Should(BeEmpty())
	// 				Ω(hello_world_instance_poller()).Should(BeEmpty())
	// 			})

	// 			It("Eventually brings the long-running process up", func() {
	// 				suiteContext.AuctioneerRunner.Start(AUCTION_MAX_ROUNDS)

	// 				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
	// 				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
	// 				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(HaveLen(1))
	// 				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(Equal([]string{"0"}))
	// 			})
	// 		})

	// 		Context("When an auctioneer dies before the auction completes", func() {
	// 			BeforeEach(func() {
	// 				//start the auctioneer and have it keep trying the auction for ever
	// 				//note: there is no executor running so the auction will not succeed
	// 				suiteContext.AuctioneerRunner.Start(100000000)

	// 				desiredAppRequest = constructDesiredAppRequest(1)

	// 				err := suiteContext.NatsRunner.MessageBus.Publish("diego.desire.app", desiredAppRequest.ToJSON())
	// 				Ω(err).ShouldNot(HaveOccurred())

	// 				time.Sleep(PENDING_AUCTION_KICK_THRESHOLD)
	// 				time.Sleep(WAIT_FOR_MULTIPLE_CONVERGE_INTERVAL)

	// 				suiteContext.AuctioneerRunner.Stop()

	// 				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
	// 				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
	// 				Ω(running_lrps_poller()).Should(BeEmpty())
	// 				Ω(hello_world_instance_poller()).Should(BeEmpty())
	// 			})

	// 			It("Eventually brings the long-running process up", func() {
	// 				suiteContext.ExecutorRunner.Start()
	// 				suiteContext.AuctioneerRunner.Start(AUCTION_MAX_ROUNDS)

	// 				running_lrps_poller := helpers.RunningLRPInstancesPoller(tpsAddr, processGuid)
	// 				hello_world_instance_poller := helpers.HelloWorldInstancePoller(suiteContext.RouterRunner.Addr(), "route-to-simple")
	// 				Eventually(running_lrps_poller, LONG_TIMEOUT).Should(HaveLen(1))
	// 				Eventually(hello_world_instance_poller, LONG_TIMEOUT, 1).Should(Equal([]string{"0"}))
	// 			})
	// 		})
	// 	})
})
