package inigo_test

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cloudfoundry-incubator/app-manager/integration/app_manager_runner"
	"github.com/cloudfoundry-incubator/auctioneer/integration/auctioneer_runner"
	"github.com/cloudfoundry-incubator/converger/converger_runner"
	"github.com/cloudfoundry-incubator/executor/integration/executor_runner"
	"github.com/cloudfoundry-incubator/garden/warden"
	"github.com/cloudfoundry-incubator/nsync/integration/runner"
	"github.com/cloudfoundry-incubator/rep/reprunner"
	"github.com/cloudfoundry-incubator/route-emitter/integration/route_emitter_runner"
	"github.com/cloudfoundry-incubator/tps/integration/tpsrunner"
	WardenRunner "github.com/cloudfoundry-incubator/warden-linux/integration/runner"
	gorouterconfig "github.com/cloudfoundry/gorouter/config"
	"github.com/cloudfoundry/gunk/natsrunner"
	"github.com/cloudfoundry/storeadapter/storerunner/etcdstorerunner"
	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/tedsuo/ifrit"

	"github.com/cloudfoundry-incubator/inigo/fake_cc"
	"github.com/cloudfoundry-incubator/inigo/fileserver_runner"
	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	"github.com/cloudfoundry-incubator/inigo/loggregator_runner"
	"github.com/cloudfoundry-incubator/inigo/router_runner"
	"github.com/cloudfoundry-incubator/stager/integration/stager_runner"
)

var SHORT_TIMEOUT = 5.0
var LONG_TIMEOUT = 15.0
var AUCTION_MAX_ROUNDS = 3 //we limit this to prevent overwhelming numbers of auctioneer logs.  it should not impact the behavior of the tests.

var wardenRunner *WardenRunner.Runner

type sharedContextType struct {
	AuctioneerPath    string
	ExecutorPath      string
	ConvergerPath     string
	RepPath           string
	StagerPath        string
	AppManagerPath    string
	NsyncListenerPath string
	FileServerPath    string
	LoggregatorPath   string
	RouteEmitterPath  string
	RouterPath        string
	CircusZipPath     string
	TPSPath           string
	WardenPath        string
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
	KillWithFire()
}

type suiteContextType struct {
	SharedContext sharedContextType

	ExternalAddress string

	RepStack   string
	RepID      string
	EtcdRunner *etcdstorerunner.ETCDClusterRunner

	WardenProcess ifrit.Process
	WardenClient  warden.Client
	WardenPort    int

	NatsRunner *natsrunner.NATSRunner
	NatsPort   int

	LoggregatorRunner       *loggregator_runner.LoggregatorRunner
	LoggregatorInPort       int
	LoggregatorOutPort      int
	LoggregatorSharedSecret string

	AuctioneerRunner *auctioneer_runner.AuctioneerRunner

	ExecutorRunner *executor_runner.ExecutorRunner
	ExecutorPort   int

	ConvergerRunner *converger_runner.ConvergerRunner

	RepRunner *reprunner.Runner
	RepPort   int

	StagerRunner     *stager_runner.StagerRunner
	AppManagerRunner *app_manager_runner.AppManagerRunner

	NsyncListenerRunner ifrit.Runner

	FakeCC        *fake_cc.FakeCC
	FakeCCAddress string

	FileServerRunner *fileserver_runner.FileServerRunner
	FileServerPort   int

	RouteEmitterRunner *route_emitter_runner.Runner

	RouterRunner *router_runner.Runner
	RouterPort   int

	TPSRunner            ifrit.Runner
	TPSAddress           string
	TPSHeartbeatInterval time.Duration

	EtcdPort int
}

func (context suiteContextType) Runners() []Runner {
	return []Runner{
		context.AuctioneerRunner,
		context.ExecutorRunner,
		context.ConvergerRunner,
		context.RepRunner,
		context.StagerRunner,
		context.AppManagerRunner,
		context.FileServerRunner,
		context.LoggregatorRunner,
		context.NatsRunner,
		context.EtcdRunner,
		context.RouteEmitterRunner,
		context.RouterRunner,
	}
}

func (context suiteContextType) StopRunners() {
	for _, stoppable := range context.Runners() {
		if !reflect.ValueOf(stoppable).IsNil() {
			stoppable.KillWithFire()
		}
	}
}

var suiteContext suiteContextType

