package cell_test

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	archive_helper "code.cloudfoundry.org/archiver/extractor/test_helper"
	"code.cloudfoundry.org/bbs/events"
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/inigo/fixtures"
	"code.cloudfoundry.org/inigo/helpers"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/rep/cmd/rep/config"
	logevents "github.com/cloudfoundry/sonde-go/events"
	"github.com/gogo/protobuf/proto"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Context("when declarative healthchecks is turned on", func() {
	var (
		processGuid         string
		archiveFiles        []archive_helper.ArchiveFile
		fileServerStaticDir string

		runtime ifrit.Process

		lock        *sync.Mutex
		eventSource events.EventSource
		events      []models.Event
	)

	BeforeEach(func() {
		processGuid = helpers.GenerateGuid()

		var fileServer ifrit.Runner
		fileServer, fileServerStaticDir = componentMaker.FileServer()

		turnOnLongRunningHealthchecks := func(cfg *config.RepConfig) {
			cfg.EnableDeclarativeHealthcheck = true
			cfg.DeclarativeHealthcheckPath = componentMaker.Artifacts.Healthcheck
			cfg.HealthCheckWorkPoolSize = 1
		}

		port, err := componentMaker.PortAllocator.ClaimPorts(1)
		Expect(err).NotTo(HaveOccurred())
		addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
		Expect(err).NotTo(HaveOccurred())
		udpConn, err := net.ListenUDP("udp", addr)
		Expect(err).NotTo(HaveOccurred())

		metronAgent := ifrit.RunFunc(func(signals <-chan os.Signal, ready chan<- struct{}) error {
			logger := logger.Session("metron-agent")
			close(ready)
			logger.Info("starting", lager.Data{"port": addr.Port})
			msgs := make(chan []byte)
			errCh := make(chan error)
			go func() {
				for {
					bs := make([]byte, 102400)
					n, _, err := udpConn.ReadFromUDP(bs)
					if err != nil {
						errCh <- err
						return
					}
					msgs <- bs[:n]
				}
			}()
		loop:
			for {
				select {
				case <-signals:
					logger.Info("signaled")
					break loop
				case err := <-errCh:
					return err
				case msg := <-msgs:
					var envelope logevents.Envelope
					err := proto.Unmarshal(msg, &envelope)
					if err != nil {
						logger.Error("error-parsing-message", err)
						continue
					}
					if envelope.GetEventType() != logevents.Envelope_LogMessage {
						continue
					}
					logger.Info("received-data", lager.Data{"message": string(envelope.GetLogMessage().GetMessage())})
				}
			}
			udpConn.Close()
			return nil
		})

		dropsondeListener := func(cfg *config.RepConfig) {
			cfg.DropsondePort = addr.Port
		}

		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"router", componentMaker.Router()},
			{"file-server", fileServer},
			{"metron-agent", metronAgent},
			{"rep", componentMaker.Rep(turnOnLongRunningHealthchecks, dropsondeListener)},
			{"auctioneer", componentMaker.Auctioneer()},
			{"route-emitter", componentMaker.RouteEmitter()},
		}))

		archiveFiles = fixtures.GoServerApp()
		archive_helper.CreateZipArchive(
			filepath.Join(fileServerStaticDir, "lrp.zip"),
			archiveFiles,
		)

		lock = &sync.Mutex{}
	})

	JustBeforeEach(func() {
		var err error
		eventSource, err = bbsClient.SubscribeToEvents(logger)
		Expect(err).NotTo(HaveOccurred())
		go func() {
			defer GinkgoRecover()

			for {
				event, err := eventSource.Next()
				if err != nil {
					return
				}
				lock.Lock()
				events = append(events, event)
				lock.Unlock()
			}
		}()
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime)
	})

	Describe("desiring", func() {
		var lrp *models.DesiredLRP

		BeforeEach(func() {
			lrp = helpers.DefaultDeclaritiveHealthcheckLRPCreateRequest(componentMaker.Addresses, processGuid, "log-guid", 1)
		})

		JustBeforeEach(func() {
			err := bbsClient.DesireLRP(logger, lrp)
			Expect(err).NotTo(HaveOccurred())
		})

		It("eventually runs", func() {
			Eventually(helpers.LRPStatePoller(logger, bbsClient, processGuid, nil)).Should(Equal(models.ActualLRPStateRunning))
			Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost)).Should(ConsistOf([]string{"0"}))
		})

		Context("the container is privileged", func() {
			BeforeEach(func() {
				lrp.Privileged = true
			})

			It("eventually runs", func() {
				Eventually(helpers.LRPStatePoller(logger, bbsClient, processGuid, nil)).Should(Equal(models.ActualLRPStateRunning))
				Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost)).Should(ConsistOf([]string{"0"}))
			})
		})

		Context("when the lrp is scaled up", func() {
			JustBeforeEach(func() {
				Eventually(helpers.LRPStatePoller(logger, bbsClient, processGuid, nil)).Should(Equal(models.ActualLRPStateRunning))
				bbsClient.UpdateDesiredLRP(logger, processGuid, &models.DesiredLRPUpdate{
					Instances: proto.Int32(2),
				})
			})

			It("eventually runs", func() {
				Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost)).Should(ConsistOf([]string{"0", "1"}))
			})
		})

		Context("when the lrp does not have a start timeout", func() {
			BeforeEach(func() {
				lrp.StartTimeoutMs = 0
			})

			It("eventually runs", func() {
				Eventually(helpers.LRPStatePoller(logger, bbsClient, processGuid, nil)).Should(Equal(models.ActualLRPStateRunning))
				Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, helpers.DefaultHost)).Should(ConsistOf([]string{"0"}))
			})
		})
	})
})
