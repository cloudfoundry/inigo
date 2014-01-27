package run_once_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
)

var _ = Describe("RunOnce", func() {
	var bbs *Bbs.BBS

	BeforeEach(func() {
		runner.Start()

		bbs = Bbs.New(etcdRunner.Adapter())
	})

	AfterEach(func() {
		runner.Stop()
	})

	Context("when a RunOnce is desired", func() {
		BeforeEach(func() {
			err := bbs.DesireRunOnce(models.RunOnce{
				Guid: "some-guid",
			})
			Expect(err).ToNot(HaveOccurred())
		})

		It("eventually is running on an executor", func() {
			Eventually(func() []models.RunOnce {
				runOnces, _ := bbs.GetAllStartingRunOnces()
				return runOnces
			}, 5).Should(HaveLen(1))

			runOnces, _ := bbs.GetAllStartingRunOnces()
			runOnce := runOnces[0]

			Expect(runOnce.Guid).To(Equal("some-guid"))
			Expect(runOnce.ContainerHandle).ToNot(BeEmpty())

			listResponse, err := wardenClient.List()
			Expect(err).ToNot(HaveOccurred())

			Expect(listResponse.GetHandles()).To(ContainElement(runOnce.ContainerHandle))
		})
	})
})
