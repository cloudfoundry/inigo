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
		executorRunner.Start()
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
	})

	Context("when the stager receives a staging message", func() {
		It("eventually is running on an executor", func(done Done) {
			natsClient.Subscribe("stager-test", func(message *yagnats.Message) {
				// seeing the response at all is enough for now;
				// this will make the test complete successfully

				// but it has to be done  up here so that we subscribe before we publish
				close(done)
			})

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
		}, 5.0)
	})
})
