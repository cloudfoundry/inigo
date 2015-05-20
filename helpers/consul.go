package helpers

import (
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/cloudfoundry-incubator/consuladapter"
	. "github.com/onsi/gomega"
)

func ConsulWaitUntilReady() {
	_, port, err := net.SplitHostPort(addresses.Consul)
	Expect(err).NotTo(HaveOccurred())
	httpPort, err := strconv.Atoi(port)
	Expect(err).NotTo(HaveOccurred())

	startingPort := httpPort - consuladapter.PortOffsetHTTP

	cr := consuladapter.NewClusterRunner(startingPort, 1, "http")

	client := cr.NewClient()
	catalog := client.Catalog()

	Eventually(func() error {
		_, qm, err := catalog.Nodes(nil)
		if err != nil {
			return err
		}
		if qm.KnownLeader && qm.LastIndex > 0 {
			return nil
		}
		return errors.New("not ready")
	}, 10, 100*time.Millisecond).Should(BeNil())
}