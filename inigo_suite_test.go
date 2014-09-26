package inigo_test

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/pivotal-golang/lager/ginkgoreporter"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"

	"github.com/cloudfoundry-incubator/garden/warden"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	"github.com/cloudfoundry-incubator/inigo/world"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry/gunk/timeprovider"
	"github.com/cloudfoundry/storeadapter/etcdstoreadapter"
	"github.com/cloudfoundry/storeadapter/workerpool"
	"github.com/cloudfoundry/yagnats"
)

var DEFAULT_EVENTUALLY_TIMEOUT = 15 * time.Second
var DEFAULT_CONSISTENTLY_DURATION = 5 * time.Second

var SHORT_TIMEOUT = 5 * time.Second
var LONG_TIMEOUT = 15 * time.Second

// use this for tests exercising docker; pulling can take a while
const DOCKER_PULL_ESTIMATE = 5 * time.Minute

const StackName = "lucid64"

var builtArtifacts world.BuiltArtifacts
var componentMaker world.ComponentMaker

var (
	plumbing      ifrit.Process
	wardenProcess ifrit.Process
	bbs           *Bbs.BBS
	natsClient    yagnats.NATSConn
	wardenClient  warden.Client
)

var _ = BeforeEach(func() {
	wardenLinux := componentMaker.WardenLinux()

	plumbing = ifrit.Invoke(grouper.NewParallel(nil, grouper.Members{
		{"etcd", componentMaker.Etcd()},
		{"nats", componentMaker.NATS()},
	}))

	wardenProcess = ifrit.Envoke(wardenLinux)

	wardenClient = wardenLinux.NewClient()

	var err error
	natsClient, err = yagnats.Connect([]string{"nats://" + componentMaker.Addresses.NATS})
	Ω(err).ShouldNot(HaveOccurred())

	adapter := etcdstoreadapter.NewETCDStoreAdapter([]string{"http://" + componentMaker.Addresses.Etcd}, workerpool.NewWorkerPool(20))

	err = adapter.Connect()
	Ω(err).ShouldNot(HaveOccurred())

	bbs = Bbs.NewBBS(adapter, timeprovider.NewTimeProvider(), lagertest.NewTestLogger("test"))

	inigo_server.Start(componentMaker.ExternalAddress)
})

var _ = AfterEach(func() {
	inigo_server.Stop(wardenClient)
})

var _ = AfterEach(func() {
	containers, err := wardenClient.Containers(nil)
	Ω(err).ShouldNot(HaveOccurred())

	for _, container := range containers {
		err := wardenClient.Destroy(container.Handle())
		Ω(err).ShouldNot(HaveOccurred())
	}

	helpers.StopProcess(wardenProcess)
})

var _ = AfterEach(func() {
	helpers.StopProcess(plumbing)
})

