package volman_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Given volman and fakedriver", func() {

	var driverId string
	var volumeId string

	BeforeEach(func() {
		driverId = "fakedriver"
		volumeId = "test-volume"
	})

	It("should mount a volume", func() {
		var err error
		someConfig := map[string]interface{}{"volume_id": "someID"}
		mountPointResponse, err := volmanClient.Mount(logger, driverId, volumeId, someConfig)
		Expect(err).NotTo(HaveOccurred())
		Expect(mountPointResponse.Path).NotTo(BeEmpty())
	})

	Context("and a mounted volman", func() {
		var mountPoint string

		BeforeEach(func() {
			var err error
			someConfig := map[string]interface{}{"volume_id": "someID"}
			mountPointResponse, err := volmanClient.Mount(logger, driverId, volumeId, someConfig)
			Expect(err).NotTo(HaveOccurred())
			Expect(mountPointResponse.Path).NotTo(BeEmpty())
			mountPoint = mountPointResponse.Path
		})

		It("should be able to unmount the volume", func() {
			err := volmanClient.Unmount(logger, driverId, volumeId)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
