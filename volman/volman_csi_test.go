package volman_test

import (
	"path"

	"code.cloudfoundry.org/volman"

	"github.com/jeffpak/local-node-plugin/node"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Given volman and a local-node-plugin", func() {
	var (
		volumeName          string
		containerId         string
		csiVolume           string
		csiMountRootDir     string
		expectedMountPath   string
		volumeConfig        map[string]interface{}
		mountResponse       volman.MountResponse
		listDriversResponse volman.ListDriversResponse
		err                 error
	)

	It("should eventually discover the plugin", func() {
		Eventually(func() bool {
			listDriversResponse, err = volmanClient.ListDrivers(logger)
			Expect(err).NotTo(HaveOccurred())
			infos := listDriversResponse.Drivers
			for _, info := range infos {
				if info.Name == node.NODE_PLUGIN_ID {
					return true
				}
			}
			return false
		}).Should(Equal(true))
	})

	Context("when running mount and unmount", func() {
		BeforeEach(func() {
			volumeName = "someVolume"
			containerId = "someContainer"
			csiVolume = "csiVolume"
			csiMountRootDir = "local-node-plugin-mount"
			volumeConfig = map[string]interface{}{"id": csiVolume, "attributes": map[string]string{}}
			expectedMountPath = path.Join(componentMaker.VolmanDriverConfigDir(), csiMountRootDir, "mounts", node.NODE_PLUGIN_ID, volumeName)
		})

		JustBeforeEach(func() {
			mountResponse, err = volmanClient.Mount(logger, node.NODE_PLUGIN_ID, volumeName, containerId, volumeConfig)
		})

		Context("when volumeConfig doesn't have any attributes", func() {
			BeforeEach(func() {
				volumeConfig = map[string]interface{}{"id": csiVolume}
			})

			It("should be able to mount volume", func() {
				Expect(err).NotTo(HaveOccurred())
				mountPoint := mountResponse.Path
				Expect(mountPoint).To(Equal(expectedMountPath))
				Expect(expectedMountPath).To(BeAnExistingFile())
			})

			It("should be able to unmount volume", func() {
				err = volmanClient.Unmount(logger, node.NODE_PLUGIN_ID, volumeName, containerId)
				Expect(err).NotTo(HaveOccurred())
				Expect(expectedMountPath).NotTo(BeAnExistingFile())
			})

		})

		It("should be able to mount volume", func() {
			Expect(err).NotTo(HaveOccurred())
			mountPoint := mountResponse.Path
			Expect(mountPoint).To(Equal(expectedMountPath))
			Expect(expectedMountPath).To(BeAnExistingFile())
		})

		It("should be able to unmount volume", func() {
			err = volmanClient.Unmount(logger, node.NODE_PLUGIN_ID, volumeName, containerId)
			Expect(err).NotTo(HaveOccurred())
			Expect(expectedMountPath).NotTo(BeAnExistingFile())
		})

	})
})
