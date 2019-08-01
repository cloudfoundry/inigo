package cell_test

import (
	"os"
	"path/filepath"
	"runtime"

	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/inigo/fixtures"
	"code.cloudfoundry.org/inigo/helpers"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	archive_helper "code.cloudfoundry.org/archiver/extractor/test_helper"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Convergence to desired state", func() {
	var (
		ifritRuntime ifrit.Process
		auctioneer   ifrit.Process
		rep          ifrit.Process

		appId       string
		processGuid string

		runningLRPsPoller         func() []models.ActualLRP
		helloWorldInstancePoller  func() []string
		runningLRPsPresencePoller func() []models.ActualLRP_Presence
	)

	BeforeEach(func() {
		if runtime.GOOS == "windows" {
			Skip(" not yet working on windows")
		}
		fileServer, fileServerStaticDir := componentMaker.FileServer()

		ifritRuntime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"file-server", fileServer},
			{"route-emitter", componentMaker.RouteEmitter()},
			{"router", componentMaker.Router()},
		}))

		archive_helper.CreateZipArchive(
			filepath.Join(fileServerStaticDir, "lrp.zip"),
			fixtures.GoServerApp(),
		)

		appId = helpers.GenerateGuid()

		processGuid = helpers.GenerateGuid()

		runningLRPsPoller = func() []models.ActualLRP {
			return helpers.ActiveActualLRPs(lgr, bbsClient, processGuid)
		}

		runningLRPsPresencePoller = func() []models.ActualLRP_Presence {
			lrps := helpers.ActiveActualLRPs(lgr, bbsClient, processGuid)
			presences := []models.ActualLRP_Presence{}
			for _, lrp := range lrps {
				presences = append(presences, lrp.Presence)
			}
			return presences
		}

		helloWorldInstancePoller = helpers.HelloWorldInstancePoller(componentMaker.Addresses().Router, helpers.DefaultHost)
	})

	AfterEach(func() {
		By("Stopping all the processes")
		helpers.StopProcesses(auctioneer, rep, ifritRuntime)
	})

	Describe("Executor fault tolerance", func() {
		BeforeEach(func() {
			auctioneer = ginkgomon.Invoke(componentMaker.Auctioneer())
		})

		Context("when an rep and converger are running", func() {
			var initialInstanceGuids []string

			BeforeEach(func() {
				rep = ginkgomon.Invoke(componentMaker.Rep())

				By("restarting the bbs with smaller convergeRepeatInterval")
				ginkgomon.Interrupt(bbsProcess)
				bbsProcess = ginkgomon.Invoke(componentMaker.BBS(
					overrideConvergenceRepeatInterval,
				))

				By("creating and ActualLRP")
				err := bbsClient.DesireLRP(lgr, helpers.DefaultLRPCreateRequest(componentMaker.Addresses(), processGuid, appId, 2))
				Expect(err).NotTo(HaveOccurred())
				Eventually(runningLRPsPoller).Should(HaveLen(2))
				Eventually(helloWorldInstancePoller).Should(Equal([]string{"0", "1"}))

				By("collecting the ActualLRP instance guids")
				initialActuals := runningLRPsPoller()
				initialInstanceGuids = []string{initialActuals[0].InstanceGuid, initialActuals[1].InstanceGuid}

				By("restarting the bbs with smaller convergeRepeatInterval and generating SuspectActualLRPs")
				ginkgomon.Interrupt(bbsProcess)
				bbsProcess = ginkgomon.Invoke(componentMaker.BBS(
					overrideConvergenceRepeatInterval,
				))
			})

			It("marks the LRPs as Suspect until the rep comes back, and then marks the LRPs as Ordinary", func() {
				By("killing the lone rep")
				ginkgomon.Interrupt(rep)

				By("Asserting that the LRPs are marked as Suspect")
				Eventually(runningLRPsPoller).Should(HaveLen(2))
				Eventually(runningLRPsPresencePoller).Should(ConsistOf(models.ActualLRP_Suspect, models.ActualLRP_Suspect))

				By("bringing back the original rep")
				rep = ginkgomon.Invoke(componentMaker.Rep())

				Eventually(runningLRPsPoller).Should(HaveLen(2))
				Eventually(helloWorldInstancePoller).Should(Equal([]string{"0", "1"}))

				By("Asserting that the LRPs marked as Ordinary")
				currentActuals := runningLRPsPoller()
				instanceGuids := []string{currentActuals[0].InstanceGuid, currentActuals[1].InstanceGuid}
				Expect(instanceGuids).NotTo(ContainElement(initialInstanceGuids[0]))
				Expect(instanceGuids).NotTo(ContainElement(initialInstanceGuids[1]))
				Eventually(runningLRPsPresencePoller).Should(ConsistOf(models.ActualLRP_Ordinary, models.ActualLRP_Ordinary))
			})

			Context("and a second rep is running", func() {
				var firstActualLRPs []models.ActualLRP
				var rep2 ifrit.Process

				BeforeEach(func() {
					firstActualLRPs = runningLRPsPoller()
					rep2 = ginkgomon.Invoke(componentMaker.RepN(1))
				})

				AfterEach(func() {
					helpers.StopProcesses(rep2)
				})

				It("marks the LRPs as Suspect until they get started on the other rep", func() {
					By("killing the original rep")
					ginkgomon.Kill(rep)

					By("Asserting that the LRPs are marked as Suspect")
					Eventually(runningLRPsPoller).Should(HaveLen(2))
					Eventually(runningLRPsPresencePoller).Should(ConsistOf(models.ActualLRP_Suspect, models.ActualLRP_Suspect))

					By("Asserting that the LRPs are started on the second rep")
					Eventually(func() bool {
						secondActualLRPs := runningLRPsPoller()
						if len(secondActualLRPs) != 2 {
							return false
						}
						return secondActualLRPs[0].CellId != firstActualLRPs[0].CellId &&
							secondActualLRPs[1].CellId != firstActualLRPs[1].CellId
					}).Should(BeTrue())
				})
			})
		})

		Context("when a converger is running without a rep", func() {
			BeforeEach(func() {
				By("restarting the bbs with smaller convergeRepeatInterval")
				ginkgomon.Interrupt(bbsProcess)
				bbsProcess = ginkgomon.Invoke(componentMaker.BBS(
					overrideConvergenceRepeatInterval,
				))
			})

			Context("and an LRP is desired", func() {
				BeforeEach(func() {
					err := bbsClient.DesireLRP(lgr, helpers.DefaultLRPCreateRequest(componentMaker.Addresses(), processGuid, appId, 1))
					Expect(err).NotTo(HaveOccurred())

					Consistently(runningLRPsPoller).Should(BeEmpty())
					Consistently(helloWorldInstancePoller).Should(BeEmpty())
				})

				Context("and then a rep come up", func() {
					BeforeEach(func() {
						rep = ginkgomon.Invoke(componentMaker.Rep())
					})

					It("eventually brings the LRP up", func() {
						Eventually(runningLRPsPoller).Should(HaveLen(1))
						Eventually(helloWorldInstancePoller).Should(Equal([]string{"0"}))
					})
				})
			})
		})
	})

	Describe("Auctioneer Fault Tolerance", func() {
		BeforeEach(func() {
			By("restarting the bbs with smaller convergeRepeatInterval")
			ginkgomon.Interrupt(bbsProcess)
			bbsProcess = ginkgomon.Invoke(componentMaker.BBS(
				overrideConvergenceRepeatInterval,
			))
		})

		Context("when a rep is running with no auctioneer", func() {
			BeforeEach(func() {
				rep = ginkgomon.Invoke(componentMaker.Rep())
			})

			Context("and an LRP is desired", func() {
				BeforeEach(func() {
					err := bbsClient.DesireLRP(lgr, helpers.DefaultLRPCreateRequest(componentMaker.Addresses(), processGuid, appId, 1))
					Expect(err).NotTo(HaveOccurred())

					Consistently(runningLRPsPoller).Should(BeEmpty())
					Consistently(helloWorldInstancePoller).Should(BeEmpty())
				})

				Context("and then an auctioneer comes up", func() {
					BeforeEach(func() {
						auctioneer = ginkgomon.Invoke(componentMaker.Auctioneer())
					})

					It("eventually brings it up", func() {
						Eventually(runningLRPsPoller).Should(HaveLen(1))
						Eventually(helloWorldInstancePoller).Should(Equal([]string{"0"}))
					})
				})
			})
		})

		Context("when an auctioneer is running with no rep", func() {
			BeforeEach(func() {
				auctioneer = ginkgomon.Invoke(componentMaker.Auctioneer())
			})

			Context("and an LRP is desired", func() {
				BeforeEach(func() {
					err := bbsClient.DesireLRP(lgr, helpers.DefaultLRPCreateRequest(componentMaker.Addresses(), processGuid, appId, 1))
					Expect(err).NotTo(HaveOccurred())

					Consistently(runningLRPsPoller).Should(BeEmpty())
					Consistently(helloWorldInstancePoller).Should(BeEmpty())
				})

				Context("and the rep come up", func() {
					BeforeEach(func() {
						rep = ginkgomon.Invoke(componentMaker.Rep())
					})

					It("eventually brings it up", func() {
						Eventually(runningLRPsPoller).Should(HaveLen(1))
						Eventually(helloWorldInstancePoller).Should(Equal([]string{"0"}))
					})
				})
			})
		})
	})
})
