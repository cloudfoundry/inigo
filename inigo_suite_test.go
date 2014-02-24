package inigo_test

import (
	"fmt"
	"github.com/onsi/ginkgo/config"
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
	"github.com/cloudfoundry-incubator/inigo/fileserver_runner"
	"github.com/cloudfoundry-incubator/inigo/inigolistener"
	"github.com/cloudfoundry-incubator/inigo/loggregator_runner"
	"github.com/cloudfoundry-incubator/inigo/stager_runner"
	"github.com/pivotal-cf-experimental/garden/integration/garden_runner"
)

var etcdRunner *etcdstorerunner.ETCDClusterRunner

var gardenRunner *garden_runner.GardenRunner
var wardenClient gordon.Client

var natsRunner *natsrunner.NATSRunner
var natsPort int

var loggregatorRunner *loggregator_runner.LoggregatorRunner
var loggregatorPort int
var loggregatorSharedSecret string

var executorRunner *executor_runner.ExecutorRunner
var executorPath string

var stagerRunner *stager_runner.StagerRunner
var stagerPath string

var fileServerRunner *fileserver_runner.FileServerRunner
var fileServerPath string
var fileServerPort int

func TestInigo(t *testing.T) {
	registerSignalHandler()
	RegisterFailHandler(Fail)

	setUpEtcd()
	startGarden()
	setUpNats()
	setUpLoggregator()
	setUpExecutor()
	setUpStager()
	setUpFileServer()

	RunSpecs(t, "Inigo Integration Suite")

	cleanup()
}

var _ = BeforeEach(func() {
	etcdRunner.Start()
	natsRunner.Start()
	loggregatorRunner.Start()

	gardenRunner.DestroyContainers()

	inigolistener.Start(wardenClient)
})

var _ = AfterEach(func() {
	executorRunner.Stop()
	stagerRunner.Stop()
	fileServerRunner.Stop()

	loggregatorRunner.Stop()
	natsRunner.Stop()
	etcdRunner.Stop()
})

func setUpEtcd() {
	etcdRunner = etcdstorerunner.NewETCDClusterRunner(5001+config.GinkgoConfig.ParallelNode, 1)
}

func startGarden() {
	gardenBinPath := os.Getenv("GARDEN_BINPATH")
	gardenRootfs := os.Getenv("GARDEN_ROOTFS")

	if gardenBinPath == "" || gardenRootfs == "" {
		println("Please define either WARDEN_NETWORK and WARDEN_ADDR (for a running Warden), or")
		println("GARDEN_BINPATH and GARDEN_ROOTFS (for the tests to start it)")
		println("")
		failFast("garden is not set up")
		return
	}

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

	err = wardenClient.Connect()
	if err != nil {
		failFast("warden is not up")
		return
	}
}

func setUpNats() {
	natsPort = 4222 + config.GinkgoConfig.ParallelNode
	natsRunner = natsrunner.NewNATSRunner(natsPort)
}

func setUpLoggregator() {
	loggregatorPort = 3456 + config.GinkgoConfig.ParallelNode
	loggregatorSharedSecret = "conspiracy"

	// hack around GOPATH to compile loggregator
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
			SharedSecret:           loggregatorSharedSecret,
			NatsHost:               "127.0.0.1",
			NatsPort:               natsPort,
		},
	)
}

func setUpExecutor() {
	var err error
	executorPath, err = cmdtest.Build("github.com/cloudfoundry-incubator/executor")
	if err != nil {
		failFast("failed to compile executor")
	}

	executorRunner = executor_runner.New(
		executorPath,
		gardenRunner.Network,
		gardenRunner.Addr,
		etcdRunner.NodeURLS(),
		fmt.Sprintf("127.0.0.1:%d", loggregatorPort),
		loggregatorSharedSecret,
	)
}

func setUpStager() {
	var err error
	stagerPath, err = cmdtest.Build("github.com/cloudfoundry-incubator/stager")
	if err != nil {
		failFast("failed to compile stager")
	}

	stagerRunner = stager_runner.New(
		stagerPath,
		etcdRunner.NodeURLS(),
		[]string{fmt.Sprintf("127.0.0.1:%d", natsPort)},
	)
}

func setUpFileServer() {
	var err error
	fileServerPath, err = cmdtest.Build("github.com/cloudfoundry-incubator/file-server")
	if err != nil {
		failFast("failed to compile file server")
	}
	fileServerPort = 12760 + config.GinkgoConfig.ParallelNode
	fileServerRunner = fileserver_runner.New(fileServerPath, fileServerPort, etcdRunner.NodeURLS())
}

func cleanup() {
	if etcdRunner != nil {
		etcdRunner.Stop()
	}

	if gardenRunner != nil {
		gardenRunner.Stop()
	}

	if natsRunner != nil {
		natsRunner.Stop()
	}

	if loggregatorRunner != nil {
		loggregatorRunner.Stop()
	}

	if executorRunner != nil {
		executorRunner.Stop()
	}

	if stagerRunner != nil {
		stagerRunner.Stop()
	}

	if fileServerRunner != nil {
		fileServerRunner.Stop()
	}
}

func failFast(msg string) {
	println("!!!!! " + msg + " !!!!!")
	cleanup()
	os.Exit(1)
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
