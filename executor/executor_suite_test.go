package executor_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/pivotal-golang/lager/ginkgoreporter"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"

	garden_api "github.com/cloudfoundry-incubator/garden/api"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/world"
)

var DEFAULT_EVENTUALLY_TIMEOUT = 1 * time.Minute
var DEFAULT_CONSISTENTLY_DURATION = 1 * time.Second

var builtArtifacts world.BuiltArtifacts
var componentMaker world.ComponentMaker

var (
	gardenProcess ifrit.Process
	gardenClient  garden_api.Client
)

var _ = SynchronizedBeforeSuite(func() []byte {
	payload, err := json.Marshal(world.BuiltArtifacts{
		Executables: CompileTestedExecutables(),
	})
	Ω(err).ShouldNot(HaveOccurred())

	return payload
}, func(encodedBuiltArtifacts []byte) {
	var err error

	err = json.Unmarshal(encodedBuiltArtifacts, &builtArtifacts)
	Ω(err).ShouldNot(HaveOccurred())

	addresses := world.ComponentAddresses{
		GardenLinux: fmt.Sprintf("127.0.0.1:%d", 10000+config.GinkgoConfig.ParallelNode),
		Executor:    fmt.Sprintf("127.0.0.1:%d", 13000+config.GinkgoConfig.ParallelNode),
	}

	gardenBinPath := os.Getenv("GARDEN_BINPATH")
	gardenRootFSPath := os.Getenv("GARDEN_ROOTFS")
	gardenGraphPath := os.Getenv("GARDEN_GRAPH_PATH")
	externalAddress := os.Getenv("EXTERNAL_ADDRESS")

	if gardenGraphPath == "" {
		gardenGraphPath = os.TempDir()
	}

	Ω(gardenBinPath).ShouldNot(BeEmpty(), "must provide $GARDEN_BINPATH")
	Ω(gardenRootFSPath).ShouldNot(BeEmpty(), "must provide $GARDEN_ROOTFS")
	Ω(externalAddress).ShouldNot(BeEmpty(), "must provide $EXTERNAL_ADDRESS")

	componentMaker = world.ComponentMaker{
		Artifacts: builtArtifacts,
		Addresses: addresses,

		ExternalAddress: externalAddress,

		GardenBinPath:    gardenBinPath,
		GardenRootFSPath: gardenRootFSPath,
		GardenGraphPath:  gardenGraphPath,
		ExecutorTmpDir:   path.Join(os.TempDir(), fmt.Sprintf("executor_%d", GinkgoParallelNode())),
	}
})

var _ = BeforeEach(func() {
	currentTestDescription := CurrentGinkgoTestDescription()
	fmt.Fprintf(GinkgoWriter, "\n%s\n%s\n\n", strings.Repeat("~", 50), currentTestDescription.FullTestText)

	gardenLinux := componentMaker.GardenLinux()

	gardenProcess = ginkgomon.Invoke(gardenLinux)
	gardenClient = gardenLinux.NewClient()
})

var _ = AfterEach(func() {
	containers, err := gardenClient.Containers(nil)
	Ω(err).ShouldNot(HaveOccurred())

	// even if containers fail to destroy, stop garden, but still report the
	// errors
	destroyContainerErrors := []error{}
	for _, container := range containers {
		err := gardenClient.Destroy(container.Handle())
		if err != nil {
			destroyContainerErrors = append(destroyContainerErrors, err)
		}
	}

	helpers.StopProcess(gardenProcess)

	Ω(destroyContainerErrors).Should(
		BeEmpty(),
		"%d of %d containers failed to be destroyed!",
		len(destroyContainerErrors),
		len(containers),
	)
})

func TestExecutor(t *testing.T) {
	registerDefaultTimeouts()

	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t, "Executor Integration Suite", []Reporter{
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

	builtExecutables["garden-linux"], err = gexec.BuildIn(os.Getenv("GARDEN_LINUX_GOPATH"), "github.com/cloudfoundry-incubator/garden-linux", "-race", "-a", "-tags", "daemon")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["exec"], err = gexec.BuildIn(os.Getenv("EXECUTOR_GOPATH"), "github.com/cloudfoundry-incubator/executor/cmd/executor", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	return builtExecutables
}
