package inigo_test

import (
	"github.com/cloudfoundry-incubator/inigo/inigolistener"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	"github.com/cloudfoundry/yagnats"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("RunOnce", func() {
	var natsClient *yagnats.Client
	var bbs *Bbs.BBS

	BeforeEach(func() {
		natsRunner.Start()
		stagerRunner.Start()

		bbs = Bbs.New(etcdRunner.Adapter())

		natsClient = yagnats.NewClient()

		err := natsClient.Connect(&yagnats.ConnectionInfo{"127.0.0.1:4222", "nats", "nats"})
		Ω(err).ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		executorRunner.Stop()
		stagerRunner.Stop()
		natsClient.Disconnect()
	})

	Context("when the stager receives a staging message", func() {
		BeforeEach(func() {
			executorRunner.Start(1024, 1024)
		})

		It("eventually is running on an executor", func(done Done) {
			err := natsClient.PublishWithReplyTo("diego.staging.start", "stager-test", []byte(`{"app_id": "some-app-guid", "task_id": "some-task-id"}`))
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(func() []models.RunOnce {
				runOnces, _ := bbs.GetAllStartingRunOnces()
				return runOnces
			}, 5).Should(HaveLen(1))

			runOnces, _ := bbs.GetAllStartingRunOnces()
			runOnce := runOnces[0]

			Expect(runOnce.Guid).To(Equal("some-app-guid-some-task-id"))
			Expect(runOnce.ContainerHandle).ToNot(BeEmpty())

			listResponse, err := wardenClient.List()
			Expect(err).ToNot(HaveOccurred())

			Expect(listResponse.GetHandles()).To(ContainElement(runOnce.ContainerHandle))
			close(done)
		}, 5.0)
	})

	Context("when there are no executors listening when a RunOnce is registered", func() {
		It("eventually runs the RunOnce once a stager comes up", func() {
			guid := factories.GenerateGuid()
			runOnce := factories.BuildRunOnceWithRunAction(1, 1, inigolistener.CurlCommand(guid))
			bbs.DesireRunOnce(runOnce)

			executorRunner.Start(1024, 1024)

			Eventually(inigolistener.ReportingGuids, 5.0).Should(ContainElement(guid))
		})
	})
})
