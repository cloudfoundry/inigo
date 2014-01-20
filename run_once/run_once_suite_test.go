package run_once_test

import (
	"os"
	"os/signal"
	"testing"

	"github.com/cloudfoundry/storeadapter/storerunner"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var etcdRunner *storerunner.ETCDClusterRunner

func TestRun_once(t *testing.T) {
	registerSignalHandler()
	RegisterFailHandler(Fail)

	etcdRunner = storerunner.NewETCDClusterRunner(5001, 1)
	etcdRunner.Start()

	RunSpecs(t, "Run_once Suite")

	etcdRunner.Stop()
}

var _ = BeforeEach(func() {
	etcdRunner.Reset()
})

func registerSignalHandler() {
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, os.Kill)

		select {
		case <-c:
			etcdRunner.Stop()
			os.Exit(0)
		}
	}()
}
