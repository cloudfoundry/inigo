package executor_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/pivotal-golang/lager/ginkgoreporter"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"

	garden "github.com/cloudfoundry-incubator/garden/api"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/world"
)

var builtArtifacts world.BuiltArtifacts
var componentMaker world.ComponentMaker

var (
	gardenProcess ifrit.Process
	gardenClient  garden.Client
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

	componentMaker = helpers.MakeComponentMaker(builtArtifacts)
})

var _ = BeforeEach(func() {
	currentTestDescription := CurrentGinkgoTestDescription()
	fmt.Fprintf(GinkgoWriter, "\n%s\n%s\n\n", strings.Repeat("~", 50), currentTestDescription.FullTestText)

	gardenProcess = ginkgomon.Invoke(componentMaker.GardenLinux())

	gardenClient = componentMaker.GardenClient()
})

var _ = AfterEach(func() {
	destroyContainerErrors := helpers.CleanupGarden(gardenClient)

	helpers.StopProcesses(gardenProcess)

	Ω(destroyContainerErrors).Should(
		BeEmpty(),
		"%d containers failed to be destroyed!",
		len(destroyContainerErrors),
	)
})

func TestExecutor(t *testing.T) {
	helpers.RegisterDefaultTimeouts()

	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t, "Executor Integration Suite", []Reporter{
		ginkgoreporter.New(GinkgoWriter),
	})
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
