package run_once_test

import (
	"fmt"
	"os"
	"os/signal"
	"testing"

	"github.com/cloudfoundry/storeadapter/storerunner"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/vito/cmdtest"
	"github.com/vito/gordon"

	"github.com/cloudfoundry-incubator/inigo/executor_runner"
	"github.com/cloudfoundry-incubator/inigo/garden_runner"
)

var etcdRunner *storerunner.ETCDClusterRunner
var wardenClient gordon.Client
var executor *cmdtest.Session

var gardenRunner *garden_runner.GardenRunner
var runner *executor_runner.ExecutorRunner

var wardenNetwork, wardenAddr string

func TestRun_once(t *testing.T) {
	registerSignalHandler()
	RegisterFailHandler(Fail)

	etcdRunner = storerunner.NewETCDClusterRunner(5001, 1)
	etcdRunner.Start()

	wardenNetwork = os.Getenv("WARDEN_NETWORK")
	wardenAddr = os.Getenv("WARDEN_ADDR")

	gardenRoot := os.Getenv("GARDEN_ROOT")
	gardenRootfs := os.Getenv("GARDEN_ROOTFS")

	if (wardenNetwork == "" || wardenAddr == "") && (gardenRoot == "" || gardenRootfs == "") {
		println("Please define either WARDEN_NETWORK and WARDEN_ADDR (for a running Warden), or")
		println("GARDEN_ROOT and GARDEN_ROOTFS (for the tests to start it)")
		println("")
		println("Skipping!")
		return
	}

	if gardenRoot != "" && gardenRootfs != "" {
		var err error

		gardenRunner, err = garden_runner.New(
			gardenRoot,
			gardenRootfs,
		)
		if err != nil {
			panic(err.Error())
		}

		gardenRunner.SnapshotsPath = ""

		err = gardenRunner.Start()
		if err != nil {
			panic(err.Error())
		}

		wardenClient = gardenRunner.NewClient()

		wardenNetwork = "tcp"
		wardenAddr = fmt.Sprintf("127.0.0.1:%d", gardenRunner.Port)
	} else {
		wardenClient = gordon.NewClient(&gordon.ConnectionInfo{
			Network: wardenNetwork,
			Addr:    wardenAddr,
		})
	}

	err := wardenClient.Connect()
	if err != nil {
		println("warden is not up!")
		os.Exit(1)
		return
	}

	executorPath, err := cmdtest.Build("github.com/cloudfoundry-incubator/executor")
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
	gardenRunner.Stop()
}

var _ = BeforeEach(func() {
	etcdRunner.Reset()
	gardenRunner.DestroyContainers()
})

func registerSignalHandler() {
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, os.Kill)

		select {
		case <-c:
			etcdRunner.Stop()
			gardenRunner.Stop()
			os.Exit(1)
		}
	}()
}
