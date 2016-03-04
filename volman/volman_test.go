package volman_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pivotal-golang/lager"
	"github.com/pivotal-golang/lager/lagertest"
)

var _ = Describe("Given volman and fakedriver", func() {

	var (
		logger lager.Logger
	)

	BeforeEach(func() {

		logger = lagertest.NewTestLogger("Volman Tests")

	})

	It("should mount a volumn", func() {

		var err error
		mountPointResponse, err := volmanClient.Mount(logger, "fakedriver", "someVolume", "someconfig")
		Expect(err).NotTo(HaveOccurred())
		Expect(mountPointResponse.Path).NotTo(BeEmpty())
	})

	Context("and a mounted volman", func() {

		var (
			mountPoint string
		)

		BeforeEach(func() {

			var err error
			mountPointResponse, err := volmanClient.Mount(logger, "fakedriver", "someVolume", "someconfig")
			Expect(err).NotTo(HaveOccurred())
			Expect(mountPointResponse.Path).NotTo(BeEmpty())
			mountPoint = mountPointResponse.Path

		})

		It("should be able to unmount the volume", func() {

			err := volmanClient.Unmount(logger, "fakedriver", mountPoint)
			Expect(err).NotTo(HaveOccurred())

		})

	})

})
