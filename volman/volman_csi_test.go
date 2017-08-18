package volman_test

import (
	"path"

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

	It("should be able to mount volume", func() {
		volumeName := "someVolume"
		csiMountRootDir := "local-node-plugin-mount"
		volumeConfig := map[string]interface{}{"volume_id": volumeName}

		expectedMountPath := path.Join(componentMaker.VolmanDriverConfigDir, csiMountRootDir, node.NODE_PLUGIN_ID, volumeName)

		mountPointResponse, err := volmanClient.Mount(logger, node.NODE_PLUGIN_ID, volumeName, volumeConfig)
		Expect(err).NotTo(HaveOccurred())

		mountPoint := mountPointResponse.Path
		Expect(mountPoint).To(Equal(expectedMountPath))
	})
})
