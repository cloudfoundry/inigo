package stager_test

import (
	"encoding/json"
	. "github.com/cloudfoundry-incubator/stager/stager"
	"github.com/cloudfoundry-incubator/stager/stager/fakestager"
	steno "github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/yagnats/fakeyagnats"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("StagingListener", func() {
	Context("when it receives a staging request", func() {
		var fakenats *fakeyagnats.FakeYagnats
		var fauxstager *fakestager.FakeStager
		var testingSink *steno.TestingSink
		var logger *steno.Logger

		BeforeEach(func() {
			testingSink = steno.NewTestingSink()
			stenoConfig := &steno.Config{
				Sinks: []steno.Sink{testingSink},
			}
			steno.Init(stenoConfig)

			fakenats = fakeyagnats.New()
			fauxstager = &fakestager.FakeStager{}
			logger = steno.NewLogger("fakelogger")
		})

		JustBeforeEach(func() {
			Listen(fakenats, fauxstager, logger)
		})

		It("kicks off staging", func() {
			stagingRequest := StagingRequest{
				AppId:  "myapp",
				TaskId: "mytask",
			}
			msg, _ := json.Marshal(stagingRequest)

			fakenats.Publish("diego.staging.start", msg)
			Ω(fauxstager.TimesStageInvoked).To(Equal(1))
			Ω(fauxstager.StagingRequests[0]).To(Equal(stagingRequest))
		})

		Context("when unmarshaling fails", func() {
			It("logs the failure", func() {
				Ω(testingSink.Records).To(HaveLen(0))

				fakenats.PublishWithReplyTo("diego.staging.start", "reply string", []byte("fdsaljkfdsljkfedsews:/sdfa:''''"))

				Ω(testingSink.Records).ToNot(HaveLen(0))
			})

			It("sends a staging failure response", func() {
				fakenats.PublishWithReplyTo("diego.staging.start", "reply string", []byte("fdsaljkfdsljkfedsews:/sdfa:''''"))
				replyTo := fakenats.PublishedMessages["diego.staging.start"][0].ReplyTo

				Ω(fakenats.PublishedMessages[replyTo]).To(HaveLen(1))
				response := fakenats.PublishedMessages[replyTo][0]
				stagingResponse := StagingResponse{}
				json.Unmarshal(response.Payload, &stagingResponse)
				Ω(stagingResponse.Error).To(Equal("Staging message contained invalid JSON"))
			})
		})

		publishStagingMessage := func() {
			stagingRequest := StagingRequest{
				AppId:  "myapp",
				TaskId: "mytask",
			}
			msg, _ := json.Marshal(stagingRequest)

			fakenats.PublishWithReplyTo("diego.staging.start", "reply to", msg)
		}

		Context("when staging finishes successfully", func() {
			It("responds with a success message", func() {
				publishStagingMessage()

				replyTo := fakenats.PublishedMessages["diego.staging.start"][0].ReplyTo

				Ω(fakenats.PublishedMessages[replyTo]).To(HaveLen(1))
				response := fakenats.PublishedMessages[replyTo][0]

				//we want to make sure the "error" key doesn't exist in the json string
				//because the receiver considers any JSON with an error key a failed staging,
				//regardless of what the error value is.
				Ω(string(response.Payload)).NotTo(ContainSubstring("error"))
				stagingResponse := StagingResponse{}
				json.Unmarshal(response.Payload, &stagingResponse)
				Ω(stagingResponse.Error).To(Equal(""))
			})
		})

		Context("when staging finishes unsuccessfully", func() {
			BeforeEach(func() {
				fauxstager.AlwaysFail = true
			})

			It("logs the failure", func() {
				Ω(testingSink.Records).To(HaveLen(0))

				publishStagingMessage()

				Eventually(testingSink.Records).ShouldNot(HaveLen(0))
			})

			It("sends a staging failure response", func() {
				publishStagingMessage()

				replyTo := fakenats.PublishedMessages["diego.staging.start"][0].ReplyTo

				Ω(fakenats.PublishedMessages[replyTo]).To(HaveLen(1))
				response := fakenats.PublishedMessages[replyTo][0]
				stagingResponse := StagingResponse{}
				json.Unmarshal(response.Payload, &stagingResponse)
				Ω(stagingResponse.Error).To(Equal("Staging failed"))
			})
		})
	})
})
