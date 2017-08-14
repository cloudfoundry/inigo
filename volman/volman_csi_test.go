package volman_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/jeffpak/local-node-plugin/node"
)

var _ = Describe("Given volman and a local-node-plugin", func() {

	It("should eventually discover the plugin", func() {
		Eventually(func() bool {
			listDriversResponse, err := volmanClient.ListDrivers(logger)
			Expect(err).NotTo(HaveOccurred())
			infos := listDriversResponse.Drivers
			for _, info := range infos {
				if info.Name == node.NODE_PLUGIN_ID {
					return true;
				}
			}
			return false
		}).Should(Equal(true));
	})
})
