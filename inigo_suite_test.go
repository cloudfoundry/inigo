package inigo_test

import (
	"fmt"
	"github.com/onsi/ginkgo/config"
	"net"
	"os"
	"os/signal"
	"syscall"
	"testing"

	"github.com/cloudfoundry/gunk/natsrunner"

	"github.com/cloudfoundry/storeadapter/storerunner/etcdstorerunner"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/vito/cmdtest"
	"github.com/vito/gordon"

	"github.com/cloudfoundry-incubator/inigo/executor_runner"
	"github.com/cloudfoundry-incubator/inigo/inigolistener"
	"github.com/cloudfoundry-incubator/inigo/loggregator_runner"
	"github.com/cloudfoundry-incubator/inigo/stager_runner"
	"github.com/pivotal-cf-experimental/garden/integration/garden_runner"
)

var etcdRunner *etcdstorerunner.ETCDClusterRunner
var wardenClient gordon.Client
var executor *cmdtest.Session

var gardenRunner *garden_runner.GardenRunner
var executorRunner *executor_runner.ExecutorRunner
var loggregatorRunner *loggregator_runner.LoggregatorRunner
var executorPath string
var natsPort int
var natsRunner *natsrunner.NATSRunner
var stagerRunner *stager_runner.StagerRunner
var stagerPath string

var wardenNetwork, wardenAddr string

func TestInigo(t *testing.T) {
	registerSignalHandler()
	RegisterFailHandler(Fail)

	etcdRunner = etcdstorerunner.NewETCDClusterRunner(5001+config.GinkgoConfig.ParallelNode, 1)

	if _, err := net.Dial("tcp", "127.0.0.1:5001"); err == nil {
		failFast("another etcd is already running")
	}

	etcdRunner.Start()

	wardenNetwork = os.Getenv("WARDEN_NETWORK")
	wardenAddr = os.Getenv("WARDEN_ADDR")

	gardenBinPath := os.Getenv("GARDEN_BINPATH")
	gardenRootfs := os.Getenv("GARDEN_ROOTFS")

	if (wardenNetwork == "" || wardenAddr == "") && (gardenBinPath == "" || gardenRootfs == "") {
		println("Please define either WARDEN_NETWORK and WARDEN_ADDR (for a running Warden), or")
		println("GARDEN_BINPATH and GARDEN_ROOTFS (for the tests to start it)")
		println("")
		println("Skipping!")
		return
	}

	if gardenBinPath != "" && gardenRootfs != "" {
		var err error

		gardenRunner, err = garden_runner.New(gardenBinPath, gardenRootfs)
		if err != nil {
			failFast("garden failed to initialize: " + err.Error())
		}

		gardenRunner.SnapshotsPath = ""

		err = gardenRunner.Start()
		if err != nil {
			failFast("garden failed to start: " + err.Error())
		}

		wardenClient = gardenRunner.NewClient()

		wardenNetwork = gardenRunner.Network
		wardenAddr = gardenRunner.Addr
	} else {
		wardenClient = gordon.NewClient(&gordon.ConnectionInfo{
			Network: wardenNetwork,
			Addr:    wardenAddr,
		})
	}

	err := wardenClient.Connect()
	if err != nil {
		failFast("warden is not up")
		return
	}

	executorPath, err = cmdtest.Build("github.com/cloudfoundry-incubator/executor")
	if err != nil {
		failFast("failed to compile executor")
	}

	loggregatorPort := 3456 + config.GinkgoConfig.ParallelNode

	executorRunner = executor_runner.New(
		executorPath,
		wardenNetwork,
		wardenAddr,
		etcdRunner.NodeURLS(),
		fmt.Sprintf("127.0.0.1:%d", loggregatorPort),
		"conspiracy",
	)

	stagerPath, err = cmdtest.Build("github.com/cloudfoundry-incubator/stager")
	if err != nil {
		failFast("failed to compile stager")
	}

	natsPort = 4222 + config.GinkgoConfig.ParallelNode

	natsRunner = natsrunner.NewNATSRunner(natsPort)

	stagerRunner = stager_runner.New(
		stagerPath,
		etcdRunner.NodeURLS(),
		[]string{fmt.Sprintf("127.0.0.1:%d", natsPort)},
	)

	// HACK
	originalGopath := os.Getenv("GOPATH")

	os.Setenv("GOPATH", os.Getenv("LOGGREGATOR_GOPATH"))

	loggregatorPath, err := cmdtest.Build("loggregator/loggregator")
	if err != nil {
		failFast("failed to compile loggregator")
	}

	os.Setenv("GOPATH", originalGopath)

	loggregatorRunner = loggregator_runner.New(
		loggregatorPath,
		loggregator_runner.Config{
			IncomingPort:           loggregatorPort,
			OutgoingPort:           8083 + config.GinkgoConfig.ParallelNode,
			MaxRetainedLogMessages: 1000,
			SharedSecret:           "conspiracy",
			NatsHost:               "127.0.0.1",
			NatsPort:               natsPort,
		},
	)

	RunSpecs(t, "Inigo Integration Suite")

	cleanup()
}

var _ = BeforeEach(func() {
	natsRunner.Start()
	etcdRunner.Reset()
	loggregatorRunner.Start()

	if gardenRunner != nil {
		// local
		gardenRunner.DestroyContainers()
	} else {
		// remote
		nukeAllWardenContainers()
	}

	startInigoListener(wardenClient)
})

var _ = AfterEach(func() {
	executorRunner.Stop()
	stagerRunner.Stop()

	if natsRunner != nil {
		natsRunner.Stop()
	}

	if loggregatorRunner != nil {
		loggregatorRunner.Stop()
	}
})

func nukeAllWardenContainers() {
	listResponse, err := wardenClient.List()
	Ω(err).ShouldNot(HaveOccurred())

	handles := listResponse.GetHandles()
	for _, handle := range handles {
		wardenClient.Destroy(handle)
	}
}

func failFast(msg string) {
	println("!!!!! " + msg + " !!!!!")
	cleanup()
	os.Exit(1)
}

func cleanup() {
	if etcdRunner != nil {
		println("stopping etcd")
		etcdRunner.Stop()
	}

	if gardenRunner != nil {
		println("stopping garden")
		gardenRunner.Stop()
	}

	if stagerRunner != nil {
		println("stopping stager")
		stagerRunner.Stop()
	}

	if natsRunner != nil {
		println("stopping nats")
		natsRunner.Stop()
	}

	if loggregatorRunner != nil {
		println("stopping loggregator")
		loggregatorRunner.Stop()
	}
}

func registerSignalHandler() {
	c := make(chan os.Signal, 1)

	go func() {
		select {
		case <-c:
			println("cleaning up!")

			cleanup()

			println("goodbye!")
			os.Exit(1)
		}
	}()

	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
}

func startInigoListener(wardenClient gordon.Client) {
	inigolistener.Start(wardenClient)
}

func stopInigoListener(wardenClient gordon.Client) {
	inigolistener.Stop(wardenClient)
}
