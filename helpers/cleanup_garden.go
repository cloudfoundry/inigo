package helpers

import (
	"fmt"
	"strings"

	"github.com/cloudfoundry-incubator/garden"
	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func CleanupGarden(gardenClient garden.Client) []error {
	containers, err := gardenClient.Containers(nil)
	Ω(err).ShouldNot(HaveOccurred())

	fmt.Fprintf(ginkgo.GinkgoWriter, "cleaning up %d Garden containers", len(containers))

	// even if containers fail to destroy, stop garden, but still report the
	// errors
	destroyContainerErrors := []error{}
	for _, container := range containers {
		info, _ := container.Info()

		fmt.Fprintf(ginkgo.GinkgoWriter, "cleaning up container %s (%s)", container.Handle(), info.ContainerPath)

		err := gardenClient.Destroy(container.Handle())
		if err != nil {
			if strings.Contains(err.Error(), "unknown handle") {
				continue
			}

			if strings.Contains(err.Error(), "container already being destroyed") {
				continue
			}

			destroyContainerErrors = append(destroyContainerErrors, err)
		}
	}

	return destroyContainerErrors
}
