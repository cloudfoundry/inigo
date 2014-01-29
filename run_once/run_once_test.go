package run_once_test

import (
	"fmt"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	stgr "github.com/cloudfoundry-incubator/stager/stager"
	steno "github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/yagnats"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"log"
	"os"
)

var _ = Describe("RunOnce", func() {
	// var bbs *Bbs.BBS
	var stager stgr.Stager
	var natsClient *yagnats.Client
	var bbs *Bbs.BBS

	BeforeEach(func() {
		executorRunner.Start()
		natsRunner.Start()

		bbs = Bbs.New(etcdRunner.Adapter())
		natsClient = yagnats.NewClient()
		err := natsClient.Connect(&yagnats.ConnectionInfo{"127.0.0.1:4222", "nats", "nats"})
		if err != nil {
			log.Fatalf("Error connecting: %s\n", err)
		}

		steno.Init(&steno.Config{
			Sinks: []steno.Sink{
				steno.NewIOSink(os.Stdout),
			},
		})

		stager = stgr.NewStager(bbs)
		stgr.Listen(natsClient, stager, steno.NewLogger("Stager"))
	})

	AfterEach(func() {
		executorRunner.Stop()
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

			fmt.Printf("runonce: %#v\n", runOnce)
			Expect(runOnce.Guid).To(Equal("some-app-guid-some-task-id"))
			Expect(runOnce.ContainerHandle).ToNot(BeEmpty())

			listResponse, err := wardenClient.List()
			Expect(err).ToNot(HaveOccurred())

			Expect(listResponse.GetHandles()).To(ContainElement(runOnce.ContainerHandle))
		}, 5.0)
	})
})
