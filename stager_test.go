package inigo_test

import (
	"github.com/cloudfoundry-incubator/inigo/stager_runner"
	"github.com/cloudfoundry/yagnats"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Stager", func() {
	var otherStagerRunner *stager_runner.StagerRunner

	Context("with two stagers running", func() {
		BeforeEach(func() {
			executorRunner.Start()
			stagerRunner.Start()
			otherStagerRunner = stager_runner.New(
				stagerPath,
				etcdRunner.NodeURLS(),
			)
			otherStagerRunner.Start()
		})

		AfterEach(func() {
			otherStagerRunner.Stop()
		})

		It("only one returns a staging completed response", func() {
			messages := 0
			natsRunner.MessageBus.Subscribe("two-stagers-test", func(*yagnats.Message) {
				messages++
			})

			natsRunner.MessageBus.PublishWithReplyTo("diego.staging.start", "two-stagers-test", []byte(`{"app_id": "some-app-guid", "task_id": "some-task-id"}`))

			Eventually(func() int {
				return messages
			}, 2.0).Should(Equal(2))

			Consistently(func() int {
				return messages
			}, 2.0).Should(Equal(2))
		})
	})
})
