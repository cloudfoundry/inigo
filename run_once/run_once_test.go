package run_once_test

import (
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry/yagnats"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"log"
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
		if err != nil {
			log.Fatalf("Error connecting: %s\n", err)
		}
	})

	AfterEach(func() {
		executorRunner.Stop()
		stagerRunner.Stop()
		natsClient.Disconnect()
	})

	Context("when the stager receives a staging message", func() {
		Context("when the executors are listening", func() {
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

				listResponse, err := wardenClient.List()
				Expect(err).ToNot(HaveOccurred())

				Expect(listResponse.GetHandles()).To(ContainElement(runOnce.ContainerHandle))
				close(done)
			}, 5.0)
		})

		Context("when the executors are not listening", func() {
			It("still runs the staging process when the executors start listening again", func(done Done) {
				received := make(chan bool)
				natsClient.Subscribe("stager-test", func(message *yagnats.Message) {
					received <- true
				})
				err := natsClient.PublishWithReplyTo("diego.staging.start", "stager-test", []byte(`{"app_id": "some-app-guid", "task_id": "some-task-id"}`))
				Ω(err).ShouldNot(HaveOccurred())

				<-received

				executorRunner.Start()

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
	})
})
