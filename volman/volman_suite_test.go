package volman_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfoundry-incubator/garden"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/world"
	"github.com/cloudfoundry-incubator/volman"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/pivotal-golang/lager"
	"github.com/pivotal-golang/lager/ginkgoreporter"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/pivotal-golang/localip"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
)

var (
	componentMaker world.ComponentMaker

	gardenProcess ifrit.Process
	gardenClient  garden.Client

	fakeDriverDir     string
	volmanClient      volman.Manager
	fakedriverProcess ifrit.Process

	logger lager.Logger
)

var _ = SynchronizedBeforeSuite(func() []byte {
	payload, err := json.Marshal(world.BuiltArtifacts{
		Executables: CompileTestedExecutables(),
	})
	Expect(err).NotTo(HaveOccurred())

	return payload
}, func(encodedBuiltArtifacts []byte) {
	var builtArtifacts world.BuiltArtifacts

	err := json.Unmarshal(encodedBuiltArtifacts, &builtArtifacts)
	Expect(err).NotTo(HaveOccurred())

	localIP, err := localip.LocalIP()
	Expect(err).NotTo(HaveOccurred())

	componentMaker = helpers.MakeComponentMaker(builtArtifacts, localIP)
})

var _ = BeforeEach(func() {
	logger = lagertest.NewTestLogger("Volman Inigo Tests")

	gardenProcess = ginkgomon.Invoke(componentMaker.GardenLinux())
	gardenClient = componentMaker.GardenClient()

	fakeDriverPath := componentMaker.Artifacts.Executables["fake-driver"]
	fakeDriverDir = filepath.Dir(strings.Split(fakeDriverPath, ",")[0])
	volmanClient = componentMaker.VolmanClient(fakeDriverDir)
	fakedriverProcess = ginkgomon.Invoke(componentMaker.VolmanDriver(logger, fakeDriverPath))
})

var _ = AfterEach(func() {
	destroyContainerErrors := helpers.CleanupGarden(gardenClient)

	helpers.StopProcesses(gardenProcess, fakedriverProcess)

	Expect(destroyContainerErrors).To(
		BeEmpty(),
		"%d containers failed to be destroyed!",
		len(destroyContainerErrors),
	)
})

func TestVolman(t *testing.T) {
	helpers.RegisterDefaultTimeouts()

	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t, "Volman Integration Suite", []Reporter{
		ginkgoreporter.New(GinkgoWriter),
	})
}

func CompileTestedExecutables() world.BuiltExecutables {
	var err error

	builtExecutables := world.BuiltExecutables{}

	builtExecutables["garden-linux"], err = gexec.BuildIn(os.Getenv("GARDEN_LINUX_GOPATH"), "github.com/cloudfoundry-incubator/garden-linux", "-race", "-a", "-tags", "daemon")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["fake-driver"], err = gexec.Build("github.com/cloudfoundry-incubator/volman/fakedriver/cmd/fakedriver", "-race")
	Expect(err).NotTo(HaveOccurred())

	return builtExecutables
}
