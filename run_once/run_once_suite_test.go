package run_once_test

import (
	"os"
	"os/signal"
	"testing"

	"github.com/cloudfoundry/storeadapter/storerunner"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/vito/cmdtest"
	"github.com/vito/gordon"

	"github.com/pivotal-cf-experimental/inigo/executor_runner"
)

var etcdRunner *storerunner.ETCDClusterRunner
var wardenClient gordon.Client
var executor *cmdtest.Session

var runner *executor_runner.ExecutorRunner

var wardenNetwork, wardenAddr string

func TestRun_once(t *testing.T) {
	registerSignalHandler()
	RegisterFailHandler(Fail)

	etcdRunner = storerunner.NewETCDClusterRunner(5001, 1)
	etcdRunner.Start()

	wardenNetwork = os.Getenv("WARDEN_NETWORK")
	wardenAddr = os.Getenv("WARDEN_ADDR")

	if wardenNetwork == "" || wardenAddr == "" {
		println("WARDEN_NETWORK and/or WARDEN_ADDR not defined; skipping.")
		return
	}

	wardenClient = gordon.NewClient(&gordon.ConnectionInfo{
		Network: wardenNetwork,
		Addr:    wardenAddr,
	})

	err := wardenClient.Connect()
	if err != nil {
		println("warden is not up!")
		os.Exit(1)
		return
	}

	executorPath, err := cmdtest.Build("github.com/pivotal-cf-experimental/executor")
	if err != nil {
		println("failed to compile!")
		os.Exit(1)
		return
	}

	runner = executor_runner.New(
		executorPath,
		wardenNetwork,
		wardenAddr,
		etcdRunner.NodeURLS(),
	)

	RunSpecs(t, "RunOnce Suite")

	etcdRunner.Stop()
}

var _ = BeforeEach(func() {
	etcdRunner.Reset()
	nukeAllWardenContainers()
})

func nukeAllWardenContainers() {
	listResponse, err := wardenClient.List()
	Ω(err).ShouldNot(HaveOccurred())

	handles := listResponse.GetHandles()
	for _, handle := range handles {
		_, err := wardenClient.Destroy(handle)
		Ω(err).ShouldNot(HaveOccurred())
	}
}

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
