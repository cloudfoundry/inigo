package inigo_test

import (
	"github.com/cloudfoundry-incubator/inigo/executor_runner"
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
		natsClient.Disconnect()
	})

	Context("when the stager receives a staging message", func() {
		BeforeEach(func() {
			executorRunner.Start()
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
			close(done)
		}, 5.0)
	})

	Context("when there are no executors listening when a RunOnce is registered", func() {
		It("eventually runs the RunOnce once a stager comes up", func() {
			guid := factories.GenerateGuid()
			runOnce := factories.BuildRunOnceWithRunAction(1, 1, inigolistener.CurlCommand(guid))
			bbs.DesireRunOnce(runOnce)

			executorRunner.Start()

			Eventually(inigolistener.ReportingGuids, 5.0).Should(ContainElement(guid))
		})
	})

	Context("when an executor disappears", func() {
		var secondExecutor *executor_runner.ExecutorRunner
		BeforeEach(func() {
			secondExecutor = executor_runner.New(
				executorPath,
				wardenNetwork,
				wardenAddr,
				etcdRunner.NodeURLS(),
			)

			executorRunner.Start(executor_runner.Config{MemoryMB: 3, DiskMB: 3, ConvergenceInterval: 1})
			secondExecutor.Start(executor_runner.Config{ConvergenceInterval: 1, HeartbeatInterval: 1})
		})

		It("eventually marks jobs running on that executor as failed", func() {
			guid := factories.GenerateGuid()
			runOnce := factories.BuildRunOnceWithRunAction(1024, 1024, inigolistener.CurlCommand(guid)+"; sleep 10")
			bbs.DesireRunOnce(runOnce)
			Eventually(inigolistener.ReportingGuids, 5.0).Should(ContainElement(guid))

			secondExecutor.KillWithFire()

			Eventually(func() interface{} {
				runOnces, _ := bbs.GetAllCompletedRunOnces()
				return runOnces
			}, 5.0).Should(HaveLen(1))
			runOnces, _ := bbs.GetAllCompletedRunOnces()

			completedRunOnce := runOnces[0]
			Ω(completedRunOnce.Guid).Should(Equal(runOnce.Guid))
			Ω(completedRunOnce.Failed).To(BeTrue())
		})
	})
})
