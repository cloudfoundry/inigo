package inigo_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudfoundry-incubator/converger/converger_runner"
	"github.com/cloudfoundry-incubator/executor/integration/executor_runner"
	WardenClient "github.com/cloudfoundry-incubator/garden/client"
	WardenConnection "github.com/cloudfoundry-incubator/garden/client/connection"
	"github.com/cloudfoundry-incubator/garden/warden"
	"github.com/cloudfoundry-incubator/rep/reprunner"
	WardenRunner "github.com/cloudfoundry-incubator/warden-linux/integration/runner"
	"github.com/cloudfoundry/gunk/natsrunner"
	"github.com/cloudfoundry/storeadapter/storerunner/etcdstorerunner"
	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/cloudfoundry-incubator/inigo/fake_cc"
	"github.com/cloudfoundry-incubator/inigo/fileserver_runner"
	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	"github.com/cloudfoundry-incubator/inigo/loggregator_runner"
	"github.com/cloudfoundry-incubator/inigo/stager_runner"
)

var SHORT_TIMEOUT = 5.0
var LONG_TIMEOUT = 15.0

var wardenAddr = filepath.Join(os.TempDir(), "warden-temp-socker", "warden.sock")

type sharedContextType struct {
	ExecutorPath    string
	ConvergerPath   string
	RepPath         string
	StagerPath      string
	FileServerPath  string
	LoggregatorPath string
	SmelterZipPath  string

	WardenAddr    string
	WardenNetwork string
}

func DecodeSharedContext(data []byte) sharedContextType {
	var context sharedContextType
	err := json.Unmarshal(data, &context)
	Ω(err).ShouldNot(HaveOccurred())

	return context
}

func (d sharedContextType) Encode() []byte {
	data, err := json.Marshal(d)
	Ω(err).ShouldNot(HaveOccurred())
	return data
}

type Runner interface {
	Stop()
}

type suiteContextType struct {
	SharedContext sharedContextType

	RepStack     string
	EtcdRunner   *etcdstorerunner.ETCDClusterRunner
	WardenClient warden.Client

	NatsRunner *natsrunner.NATSRunner
	NatsPort   int

	LoggregatorRunner       *loggregator_runner.LoggregatorRunner
	LoggregatorInPort       int
	LoggregatorOutPort      int
	LoggregatorSharedSecret string

	ExecutorRunner *executor_runner.ExecutorRunner
	ExecutorPort   int

	ConvergerRunner *converger_runner.ConvergerRunner

	RepRunner *reprunner.Runner
	RepPort   int

	StagerRunner *stager_runner.StagerRunner

	FakeCC        *fake_cc.FakeCC
	FakeCCAddress string

	FileServerRunner *fileserver_runner.FileServerRunner
	FileServerPort   int

	EtcdPort int
}

func (context suiteContextType) Runners() []Runner {
	return []Runner{
		context.ExecutorRunner,
		context.ConvergerRunner,
		context.RepRunner,
		context.StagerRunner,
		context.FileServerRunner,
		context.LoggregatorRunner,
		context.NatsRunner,
		context.EtcdRunner,
	}
}

func (context suiteContextType) StopRunners() {
	for _, stoppable := range context.Runners() {
		if !reflect.ValueOf(stoppable).IsNil() {
			stoppable.Stop()
		}
	}
}

var suiteContext suiteContextType

func beforeSuite(encodedSharedContext []byte) {
	sharedContext := DecodeSharedContext(encodedSharedContext)

	context := suiteContextType{
		SharedContext:           sharedContext,
		RepStack:                "lucid64",
		NatsPort:                4222 + config.GinkgoConfig.ParallelNode,
		ExecutorPort:            1700 + config.GinkgoConfig.ParallelNode,
		RepPort:                 20515 + config.GinkgoConfig.ParallelNode,
		LoggregatorInPort:       3456 + config.GinkgoConfig.ParallelNode,
		LoggregatorOutPort:      8083 + config.GinkgoConfig.ParallelNode,
		LoggregatorSharedSecret: "conspiracy",
		FileServerPort:          12760 + config.GinkgoConfig.ParallelNode,
		EtcdPort:                5001 + config.GinkgoConfig.ParallelNode,
	}

	context.FakeCC = fake_cc.New()
	context.FakeCCAddress = context.FakeCC.Start()

	context.EtcdRunner = etcdstorerunner.NewETCDClusterRunner(context.EtcdPort, 1)

	context.NatsRunner = natsrunner.NewNATSRunner(context.NatsPort)

	context.LoggregatorRunner = loggregator_runner.New(
		context.SharedContext.LoggregatorPath,
		loggregator_runner.Config{
			IncomingPort:           context.LoggregatorInPort,
			OutgoingPort:           context.LoggregatorOutPort,
			MaxRetainedLogMessages: 1000,
			SharedSecret:           context.LoggregatorSharedSecret,
			NatsHost:               "127.0.0.1",
			NatsPort:               context.NatsPort,
		},
	)

	context.ExecutorRunner = executor_runner.New(
		context.SharedContext.ExecutorPath,
		fmt.Sprintf("127.0.0.1:%d", context.ExecutorPort),
		context.SharedContext.WardenNetwork,
		context.SharedContext.WardenAddr,
		context.EtcdRunner.NodeURLS(),
		fmt.Sprintf("127.0.0.1:%d", context.LoggregatorInPort),
		context.LoggregatorSharedSecret,
	)

	context.ConvergerRunner = converger_runner.New(
		context.SharedContext.ConvergerPath,
		strings.Join(context.EtcdRunner.NodeURLS(), ","),
		"debug",
	)

	context.RepRunner = reprunner.New(
		context.SharedContext.RepPath,
		context.RepStack,
		fmt.Sprintf("127.0.0.1:%d", context.RepPort),
		fmt.Sprintf("http://127.0.0.1:%d", context.ExecutorPort),
		strings.Join(context.EtcdRunner.NodeURLS(), ","),
		"debug",
	)

	context.StagerRunner = stager_runner.New(
		context.SharedContext.StagerPath,
		context.EtcdRunner.NodeURLS(),
		[]string{fmt.Sprintf("127.0.0.1:%d", context.NatsPort)},
	)

	context.FileServerRunner = fileserver_runner.New(
		context.SharedContext.FileServerPath,
		context.FileServerPort,
		context.EtcdRunner.NodeURLS(),
		context.FakeCCAddress,
		fake_cc.CC_USERNAME,
		fake_cc.CC_PASSWORD,
	)

	wardenClient := WardenClient.New(&WardenConnection.Info{
		Network: context.SharedContext.WardenNetwork,
		Addr:    context.SharedContext.WardenAddr,
	})

	context.WardenClient = wardenClient

	// make context available to all tests
	suiteContext = context
}

