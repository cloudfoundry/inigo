package helpers

import (
	"syscall"
	"time"

	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
)

func StopProcess(process ifrit.Process) {
	// ideally this would send SIGTERM and time out, sending SIGQUIT later;
	// sending SIGKILL hides graceful shutdown bugs
	//
	// see [#76301312]
	//
	// note that the warden runner reinterprets SIGKILL, because he's weird like
	// that.
	process.Signal(syscall.SIGKILL)
	Eventually(process.Wait(), 10*time.Second).Should(Receive())
}