func beforeSuite(encodedSharedContext []byte) {
	sharedContext := DecodeSharedContext(encodedSharedContext)

	context := suiteContextType{
		SharedContext:           sharedContext,
		ExternalAddress:         os.Getenv("EXTERNAL_ADDRESS"),
		RepStack:                "lucid64",
		RepID:                   "the-rep-id-" + string(config.GinkgoConfig.ParallelNode),
		NatsPort:                4222 + config.GinkgoConfig.ParallelNode,
		ExecutorPort:            1700 + config.GinkgoConfig.ParallelNode,
		RepPort:                 20515 + config.GinkgoConfig.ParallelNode,
		LoggregatorInPort:       3456 + config.GinkgoConfig.ParallelNode,
		LoggregatorOutPort:      8083 + config.GinkgoConfig.ParallelNode,
		LoggregatorSharedSecret: "conspiracy",
		FileServerPort:          12760 + config.GinkgoConfig.ParallelNode,
		EtcdPort:                5001 + config.GinkgoConfig.ParallelNode,
		RouterPort:              9090 + config.GinkgoConfig.ParallelNode,
		TPSAddress:              fmt.Sprintf("127.0.0.1:%d", 1518+config.GinkgoConfig.ParallelNode),
		TPSHeartbeatInterval:    5 * time.Second,
		WardenPort:              11997 + config.GinkgoConfig.ParallelNode,
	}

	Ω(context.ExternalAddress).ShouldNot(BeEmpty())

	wardenBinPath := os.Getenv("WARDEN_BINPATH")
	wardenRootfs := os.Getenv("WARDEN_ROOTFS")

	if wardenBinPath == "" || wardenRootfs == "" {
		println("Please define either WARDEN_NETWORK and WARDEN_ADDR (for a running Warden), or")
		println("WARDEN_BINPATH and WARDEN_ROOTFS (for the tests to start it)")
		println("")

		Fail("warden is not set up")
	}

	wardenAddress := fmt.Sprintf("127.0.0.1:%d", context.WardenPort)
	wardenRunner = WardenRunner.New(
		wardenAddress,
		context.SharedContext.WardenPath,
		wardenBinPath,
		wardenRootfs,
	)

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

	context.AuctioneerRunner = auctioneer_runner.New(
		context.SharedContext.AuctioneerPath,
		context.EtcdRunner.NodeURLS(),
		[]string{fmt.Sprintf("127.0.0.1:%d", context.NatsPort)},
	)

	context.ExecutorRunner = executor_runner.New(
		context.SharedContext.ExecutorPath,
		fmt.Sprintf("127.0.0.1:%d", context.ExecutorPort),
		"tcp",
		wardenAddress,
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
		context.RepID,
		context.RepStack,
		context.ExternalAddress,
		fmt.Sprintf("127.0.0.1:%d", context.RepPort),
		fmt.Sprintf("http://127.0.0.1:%d", context.ExecutorPort),
		strings.Join(context.EtcdRunner.NodeURLS(), ","),
		fmt.Sprintf("127.0.0.1:%d", context.NatsPort),
		"debug",
		time.Second,
	)

	context.StagerRunner = stager_runner.New(
		context.SharedContext.StagerPath,
		context.EtcdRunner.NodeURLS(),
		[]string{fmt.Sprintf("127.0.0.1:%d", context.NatsPort)},
	)

	context.NsyncListenerRunner = runner.NewRunner(
		"nsync.listener.started",
		context.SharedContext.NsyncListenerPath,
		"-etcdCluster", strings.Join(context.EtcdRunner.NodeURLS(), ","),
		"-natsAddresses", fmt.Sprintf("127.0.0.1:%d", context.NatsPort),
	)

	context.AppManagerRunner = app_manager_runner.New(
		context.SharedContext.AppManagerPath,
		context.EtcdRunner.NodeURLS(),
		map[string]string{context.RepStack: "some-lifecycle-bundle.tgz"},
		fmt.Sprintf("127.0.0.1:%d", context.RepPort),
	)

	context.FileServerRunner = fileserver_runner.New(
		context.SharedContext.FileServerPath,
		context.FileServerPort,
		context.EtcdRunner.NodeURLS(),
		context.FakeCCAddress,
		fake_cc.CC_USERNAME,
		fake_cc.CC_PASSWORD,
	)

	context.RouteEmitterRunner = route_emitter_runner.New(
		context.SharedContext.RouteEmitterPath,
		context.EtcdRunner.NodeURLS(),
		[]string{fmt.Sprintf("127.0.0.1:%d", context.NatsPort)},
	)

	context.RouterRunner = router_runner.New(
		context.SharedContext.RouterPath,
		&gorouterconfig.Config{
			Port: uint16(context.RouterPort),

			PruneStaleDropletsIntervalInSeconds: 5,
			DropletStaleThresholdInSeconds:      10,
			PublishActiveAppsIntervalInSeconds:  0,
			StartResponseDelayIntervalInSeconds: 1,

			Nats: []gorouterconfig.NatsConfig{
				{
					Host: "127.0.0.1",
					Port: uint16(context.NatsPort),
				},
			},
			Logging: gorouterconfig.LoggingConfig{
				File:  "/dev/stdout",
				Level: "info",
			},
		},
	)

	context.TPSRunner = tpsrunner.New(
		context.SharedContext.TPSPath,
		context.TPSAddress,
		context.EtcdRunner.NodeURLS(),
		[]string{fmt.Sprintf("127.0.0.1:%d", context.NatsPort)},
		context.TPSHeartbeatInterval,
	)

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
		nodeOne.CompileTestedExecutables()

		return nodeOne.context.Encode()
	}, beforeSuite)

	BeforeEach(func() {
		suiteContext.FakeCC.Reset()
		suiteContext.EtcdRunner.Start()
		suiteContext.NatsRunner.Start()
		suiteContext.LoggregatorRunner.Start()

		suiteContext.WardenClient = wardenRunner.NewClient()
		suiteContext.WardenProcess = ifrit.Envoke(wardenRunner)
		Eventually(wardenRunner.TryDial, 10).ShouldNot(HaveOccurred())

		inigo_server.Start(suiteContext.WardenClient)

		currentTestDescription := CurrentGinkgoTestDescription()
		fmt.Fprintf(GinkgoWriter, "\n%s\n%s\n\n", strings.Repeat("~", 50), currentTestDescription.FullTestText)
	})

	AfterEach(func() {
		suiteContext.StopRunners()

		inigo_server.Stop(suiteContext.WardenClient)

		suiteContext.WardenProcess.Signal(syscall.SIGKILL)
		Eventually(suiteContext.WardenProcess.Wait(), 5).Should(Receive())
	})

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
	context sharedContextType
}

