package run_once_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/cloudfoundry/storeadapter"
	"github.com/cloudfoundry/storeadapter/workerpool"
)

type RunOncePayload struct {
	State string `json:"state"`
}

var _ = Describe("RunOnce", func() {
	var store storeadapter.StoreAdapter

	BeforeEach(func() {
		store = storeadapter.NewETCDStoreAdapter(
			etcdRunner.NodeURLS(),
			workerpool.NewWorkerPool(1),
		)

		err := store.Connect()
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		err := store.Disconnect()
		Expect(err).ToNot(HaveOccurred())
	})

	Context("when /run_once/{guid} is created", func() {
		It("eventually is claimed by an executor", func() {
			err := store.Set([]storeadapter.StoreNode{
				{
					Key:   "/run_once/abc",
					Value: []byte(`{"state":"PENDING"}`),
				},
			})

			Expect(err).ToNot(HaveOccurred())

			Eventually(func() string {
				response, err := store.Get("/run_once/abc")
				Expect(err).ToNot(HaveOccurred())

				var payload RunOncePayload

				err = json.Unmarshal(response.Value, &payload)
				Expect(err).ToNot(HaveOccurred())

				return payload.State
			}).Should(Equal("RUNNING"))
		})
	})
})
