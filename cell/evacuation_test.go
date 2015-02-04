package cell_test

import (
	"syscall"

	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

var _ = Describe("Evacuation", func() {
	var (
		auctioneer ifrit.Process
		executor   ifrit.Process
		rep        ifrit.Process
		converger  ifrit.Process

		processGuid string
		appId       string

		repRunner ifrit.Runner
	)

	BeforeEach(func() {
		auctioneer = ginkgomon.Invoke(componentMaker.Auctioneer())
		executor = ginkgomon.Invoke(componentMaker.Executor())
		repRunner = componentMaker.Rep()
		rep = ginkgomon.Invoke(repRunner)
		converger = ginkgomon.Invoke(componentMaker.Converger(
			"-convergeRepeatInterval", "1s",
		))
		processGuid = factories.GenerateGuid()
		appId = factories.GenerateGuid()
	})

	AfterEach(func() {
		helpers.StopProcesses(auctioneer, executor, rep, converger)
	})

	Context("when desiring new work", func() {
		BeforeEach(func() {
			rep.Signal(syscall.SIGUSR1)

			gRunner, ok := repRunner.(*ginkgomon.Runner)
			Ω(ok).Should(BeTrue())
			Eventually(gRunner.Buffer).Should(gbytes.Say("run-signaled.*user defined signal 1"))

			lrp := receptor.DesiredLRPCreateRequest{
				Domain:      INIGO_DOMAIN,
				ProcessGuid: processGuid,
				Instances:   1,
				Stack:       componentMaker.Stack,
				Ports:       []uint16{},

				Action: &models.RunAction{
					Path: "true",
				},

				Monitor: &models.RunAction{
					Path: "true",
				},
			}

			err := receptorClient.CreateDesiredLRP(lrp)
			Ω(err).ShouldNot(HaveOccurred())
		})

		It("does not schedule new work on evacuating reps", func() {
			Consistently(func() []receptor.ActualLRPResponse {
				actualLRPs := helpers.ActiveActualLRPs(receptorClient, processGuid)
				return actualLRPs
			}).Should(HaveLen(0))
		})
	})
})
