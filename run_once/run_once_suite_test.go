package run_once_test

import (
	"os"
	"os/signal"
	"testing"

	"github.com/cloudfoundry/storeadapter/storerunner"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/vito/gordon"
)

var etcdRunner *storerunner.ETCDClusterRunner
var wardenClient *gordon.Client

func TestRun_once(t *testing.T) {
	registerSignalHandler()
	RegisterFailHandler(Fail)

	etcdRunner = storerunner.NewETCDClusterRunner(5001, 1)
	etcdRunner.Start()

	wardenNetwork := os.Getenv("WARDEN_NETWORK")
	wardenAddr := os.Getenv("WARDEN_ADDR")

	if wardenNetwork == "" || wardenAddr == "" {
		println("WARDEN_NETWORK and/or WARDEN_ADDR not defined; skipping.")
		return
	}

	wardenClient = gordon.NewClient(&gordon.ConnectionInfo{
		Network: wardenNetwork,
		Addr:    wardenAddr,
	})

	err := wardenClient.Connect()
	Expect(err).ToNot(HaveOccurred())

	RunSpecs(t, "RunOnce Suite")

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
