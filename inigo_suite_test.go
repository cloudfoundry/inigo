package inigo_test

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/onsi/ginkgo/config"

	"github.com/cloudfoundry/gunk/natsrunner"

	"github.com/cloudfoundry/storeadapter/storerunner/etcdstorerunner"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/vito/cmdtest"
	"github.com/vito/gordon"

	"github.com/cloudfoundry-incubator/inigo/executor_runner"
	"github.com/cloudfoundry-incubator/inigo/fake_cc"
	"github.com/cloudfoundry-incubator/inigo/fileserver_runner"
	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	"github.com/cloudfoundry-incubator/inigo/loggregator_runner"
	"github.com/cloudfoundry-incubator/inigo/stager_runner"
	"github.com/pivotal-cf-experimental/garden/integration/garden_runner"
)

var SHORT_TIMEOUT = 5.0
var LONG_TIMEOUT = 10.0

var etcdRunner *etcdstorerunner.ETCDClusterRunner

var gardenRunner *garden_runner.GardenRunner
var gardenAddr = filepath.Join(os.TempDir(), "garden-temp-socker", "warden.sock")
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
	var err error
	if os.Getenv("SHORT_TIMEOUT") != "" {
		SHORT_TIMEOUT, err = strconv.ParseFloat(os.Getenv("SHORT_TIMEOUT"), 64)
		if err != nil {
			panic(err)
		}
	}

	if os.Getenv("LONG_TIMEOUT") != "" {
		LONG_TIMEOUT, err = strconv.ParseFloat(os.Getenv("LONG_TIMEOUT"), 64)
		if err != nil {
			panic(err)
		}
	}

	registerSignalHandler()
	RegisterFailHandler(Fail)

	startUpFakeCC()
	setUpEtcd()
	setupGarden()
	setUpNats()
	setUpLoggregator()
	setUpExecutor()
	setUpStager()
	compileAndZipUpSmelter()
	setUpFileServer()
	connectToGarden()

	BeforeEach(func() {
		fakeCC.Reset()
		etcdRunner.Start()
		natsRunner.Start()
		loggregatorRunner.Start()

		inigo_server.Start(wardenClient)

		currentTestDescription := CurrentGinkgoTestDescription()
		fmt.Fprintf(GinkgoWriter, "\n%s\n%s\n\n", strings.Repeat("~", 50), currentTestDescription.FullTestText)
	})

	AfterEach(func() {
		executorRunner.Stop()
		stagerRunner.Stop()
		fileServerRunner.Stop()

		loggregatorRunner.Stop()
		natsRunner.Stop()
		etcdRunner.Stop()

		inigo_server.Stop(wardenClient)
	})

	RunSpecs(t, "Inigo Integration Suite")

	if config.GinkgoConfig.ParallelNode == 1 && config.GinkgoConfig.ParallelTotal > 1 {
		waitForOtherGinkgoNodes()
	}

	notifyFinished()

	cleanup()
}

func startUpFakeCC() {
	BeforeEach(func() {
		fakeCC = fake_cc.New()
		fakeCCAddress = fakeCC.Start()
	})
}

func setUpEtcd() {
	BeforeEach(func() {
		etcdRunner = etcdstorerunner.NewETCDClusterRunner(
			5001+config.GinkgoConfig.ParallelNode,
			1,
		)
	})
}

func setupGarden() {
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

	err = os.MkdirAll(filepath.Dir(gardenAddr), 0700)
	if err != nil {
		failFast(err.Error())
	}

	gardenPath, err := cmdtest.BuildIn(os.Getenv("GARDEN_GOPATH"), "github.com/pivotal-cf-experimental/garden", "-race")
	if err != nil {
		failFast("failed to compile garden:", err)
	}

	gardenRunner, err = garden_runner.New(gardenPath, gardenBinPath, gardenRootfs, "unix", gardenAddr)
	if err != nil {
		failFast("garden failed to initialize: " + err.Error())
	}

	gardenRunner.SnapshotsPath = ""
}

