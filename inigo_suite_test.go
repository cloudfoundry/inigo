package inigo_test

import (
	"fmt"
	"github.com/onsi/ginkgo/config"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/cloudfoundry/gunk/natsrunner"

	"github.com/cloudfoundry/storeadapter/storerunner/etcdstorerunner"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/vito/cmdtest"
	"github.com/vito/gordon"

	"github.com/cloudfoundry-incubator/inigo/executor_runner"
	"github.com/cloudfoundry-incubator/inigo/fake_cc"
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

var fakeCC *fake_cc.FakeCC
var fakeCCAddress string

var fileServerRunner *fileserver_runner.FileServerRunner
var fileServerPath string
var fileServerPort int

var smelterZipPath string

func TestInigo(t *testing.T) {
	registerSignalHandler()
	RegisterFailHandler(Fail)

	startUpFakeCC()
	setUpEtcd()
	setUpGarden()
	setUpNats()
	setUpLoggregator()
	setUpExecutor()
	setUpStager()
	compileAndZipUpSmelter()
	setUpFileServer()

	RunSpecs(t, "Inigo Integration Suite")

	cleanup()
}

var _ = BeforeEach(func() {
	fakeCC.Reset()
	startGarden()
	etcdRunner.Start()
	natsRunner.Start()
	loggregatorRunner.Start()

	gardenRunner.DestroyContainers()

	inigolistener.Start(wardenClient)

	currentTestDescription := CurrentGinkgoTestDescription()
	fmt.Fprintf(GinkgoWriter, "\n%s\n%s\n\n", strings.Repeat("~", 50), currentTestDescription.FullTestText)
})

var _ = AfterEach(func() {
	executorRunner.Stop()
	stagerRunner.Stop()
	fileServerRunner.Stop()

	loggregatorRunner.Stop()
	natsRunner.Stop()
	etcdRunner.Stop()
})

func startUpFakeCC() {
	fakeCC = fake_cc.New()
	fakeCCAddress = fakeCC.Start()
}

func setUpEtcd() {
	etcdRunner = etcdstorerunner.NewETCDClusterRunner(5001+config.GinkgoConfig.ParallelNode, 1)
}

func setUpGarden() {
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
}

var didStartGarden = false

func startGarden() {
	if didStartGarden {
		return
	}
	didStartGarden = true

	err := gardenRunner.Start()
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

	loggregatorPath, err := cmdtest.BuildIn("loggregator/loggregator", os.Getenv("LOGGREGATOR_GOPATH"))
	if err != nil {
		failFast("failed to compile loggregator")
	}

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
	executorPath, err = cmdtest.BuildIn("github.com/cloudfoundry-incubator/executor", os.Getenv("EXECUTOR_GOPATH"))
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
	stagerPath, err = cmdtest.BuildIn("github.com/cloudfoundry-incubator/stager", os.Getenv("STAGER_GOPATH"))
	if err != nil {
		failFast("failed to compile stager")
	}

	stagerRunner = stager_runner.New(
		stagerPath,
		etcdRunner.NodeURLS(),
		[]string{fmt.Sprintf("127.0.0.1:%d", natsPort)},
	)
}

func compileAndZipUpSmelter() {
	smelterPath, err := cmdtest.BuildIn("github.com/cloudfoundry-incubator/linux-smelter", os.Getenv("LINUX_SMELTER_GOPATH"))
	if err != nil {
		failFast("failed to compile smelter", err)
	}

	smelterDir := filepath.Dir(smelterPath)
	err = os.Rename(smelterPath, filepath.Join(smelterDir, "run"))
	if err != nil {
		failFast("failed to move smelter", err)
	}

	cmd := exec.Command("zip", "smelter.zip", "run")
	cmd.Dir = smelterDir
	err = cmd.Run()
	if err != nil {
		failFast("failed to zip up smelter", err)
	}

	smelterZipPath = filepath.Join(smelterDir, "smelter.zip")
}

func setUpFileServer() {
	var err error
	fileServerPath, err = cmdtest.BuildIn("github.com/cloudfoundry-incubator/file-server", os.Getenv("FILE_SERVER_GOPATH"))
	if err != nil {
		failFast("failed to compile file server")
	}
	fileServerPort = 12760 + config.GinkgoConfig.ParallelNode
	fileServerRunner = fileserver_runner.New(fileServerPath, fileServerPort, etcdRunner.NodeURLS(), fakeCCAddress, fake_cc.CC_USERNAME, fake_cc.CC_PASSWORD)
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

func failFast(msg string, errs ...error) {
	println("!!!!! " + msg + " !!!!!")
	if len(errs) > 0 {
		println("error: " + errs[0].Error())
	}
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