func afterSuite() {
	suiteContext.StopRunners()
}

func TestInigo(t *testing.T) {
	extractTimeoutsFromEnvironment()

	RegisterFailHandler(Fail)

	nodeOne := &nodeOneType{}

	SynchronizedBeforeSuite(func() []byte {
		nodeOne.StartWarden()
		nodeOne.CompileTestedExecutables()

		return nodeOne.context.Encode()
	}, beforeSuite)

	BeforeEach(func() {
		suiteContext.FakeCC.Reset()
		suiteContext.EtcdRunner.Start()
		suiteContext.NatsRunner.Start()
		suiteContext.LoggregatorRunner.Start()

		inigo_server.Start(suiteContext.WardenClient)

		currentTestDescription := CurrentGinkgoTestDescription()
		fmt.Fprintf(GinkgoWriter, "\n%s\n%s\n\n", strings.Repeat("~", 50), currentTestDescription.FullTestText)
	})

	AfterEach(func() {
		suiteContext.StopRunners()

		inigo_server.Stop(suiteContext.WardenClient)
	})

	SynchronizedAfterSuite(afterSuite, nodeOne.StopWarden)

	RunSpecs(t, "Inigo Integration Suite")
}

func extractTimeoutsFromEnvironment() {
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
}

type nodeOneType struct {
	wardenRunner *WardenRunner.Runner
	context      sharedContextType
}

func (node *nodeOneType) StartWarden() {
	wardenBinPath := os.Getenv("WARDEN_BINPATH")
	wardenRootfs := os.Getenv("WARDEN_ROOTFS")

	if wardenBinPath == "" || wardenRootfs == "" {
		println("Please define either WARDEN_NETWORK and WARDEN_ADDR (for a running Warden), or")
		println("WARDEN_BINPATH and WARDEN_ROOTFS (for the tests to start it)")
		println("")

		Fail("warden is not set up")
	}
	var err error

	err = os.MkdirAll(filepath.Dir(wardenAddr), 0700)
	Ω(err).ShouldNot(HaveOccurred())

	wardenPath, err := gexec.BuildIn(os.Getenv("WARDEN_LINUX_GOPATH"), "github.com/cloudfoundry-incubator/warden-linux", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.wardenRunner, err = WardenRunner.New(wardenPath, wardenBinPath, wardenRootfs, "unix", wardenAddr)
	Ω(err).ShouldNot(HaveOccurred())

	node.wardenRunner.SnapshotsPath = ""

	err = node.wardenRunner.Start()
	Ω(err).ShouldNot(HaveOccurred())

	node.context.WardenAddr = node.wardenRunner.Addr
	node.context.WardenNetwork = node.wardenRunner.Network
}

func (node *nodeOneType) CompileTestedExecutables() {
	var err error
	node.context.LoggregatorPath, err = gexec.BuildIn(os.Getenv("LOGGREGATOR_GOPATH"), "loggregator/loggregator")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.ExecutorPath, err = gexec.BuildIn(os.Getenv("EXECUTOR_GOPATH"), "github.com/cloudfoundry-incubator/executor", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.ConvergerPath, err = gexec.BuildIn(os.Getenv("CONVERGER_GOPATH"), "github.com/cloudfoundry-incubator/converger", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.RepPath, err = gexec.BuildIn(os.Getenv("REP_GOPATH"), "github.com/cloudfoundry-incubator/rep", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.StagerPath, err = gexec.BuildIn(os.Getenv("STAGER_GOPATH"), "github.com/cloudfoundry-incubator/stager", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.FileServerPath, err = gexec.BuildIn(os.Getenv("FILE_SERVER_GOPATH"), "github.com/cloudfoundry-incubator/file-server", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.SmelterZipPath = node.compileAndZipUpSmelter()
}

func (node *nodeOneType) compileAndZipUpSmelter() string {
	smelterPath, err := gexec.BuildIn(os.Getenv("LINUX_SMELTER_GOPATH"), "github.com/cloudfoundry-incubator/linux-smelter", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	smelterDir := filepath.Dir(smelterPath)
	err = os.Rename(smelterPath, filepath.Join(smelterDir, "run"))
	Ω(err).ShouldNot(HaveOccurred())

	cmd := exec.Command("zip", "smelter.zip", "run")
	cmd.Dir = smelterDir
	err = cmd.Run()
	Ω(err).ShouldNot(HaveOccurred())

	return filepath.Join(smelterDir, "smelter.zip")
}

func (node *nodeOneType) StopWarden() {
	if node.wardenRunner != nil {
		node.wardenRunner.TearDown()
		node.wardenRunner.Stop()
	}
}
