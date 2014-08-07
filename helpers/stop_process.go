package helpers

import (
	"fmt"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
)

func StopProcess(process ifrit.Process) {
	if process == nil {
		// sometimes components aren't initialized in individual tests, but a full
		// suite may want AfterEach to clean up everything
		return
	}

	process.Signal(syscall.SIGTERM)

	select {
	case <-process.Wait():
		return
	case <-time.After(10 * time.Second):
		fmt.Fprintf(GinkgoWriter, "!!!!!!!!!!!!!!!! STOP TIMEOUT !!!!!!!!!!!!!!!!")

		process.Signal(syscall.SIGQUIT)
		Eventually(process.Wait(), 10*time.Second).Should(Receive())

		Fail("process did not shut down cleanly; SIGQUIT sent")
	}
}