func TestInigo(t *testing.T) {
	registerDefaultTimeouts()

	RegisterFailHandler(Fail)

	SynchronizedBeforeSuite(func() []byte {
		payload, err := json.Marshal(world.BuiltArtifacts{
			Executables:  CompileTestedExecutables(),
			Circuses:     CompileAndZipUpCircuses(),
			DockerCircus: CompileAndZipUpDockerCircus(),
		})
		Ω(err).ShouldNot(HaveOccurred())

		return payload
	}, func(encodedBuiltArtifacts []byte) {
		var err error

		if os.Getenv("SHORT_TIMEOUT") != "" {
			SHORT_TIMEOUT, err = time.ParseDuration(os.Getenv("SHORT_TIMEOUT"))
			Ω(err).ShouldNot(HaveOccurred())
		}

		if os.Getenv("LONG_TIMEOUT") != "" {
			LONG_TIMEOUT, err = time.ParseDuration(os.Getenv("LONG_TIMEOUT"))
			Ω(err).ShouldNot(HaveOccurred())
		}

		err = json.Unmarshal(encodedBuiltArtifacts, &builtArtifacts)
		Ω(err).ShouldNot(HaveOccurred())

		addresses := world.ComponentAddresses{
			WardenLinux:    fmt.Sprintf("127.0.0.1:%d", 10000+config.GinkgoConfig.ParallelNode),
			NATS:           fmt.Sprintf("127.0.0.1:%d", 11000+config.GinkgoConfig.ParallelNode),
			Etcd:           fmt.Sprintf("127.0.0.1:%d", 12000+config.GinkgoConfig.ParallelNode),
			EtcdPeer:       fmt.Sprintf("127.0.0.1:%d", 12500+config.GinkgoConfig.ParallelNode),
			Executor:       fmt.Sprintf("127.0.0.1:%d", 13000+config.GinkgoConfig.ParallelNode),
			Rep:            fmt.Sprintf("127.0.0.1:%d", 14000+config.GinkgoConfig.ParallelNode),
			LoggregatorIn:  fmt.Sprintf("127.0.0.1:%d", 15000+config.GinkgoConfig.ParallelNode),
			LoggregatorOut: fmt.Sprintf("127.0.0.1:%d", 16000+config.GinkgoConfig.ParallelNode),
			FileServer:     fmt.Sprintf("127.0.0.1:%d", 17000+config.GinkgoConfig.ParallelNode),
			Router:         fmt.Sprintf("127.0.0.1:%d", 18000+config.GinkgoConfig.ParallelNode),
			TPS:            fmt.Sprintf("127.0.0.1:%d", 19000+config.GinkgoConfig.ParallelNode),
			FakeCC:         fmt.Sprintf("127.0.0.1:%d", 20000+config.GinkgoConfig.ParallelNode),
		}

		wardenBinPath := os.Getenv("WARDEN_BINPATH")
		wardenRootFSPath := os.Getenv("WARDEN_ROOTFS")
		wardenGraphPath := os.Getenv("WARDEN_GRAPH_PATH")
		externalAddress := os.Getenv("EXTERNAL_ADDRESS")

		if wardenGraphPath == "" {
			wardenGraphPath = os.TempDir()
		}

		Ω(wardenBinPath).ShouldNot(BeEmpty(), "must provide $WARDEN_BINPATH")
		Ω(wardenRootFSPath).ShouldNot(BeEmpty(), "must provide $WARDEN_ROOTFS")
		Ω(externalAddress).ShouldNot(BeEmpty(), "must provide $EXTERNAL_ADDRESS")

		componentMaker = world.ComponentMaker{
			Artifacts: builtArtifacts,
			Addresses: addresses,

			Stack: StackName,

			ExternalAddress: externalAddress,

			WardenBinPath:    wardenBinPath,
			WardenRootFSPath: wardenRootFSPath,
			WardenGraphPath:  wardenGraphPath,
		}
	})

	BeforeEach(func() {
		currentTestDescription := CurrentGinkgoTestDescription()
		fmt.Fprintf(GinkgoWriter, "\n%s\n%s\n\n", strings.Repeat("~", 50), currentTestDescription.FullTestText)
	})

	RunSpecsWithDefaultAndCustomReporters(t, "Inigo Integration Suite", []Reporter{
		ginkgoreporter.New(GinkgoWriter),
	})
}

func registerDefaultTimeouts() {
	var err error
	if os.Getenv("DEFAULT_EVENTUALLY_TIMEOUT") != "" {
		DEFAULT_EVENTUALLY_TIMEOUT, err = time.ParseDuration(os.Getenv("DEFAULT_EVENTUALLY_TIMEOUT"))
		if err != nil {
			panic(err)
		}
	}

	if os.Getenv("DEFAULT_CONSISTENTLY_DURATION") != "" {
		DEFAULT_CONSISTENTLY_DURATION, err = time.ParseDuration(os.Getenv("DEFAULT_CONSISTENTLY_DURATION"))
		if err != nil {
			panic(err)
		}
	}

	SetDefaultEventuallyTimeout(DEFAULT_EVENTUALLY_TIMEOUT)
	SetDefaultConsistentlyDuration(DEFAULT_CONSISTENTLY_DURATION)

	// most things hit some component; don't hammer it
	SetDefaultConsistentlyPollingInterval(100 * time.Millisecond)
	SetDefaultEventuallyPollingInterval(500 * time.Millisecond)
}

