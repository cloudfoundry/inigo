package volman_test

import (
	"path"

	"code.cloudfoundry.org/volman"

	"github.com/jeffpak/local-node-plugin/node"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Given volman and a local-node-plugin", func() {

	It("should eventually discover the plugin", func() {
		Eventually(func() bool {
			listDriversResponse, err := volmanClient.ListDrivers(logger)
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
		var (
			volumeName        string
			csiMountRootDir   string
			expectedMountPath string
			volumeConfig      map[string]interface{}
			mountResponse     volman.MountResponse
			err               error
		)

		BeforeEach(func() {
			volumeName = "someVolume"
			csiMountRootDir = "local-node-plugin-mount"
			volumeConfig = map[string]interface{}{"volume_id": volumeName}
			expectedMountPath = path.Join(componentMaker.VolmanDriverConfigDir, csiMountRootDir, node.NODE_PLUGIN_ID, volumeName)
		})

		JustBeforeEach(func() {
			mountResponse, err = volmanClient.Mount(logger, node.NODE_PLUGIN_ID, volumeName, volumeConfig)
		})

		It("should be able to mount volume", func() {
			Expect(err).NotTo(HaveOccurred())
			mountPoint := mountResponse.Path
			Expect(mountPoint).To(Equal(expectedMountPath))
		})

		It("should be able to unmount volume", func() {
			err = volmanClient.Unmount(logger, node.NODE_PLUGIN_ID, volumeName)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
