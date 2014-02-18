package inigo_test

import (
	"fmt"
	"strings"

	"github.com/cloudfoundry-incubator/inigo/stager_runner"
	"github.com/cloudfoundry/yagnats"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Stager", func() {
	var otherStagerRunner *stager_runner.StagerRunner

	Context("with one stager", func() {
		BeforeEach(func() {
			executorRunner.Start()
			stagerRunner.Start()
		})

		It("returns error if no compiler for stack defined", func() {
			errorMessages := 0
			natsRunner.MessageBus.Subscribe("compiler-stagers-test", func(message *yagnats.Message) {
				if strings.Contains(string(message.Payload), "error") {
					errorMessages++
				}
			})

			natsRunner.MessageBus.PublishWithReplyTo(
				"diego.staging.start",
				"compiler-stagers-test",
				[]byte(`{"app_id": "some-app-guid", "task_id": "some-task-id", "stack": "no-compiler"}`))

			Eventually(func() int {
				return errorMessages
			}, 2.0).Should(Equal(1))

			Consistently(func() int {
				return errorMessages
			}, 2.0).Should(Equal(1))
		})
	})

	Context("with two stagers running", func() {
		BeforeEach(func() {
			executorRunner.Start()
			stagerRunner.Start()
			otherStagerRunner = stager_runner.New(
				stagerPath,
				etcdRunner.NodeURLS(),
				[]string{fmt.Sprintf("127.0.0.1:%d", natsPort)},
			)
			otherStagerRunner.Start()
		})

		AfterEach(func() {
			otherStagerRunner.Stop()
		})

		It("only one returns a staging completed response", func() {
			successMessages := 0
			natsRunner.MessageBus.Subscribe("two-stagers-test", func(message *yagnats.Message) {
				if !strings.Contains(string(message.Payload), "error") {
					successMessages++
				}
			})

			natsRunner.MessageBus.PublishWithReplyTo(
				"diego.staging.start",
				"two-stagers-test",
				[]byte(`{"app_id": "some-app-guid", "task_id": "some-task-id", "stack": "default"}`))

			Eventually(func() int {
				return successMessages
			}, 2.0).Should(Equal(1))

			Consistently(func() int {
				return successMessages
			}, 2.0).Should(Equal(1))
		})
	})
})
