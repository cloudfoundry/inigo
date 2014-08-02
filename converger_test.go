package inigo_test

import (
	"path/filepath"
	"syscall"
	"time"

	"github.com/cloudfoundry-incubator/garden/warden"
	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/world"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	"github.com/cloudfoundry/yagnats"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"

	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	tpsapi "github.com/cloudfoundry-incubator/tps/api"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

var _ = Describe("Convergence to desired state", func() {
	var (
		plumbing ifrit.Process
		runtime  ifrit.Process

		auctioneer ifrit.Process
		executor   ifrit.Process
		rep        ifrit.Process
		converger  ifrit.Process

		natsClient   yagnats.NATSClient
		wardenClient warden.Client

		fileServerStaticDir string

		appId       string
		processGuid string

		runningLRPsPoller        func() []tpsapi.LRPInstance
		helloWorldInstancePoller func() []string
	)

	constructDesiredAppRequest := func(numInstances int) models.DesireAppRequestFromCC {
		return models.DesireAppRequestFromCC{
			ProcessGuid:  processGuid,
			DropletUri:   inigo_server.DownloadUrl("simple-echo-droplet.zip"),
			Stack:        componentMaker.Stack,
			Environment:  []models.EnvironmentVariable{{Name: "VCAP_APPLICATION", Value: "{}"}},
			NumInstances: numInstances,
			Routes:       []string{"route-to-simple"},
			StartCommand: "./run",
			LogGuid:      appId,
		}
	}

	BeforeEach(func() {
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

		runtime = grouper.EnvokeGroup(grouper.RunGroup{
			"cc":             componentMaker.FakeCC(),
			"tps":            componentMaker.TPS(),
			"nsync-listener": componentMaker.NsyncListener(),
			"file-server":    fileServer,
			"route-emitter":  componentMaker.RouteEmitter(),
			"router":         componentMaker.Router(),
			"loggregator":    componentMaker.Loggregator(),
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

		appId = factories.GenerateGuid()

		processGuid = factories.GenerateGuid()

		runningLRPsPoller = helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid)
		helloWorldInstancePoller = helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-to-simple")
	})

	AfterEach(func() {
		if auctioneer != nil {
			auctioneer.Signal(syscall.SIGKILL)
			Eventually(auctioneer.Wait(), 5*time.Second).Should(Receive())
		}

		if executor != nil {
			executor.Signal(syscall.SIGKILL)
			Eventually(executor.Wait(), 5*time.Second).Should(Receive())
		}

		if rep != nil {
			rep.Signal(syscall.SIGKILL)
			Eventually(rep.Wait(), 5*time.Second).Should(Receive())
		}

		if converger != nil {
			converger.Signal(syscall.SIGKILL)
			Eventually(converger.Wait(), 5*time.Second).Should(Receive())
		}

		inigo_server.Stop(wardenClient)

		runtime.Signal(syscall.SIGKILL)
		Eventually(runtime.Wait(), 5*time.Second).Should(Receive())

		plumbing.Signal(syscall.SIGKILL)
		Eventually(plumbing.Wait(), 5*time.Second).Should(Receive())
	})

	Describe("Executor fault tolerance", func() {
		BeforeEach(func() {
			auctioneer = ifrit.Envoke(componentMaker.Auctioneer())
		})

		Context("when an executor, rep, and converger are running", func() {
			BeforeEach(func() {
				executor = ifrit.Envoke(componentMaker.Executor())
				rep = ifrit.Envoke(componentMaker.Rep())
				converger = ifrit.Envoke(componentMaker.Converger(
					"-convergeRepeatInterval", "1s",
					"-kickPendingLRPStartAuctionDuration", "1s",
				))
			})

			Context("and an LRP starts running", func() {
				BeforeEach(func() {
					desiredAppRequest := models.DesireAppRequestFromCC{
						ProcessGuid:  processGuid,
						NumInstances: 2,
						DropletUri:   inigo_server.DownloadUrl("simple-echo-droplet.zip"),
						Stack:        componentMaker.Stack,
						Environment:  []models.EnvironmentVariable{{Name: "VCAP_APPLICATION", Value: "{}"}},
						Routes:       []string{"route-to-simple"},
						StartCommand: "./run",
						LogGuid:      appId,
					}

					err := natsClient.Publish("diego.desire.app", desiredAppRequest.ToJSON())
					Ω(err).ShouldNot(HaveOccurred())

					Eventually(runningLRPsPoller).Should(HaveLen(1))
					Eventually(helloWorldInstancePoller, DEFAULT_EVENTUALLY_TIMEOUT, 1).Should(Equal([]string{"0", "1"}))
				})

				Context("and the LRP goes away because its executor dies", func() {
					BeforeEach(func() {
						executor.Signal(syscall.SIGKILL)

						Eventually(runningLRPsPoller).Should(BeEmpty())
						Eventually(helloWorldInstancePoller, DEFAULT_EVENTUALLY_TIMEOUT, 1).Should(BeEmpty())
					})

					Context("once the executor comes back", func() {
						BeforeEach(func() {
							executor = ifrit.Envoke(componentMaker.Executor())
						})

						It("eventually brings the long-running process up", func() {
							Eventually(runningLRPsPoller).Should(HaveLen(1))
							Eventually(helloWorldInstancePoller, DEFAULT_EVENTUALLY_TIMEOUT, 1).Should(Equal([]string{"0", "1"}))
						})
					})
				})

				Context("and the rep and converger go away", func() {
					BeforeEach(func() {
						converger.Signal(syscall.SIGKILL)
						rep.Signal(syscall.SIGKILL)
					})

					Context("and the LRP is scaled down (but the event is not handled)", func() {
						BeforeEach(func() {
							desiredAppScaleDownRequest := constructDesiredAppRequest(1)

							err := natsClient.Publish("diego.desire.app", desiredAppScaleDownRequest.ToJSON())
							Ω(err).ShouldNot(HaveOccurred())

							Consistently(runningLRPsPoller).Should(HaveLen(2))
						})

						Context("and rep and converger come back", func() {
							BeforeEach(func() {
								rep = ifrit.Envoke(componentMaker.Rep())
								converger = ifrit.Envoke(componentMaker.Converger(
									"-convergeRepeatInterval", "1s",
									"-kickPendingLRPStartAuctionDuration", "1s",
								))
							})

							It("eventually scales the LRP down", func() {
								Eventually(runningLRPsPoller).Should(HaveLen(1))
								Eventually(helloWorldInstancePoller, DEFAULT_EVENTUALLY_TIMEOUT, 1).Should(Equal([]string{"0"}))
							})
						})
					})
				})
			})
		})

		Context("when a converger is running without a rep and executor", func() {
			BeforeEach(func() {
				converger = ifrit.Envoke(componentMaker.Converger(
					"-convergeRepeatInterval", "1s",
					"-kickPendingLRPStartAuctionDuration", "1s",
				))
			})

			Context("and an LRP is desired", func() {
				BeforeEach(func() {
					desiredAppRequest := constructDesiredAppRequest(1)

					err := natsClient.Publish("diego.desire.app", desiredAppRequest.ToJSON())
					Ω(err).ShouldNot(HaveOccurred())

					Consistently(runningLRPsPoller).Should(BeEmpty())
					Consistently(helloWorldInstancePoller, DEFAULT_CONSISTENTLY_DURATION, 1).Should(BeEmpty())
				})

				Context("and then a rep and executor come up", func() {
					BeforeEach(func() {
						executor = ifrit.Envoke(componentMaker.Executor())
						rep = ifrit.Envoke(componentMaker.Rep())
					})

					It("eventually brings the LRP up", func() {
						Eventually(runningLRPsPoller).Should(HaveLen(1))
						Eventually(helloWorldInstancePoller, DEFAULT_EVENTUALLY_TIMEOUT, 1).Should(Equal([]string{"0"}))
					})
				})
			})
		})
	})

	Describe("Auctioneer Fault Tolerance", func() {
		BeforeEach(func() {
			converger = ifrit.Envoke(componentMaker.Converger(
				"-convergeRepeatInterval", "1s",
				"-kickPendingLRPStartAuctionDuration", "1s",
			))
		})

		Context("when an executor and rep are running with no auctioneer", func() {
			BeforeEach(func() {
				executor = ifrit.Envoke(componentMaker.Executor())
				rep = ifrit.Envoke(componentMaker.Rep())
			})

			Context("and an LRP is desired", func() {
				BeforeEach(func() {
					desiredAppRequest := constructDesiredAppRequest(1)

					err := natsClient.Publish("diego.desire.app", desiredAppRequest.ToJSON())
					Ω(err).ShouldNot(HaveOccurred())

					Consistently(runningLRPsPoller).Should(BeEmpty())
					Consistently(helloWorldInstancePoller).Should(BeEmpty())
				})

				Context("and then an auctioneer comes up", func() {
					BeforeEach(func() {
						auctioneer = ifrit.Envoke(componentMaker.Auctioneer())
					})

					It("eventually brings it up", func() {
						Eventually(runningLRPsPoller).Should(HaveLen(1))
						Eventually(helloWorldInstancePoller, DEFAULT_EVENTUALLY_TIMEOUT, 1).Should(Equal([]string{"0"}))
					})
				})
			})
		})

		Context("when an auctioneer is running with no executor or rep", func() {
			BeforeEach(func() {
				auctioneer = ifrit.Envoke(componentMaker.Auctioneer(
					// have it keep trying the auction for ever
					"-maxRounds", "9999999999",
				))
			})

			Context("and an LRP is desired", func() {
				BeforeEach(func() {
					desiredAppRequest := constructDesiredAppRequest(1)

					err := natsClient.Publish("diego.desire.app", desiredAppRequest.ToJSON())
					Ω(err).ShouldNot(HaveOccurred())

					Consistently(runningLRPsPoller).Should(BeEmpty())
					Consistently(helloWorldInstancePoller).Should(BeEmpty())
				})

				Context("and the executor and rep come up", func() {
					BeforeEach(func() {
						executor = ifrit.Envoke(componentMaker.Executor())
						rep = ifrit.Envoke(componentMaker.Rep())
					})

					It("eventually brings it up", func() {
						Eventually(runningLRPsPoller).Should(HaveLen(1))
						Eventually(helloWorldInstancePoller, DEFAULT_EVENTUALLY_TIMEOUT, 1).Should(Equal([]string{"0"}))
					})
				})
			})
		})
	})
})