func connectToGarden() {
	var err error
	if config.GinkgoConfig.ParallelNode == 1 {
		err = gardenRunner.Start()
	} else {
		err = gardenRunner.WaitForStart()
	}

	if err != nil {
		failFast("garden failed to start: " + err.Error())
	}

	wardenClient = gardenRunner.NewClient()

	err = wardenClient.Connect()
	if err != nil {
		failFast("warden is not up: " + err.Error())
		return
	}
}

func setUpNats() {
	natsPort = 4222 + config.GinkgoConfig.ParallelNode

	BeforeEach(func() {
		natsRunner = natsrunner.NewNATSRunner(natsPort)
	})
}

func setUpLoggregator() {
	loggregatorPort = 3456 + config.GinkgoConfig.ParallelNode
	loggregatorSharedSecret = "conspiracy"

	loggregatorPath, err := cmdtest.BuildIn(os.Getenv("LOGGREGATOR_GOPATH"), "loggregator/loggregator")
	if err != nil {
		failFast("failed to compile loggregator:", err)
	}

	BeforeEach(func() {
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
	})
}

func setUpExecutor() {
	var err error
	executorPath, err = cmdtest.BuildIn(os.Getenv("EXECUTOR_GOPATH"), "github.com/cloudfoundry-incubator/executor", "-race")
	if err != nil {
		failFast("failed to compile executor:", err)
	}

	BeforeEach(func() {
		executorRunner = executor_runner.New(
			executorPath,
			gardenRunner.Network,
			gardenRunner.Addr,
			etcdRunner.NodeURLS(),
			fmt.Sprintf("127.0.0.1:%d", loggregatorPort),
			loggregatorSharedSecret,
		)
	})
}

func setUpStager() {
	var err error
	stagerPath, err = cmdtest.BuildIn(os.Getenv("STAGER_GOPATH"), "github.com/cloudfoundry-incubator/stager", "-race")
	if err != nil {
		failFast("failed to compile stager:", err)
	}

	BeforeEach(func() {
		stagerRunner = stager_runner.New(
			stagerPath,
			etcdRunner.NodeURLS(),
			[]string{fmt.Sprintf("127.0.0.1:%d", natsPort)},
		)
	})
}

func compileAndZipUpSmelter() {
	smelterPath, err := cmdtest.BuildIn(os.Getenv("LINUX_SMELTER_GOPATH"), "github.com/cloudfoundry-incubator/linux-smelter", "-race")
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
	fileServerPath, err = cmdtest.BuildIn(os.Getenv("FILE_SERVER_GOPATH"), "github.com/cloudfoundry-incubator/file-server", "-race")
	if err != nil {
		failFast("failed to compile file server:", err)
	}

	fileServerPort = 12760 + config.GinkgoConfig.ParallelNode

	BeforeEach(func() {
		fileServerRunner = fileserver_runner.New(
			fileServerPath,
			fileServerPort,
			etcdRunner.NodeURLS(),
			fakeCCAddress,
			fake_cc.CC_USERNAME,
			fake_cc.CC_PASSWORD,
		)
	})
}

func cleanup() {
	defer GinkgoRecover()

	if etcdRunner != nil {
		etcdRunner.Stop()
	}

	if config.GinkgoConfig.ParallelNode == 1 {
		if gardenRunner != nil {
			gardenRunner.TearDown()
			gardenRunner.Stop()
		}
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

func waitForOtherGinkgoNodes() {
	for {
		found := true
		for i := 2; i <= config.GinkgoConfig.ParallelTotal; i++ {
			_, err := os.Stat(fmt.Sprintf("/tmp/ginkgo-%d", i))
			if err != nil {
				found = false
			}
		}
		if found {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func notifyFinished() {
	ioutil.WriteFile(fmt.Sprintf("/tmp/ginkgo-%d", config.GinkgoConfig.ParallelNode), []byte("done"), 0777)
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
