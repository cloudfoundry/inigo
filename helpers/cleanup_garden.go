package helpers

import (
	"strings"

	garden "github.com/cloudfoundry-incubator/garden/api"
	. "github.com/onsi/gomega"
)

func CleanupGarden(gardenClient garden.Client) []error {
	containers, err := gardenClient.Containers(nil)
	Ω(err).ShouldNot(HaveOccurred())

	// even if containers fail to destroy, stop garden, but still report the
	// errors
	destroyContainerErrors := []error{}
	for _, container := range containers {
		err := gardenClient.Destroy(container.Handle())
		if err != nil {
			if !strings.Contains(err.Error(), "unknown handle") {
				destroyContainerErrors = append(destroyContainerErrors, err)
			}
		}
	}

	return destroyContainerErrors
}