func (node *nodeOneType) CompileTestedExecutables() {
	var err error

	node.context.WardenPath, err = gexec.BuildIn(os.Getenv("WARDEN_LINUX_GOPATH"), "github.com/cloudfoundry-incubator/warden-linux", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.LoggregatorPath, err = gexec.BuildIn(os.Getenv("LOGGREGATOR_GOPATH"), "loggregator/loggregator")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.AuctioneerPath, err = gexec.BuildIn(os.Getenv("AUCTIONEER_GOPATH"), "github.com/cloudfoundry-incubator/auctioneer", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.ExecutorPath, err = gexec.BuildIn(os.Getenv("EXECUTOR_GOPATH"), "github.com/cloudfoundry-incubator/executor", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.ConvergerPath, err = gexec.BuildIn(os.Getenv("CONVERGER_GOPATH"), "github.com/cloudfoundry-incubator/converger", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.RepPath, err = gexec.BuildIn(os.Getenv("REP_GOPATH"), "github.com/cloudfoundry-incubator/rep", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.StagerPath, err = gexec.BuildIn(os.Getenv("STAGER_GOPATH"), "github.com/cloudfoundry-incubator/stager", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.NsyncListenerPath, err = gexec.BuildIn(os.Getenv("NSYNC_GOPATH"), "github.com/cloudfoundry-incubator/nsync/listener", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.AppManagerPath, err = gexec.BuildIn(os.Getenv("APP_MANAGER_GOPATH"), "github.com/cloudfoundry-incubator/app-manager", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.FileServerPath, err = gexec.BuildIn(os.Getenv("FILE_SERVER_GOPATH"), "github.com/cloudfoundry-incubator/file-server", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.RouteEmitterPath, err = gexec.BuildIn(os.Getenv("ROUTE_EMITTER_GOPATH"), "github.com/cloudfoundry-incubator/route-emitter", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.RouterPath, err = gexec.BuildIn(os.Getenv("ROUTER_GOPATH"), "github.com/cloudfoundry/gorouter", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.TPSPath, err = gexec.BuildIn(os.Getenv("TPS_GOPATH"), "github.com/cloudfoundry-incubator/tps", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	node.context.CircusZipPath = node.compileAndZipUpCircus()
}

func (node *nodeOneType) compileAndZipUpCircus() string {
	tailorPath, err := gexec.BuildIn(os.Getenv("LINUX_CIRCUS_GOPATH"), "github.com/cloudfoundry-incubator/linux-circus/tailor", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	spyPath, err := gexec.BuildIn(os.Getenv("LINUX_CIRCUS_GOPATH"), "github.com/cloudfoundry-incubator/linux-circus/spy", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	soldierPath, err := gexec.BuildIn(os.Getenv("LINUX_CIRCUS_GOPATH"), "github.com/cloudfoundry-incubator/linux-circus/soldier", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	circusDir, err := ioutil.TempDir("", "circus-dir")
	Ω(err).ShouldNot(HaveOccurred())

	err = os.Rename(tailorPath, filepath.Join(circusDir, "tailor"))
	Ω(err).ShouldNot(HaveOccurred())

	err = os.Rename(spyPath, filepath.Join(circusDir, "spy"))
	Ω(err).ShouldNot(HaveOccurred())

	err = os.Rename(soldierPath, filepath.Join(circusDir, "soldier"))
	Ω(err).ShouldNot(HaveOccurred())

	cmd := exec.Command("zip", "-v", "circus.zip", "tailor", "soldier", "spy")
	cmd.Stderr = GinkgoWriter
	cmd.Stdout = GinkgoWriter
	cmd.Dir = circusDir
	err = cmd.Run()
	Ω(err).ShouldNot(HaveOccurred())

	return filepath.Join(circusDir, "circus.zip")
}
