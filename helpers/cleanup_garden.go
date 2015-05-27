package helpers

import (
	"fmt"
	"strings"
	"time"

	"github.com/cloudfoundry-incubator/garden"
	"github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func CleanupGarden(gardenClient garden.Client) []error {
	containers, err := gardenClient.Containers(nil)
	Expect(err).NotTo(HaveOccurred())

	fmt.Fprintf(ginkgo.GinkgoWriter, "cleaning up %d Garden containers", len(containers))

	// even if containers fail to destroy, stop garden, but still report the
	// errors
	destroyContainerErrors := []error{}
	for _, container := range containers {
		info, _ := container.Info()

		fmt.Fprintf(ginkgo.GinkgoWriter, "cleaning up container %s (%s)", container.Handle(), info.ContainerPath)

		// try to Destroy the container up to 3 times
		for i := 0; i < 3; i++ {
			err := gardenClient.Destroy(container.Handle())
			switch {
			case err == nil:
				// move on if Destroy succeeds
				break
			case strings.Contains(err.Error(), "unknown handle"):
				// move on if container doesn't exist
				break
			case strings.Contains(err.Error(), "container already being destroyed"):
				// move on if container is already being destroyed
				break
			case i == 2:
				// record an error if Destroy failed 3 times
				destroyContainerErrors = append(destroyContainerErrors, err)
			default:
				// try Destroy again otherwise
				time.Sleep(50 * time.Millisecond)
				continue
			}
		}
	}

	return destroyContainerErrors
}
