package inigo_test

import (
	"encoding/json"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry/gunk/timeprovider"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func publishMessageToNats(name string, regMessage models.CCRegistrationMessage) {
	msg, err := json.Marshal(regMessage)
	Ω(err).ShouldNot(HaveOccurred())

	err = natsRunner.MessageBus.Publish(name, msg)
	Ω(err).ShouldNot(HaveOccurred())
}

var _ = FDescribe("Servistry", func() {

	var bbs *Bbs.BBS
	var ccUrl = "http://1.2.3.4:8080"
	var regMessage = models.CCRegistrationMessage{
		Host: "1.2.3.4",
		Port: 8080,
	}

	BeforeEach(func() {
		servistryRunner.Start()
		bbs = Bbs.New(etcdRunner.Adapter(), timeprovider.NewTimeProvider())
	})

	Describe("Register CC", func() {
		Context("when a register message is sent over NATS", func() {

			BeforeEach(func() {
				publishMessageToNats("router.register", regMessage)
			})

			It("registers cc in etcd", func() {
				// Wait for the BBS to confirm that CC has been registered
				Eventually(func() bool {
					urls, err := bbs.GetAvailableCC()
					Ω(err).ShouldNot(HaveOccurred())
					return len(urls) > 0 && urls[0] == ccUrl
				})
			})
		})
	})

	Describe("Unregister CC", func() {

		BeforeEach(func() {
			publishMessageToNats("router.register", regMessage)

			// Wait for the BBS to confirm that CC has been registered
			Eventually(func() bool {
				urls, err := bbs.GetAvailableCC()
				Ω(err).ShouldNot(HaveOccurred())
				return len(urls) > 0 && urls[0] == ccUrl
			})

			publishMessageToNats("router.unregister", regMessage)
		})

		It("unregisters cc in etcd", func() {
			// Wait for the BBS to confirm that CC has been unregistered
			Eventually(func() bool {
				urls, err := bbs.GetAvailableCC()
				Ω(err).ShouldNot(HaveOccurred())
				return len(urls) == 0
			})
		})
	})
})
