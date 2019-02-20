package cell_test

import (
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	archive_helper "code.cloudfoundry.org/archiver/extractor/test_helper"
	"code.cloudfoundry.org/bbs/events"
	"code.cloudfoundry.org/bbs/models"
	logging "code.cloudfoundry.org/diego-logging-client"
	"code.cloudfoundry.org/diego-logging-client/testhelpers"
	"code.cloudfoundry.org/durationjson"
	"code.cloudfoundry.org/inigo/fixtures"
	"code.cloudfoundry.org/inigo/helpers"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/lager/lagertest"
	"code.cloudfoundry.org/rep/cmd/rep/config"
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

		lock              *sync.Mutex
		eventSource       events.EventSource
		events            []models.Event
		testIngressServer *testhelpers.TestIngressServer
	)

	BeforeEach(func() {
		processGuid = helpers.GenerateGuid()

		var fileServer ifrit.Runner
		fileServer, fileServerStaticDir = componentMaker.FileServer()

		turnOnLongRunningHealthchecks := func(cfg *config.RepConfig) {
			cfg.EnableDeclarativeHealthcheck = true
			cfg.DeclarativeHealthcheckPath = componentMaker.Artifacts().Healthcheck
			cfg.HealthCheckWorkPoolSize = 1
		}

		fixturesPath := path.Join(os.Getenv("GOPATH"), "src/code.cloudfoundry.org/inigo/fixtures/certs")
		metronCAFile := path.Join(fixturesPath, "metron", "CA.crt")
		metronClientCertFile := path.Join(fixturesPath, "metron", "client.crt")
		metronClientKeyFile := path.Join(fixturesPath, "metron", "client.key")
		metronServerCertFile := path.Join(fixturesPath, "metron", "metron.crt")
		metronServerKeyFile := path.Join(fixturesPath, "metron", "metron.key")
		var err error
		testIngressServer, err = testhelpers.NewTestIngressServer(metronServerCertFile, metronServerKeyFile, metronCAFile)
		Expect(err).NotTo(HaveOccurred())

		Expect(testIngressServer.Start()).To(Succeed())

		metricsPort, err := testIngressServer.Port()
		Expect(err).NotTo(HaveOccurred())

		loggregatorConfig := func(cfg *config.RepConfig) {
			cfg.LoggregatorConfig = logging.Config{
				BatchFlushInterval: 10 * time.Millisecond,
				BatchMaxSize:       1,
				UseV2API:           true,
				APIPort:            metricsPort,
				CACertPath:         metronCAFile,
				KeyPath:            metronClientKeyFile,
				CertPath:           metronClientCertFile,
			}
			cfg.ContainerMetricsReportInterval = durationjson.Duration(5 * time.Second)
		}

		logger := lagertest.NewTestLogger("metron-agent")
		metronAgent := ifrit.RunFunc(func(signals <-chan os.Signal, ready chan<- struct{}) error {
			close(ready)
			testMetricsChan, signalMetricsChan := testhelpers.TestMetricChan(testIngressServer.Receivers())
			defer close(signalMetricsChan)
			for {
				select {
				case envelope := <-testMetricsChan:
					if log := envelope.GetLog(); log != nil {
						logger.Info("received-data", lager.Data{"message": string(log.GetPayload())})
					}
				case <-signals:
					return nil
				}
			}
		})

		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"router", componentMaker.Router()},
			{"file-server", fileServer},
			{"metron-agent", metronAgent},
			{"rep", componentMaker.Rep(turnOnLongRunningHealthchecks, loggregatorConfig)},
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
		testIngressServer.Stop()
		helpers.StopProcesses(runtime)
	})

	Describe("desiring", func() {
		var lrp *models.DesiredLRP

		BeforeEach(func() {
			lrp = helpers.DefaultDeclaritiveHealthcheckLRPCreateRequest(componentMaker.Addresses(), processGuid, "log-guid", 1)
		})

		JustBeforeEach(func() {
			err := bbsClient.DesireLRP(logger, lrp)
			Expect(err).NotTo(HaveOccurred())
		})

		It("eventually runs", func() {
			Eventually(helpers.LRPStatePoller(logger, bbsClient, processGuid, nil)).Should(Equal(models.ActualLRPStateRunning))
			Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses().Router, helpers.DefaultHost)).Should(ConsistOf([]string{"0"}))
		})

		Context("the container is privileged", func() {
			BeforeEach(func() {
				lrp.Privileged = true
			})

			It("eventually runs", func() {
				Eventually(helpers.LRPStatePoller(logger, bbsClient, processGuid, nil)).Should(Equal(models.ActualLRPStateRunning))
				Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses().Router, helpers.DefaultHost)).Should(ConsistOf([]string{"0"}))
			})
		})

		Context("when the lrp is scaled up", func() {
			JustBeforeEach(func() {
				Eventually(helpers.LRPStatePoller(logger, bbsClient, processGuid, nil)).Should(Equal(models.ActualLRPStateRunning))
				dlu := &models.DesiredLRPUpdate{}
				dlu.SetInstances(2)
				bbsClient.UpdateDesiredLRP(logger, processGuid, dlu)
			})

			It("eventually runs", func() {
				Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses().Router, helpers.DefaultHost)).Should(ConsistOf([]string{"0", "1"}))
			})
		})

		Context("when the lrp does not have a start timeout", func() {
			BeforeEach(func() {
				lrp.StartTimeoutMs = 0
			})

			It("eventually runs", func() {
				Eventually(helpers.LRPStatePoller(logger, bbsClient, processGuid, nil)).Should(Equal(models.ActualLRPStateRunning))
				Eventually(helpers.HelloWorldInstancePoller(componentMaker.Addresses().Router, helpers.DefaultHost)).Should(ConsistOf([]string{"0"}))
			})
		})
	})
})