func CompileTestedExecutables() world.BuiltExecutables {
	var err error

	builtExecutables := world.BuiltExecutables{}

	builtExecutables["garden-linux"], err = gexec.BuildIn(os.Getenv("GARDEN_LINUX_GOPATH"), "github.com/cloudfoundry-incubator/garden-linux", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["loggregator"], err = gexec.BuildIn(os.Getenv("LOGGREGATOR_GOPATH"), "loggregator")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["auctioneer"], err = gexec.BuildIn(os.Getenv("AUCTIONEER_GOPATH"), "github.com/cloudfoundry-incubator/auctioneer", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["exec"], err = gexec.BuildIn(os.Getenv("EXECUTOR_GOPATH"), "github.com/cloudfoundry-incubator/executor", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["converger"], err = gexec.BuildIn(os.Getenv("CONVERGER_GOPATH"), "github.com/cloudfoundry-incubator/converger", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["rep"], err = gexec.BuildIn(os.Getenv("REP_GOPATH"), "github.com/cloudfoundry-incubator/rep", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["stager"], err = gexec.BuildIn(os.Getenv("STAGER_GOPATH"), "github.com/cloudfoundry-incubator/stager", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["nsync-listener"], err = gexec.BuildIn(os.Getenv("NSYNC_GOPATH"), "github.com/cloudfoundry-incubator/nsync/listener", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["nsync-bulker"], err = gexec.BuildIn(os.Getenv("NSYNC_GOPATH"), "github.com/cloudfoundry-incubator/nsync/bulker", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["file-server"], err = gexec.BuildIn(os.Getenv("FILE_SERVER_GOPATH"), "github.com/cloudfoundry-incubator/file-server", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["route-emitter"], err = gexec.BuildIn(os.Getenv("ROUTE_EMITTER_GOPATH"), "github.com/cloudfoundry-incubator/route-emitter", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["router"], err = gexec.BuildIn(os.Getenv("ROUTER_GOPATH"), "github.com/cloudfoundry/gorouter", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["tps"], err = gexec.BuildIn(os.Getenv("TPS_GOPATH"), "github.com/cloudfoundry-incubator/tps", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	return builtExecutables
}

func CompileAndZipUpCircuses() world.BuiltCircuses {
	builtCircuses := world.BuiltCircuses{}

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

	builtCircuses[StackName] = filepath.Join(circusDir, "circus.zip")

	return builtCircuses
}

func CompileAndZipUpDockerCircus() string {
	tailorPath, err := gexec.BuildIn(os.Getenv("DOCKER_CIRCUS_GOPATH"), "github.com/cloudfoundry-incubator/docker-circus/tailor", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	spyPath, err := gexec.BuildIn(os.Getenv("DOCKER_CIRCUS_GOPATH"), "github.com/cloudfoundry-incubator/docker-circus/spy", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	soldierPath, err := gexec.BuildIn(os.Getenv("DOCKER_CIRCUS_GOPATH"), "github.com/cloudfoundry-incubator/docker-circus/soldier", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	circusDir, err := ioutil.TempDir("", "circus-dir")
	Ω(err).ShouldNot(HaveOccurred())

	err = os.Rename(tailorPath, filepath.Join(circusDir, "tailor"))
	Ω(err).ShouldNot(HaveOccurred())

	err = os.Rename(spyPath, filepath.Join(circusDir, "spy"))
	Ω(err).ShouldNot(HaveOccurred())

	err = os.Rename(soldierPath, filepath.Join(circusDir, "soldier"))
	Ω(err).ShouldNot(HaveOccurred())

	cmd := exec.Command("zip", "-v", "docker-circus.zip", "tailor", "soldier", "spy")
	cmd.Stderr = GinkgoWriter
	cmd.Stdout = GinkgoWriter
	cmd.Dir = circusDir
	err = cmd.Run()
	Ω(err).ShouldNot(HaveOccurred())

	return filepath.Join(circusDir, "docker-circus.zip")
}
